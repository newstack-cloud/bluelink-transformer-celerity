# Close remaining gaps in `celerity/handler` resolve

## Context

`resources/handler/handler_resolve.go` today does two things well — classifies inbound/outbound link edges, and applies per-field inheritance (handler spec > linked `celerity/handlerConfig` > `metadata.sharedHandlerConfig` > schema defaults), writing results back into `Resource.Spec.Fields`. Nine unit tests cover this and all pass.

Comparing against the **aws-serverless design doc** ([handler-aws-serverless-design.md](handler-aws-serverless-design.md) lines 30–43), the **shared contract** ([../contract/index.md](../contract/index.md) §1.1, §2.2), and the **upstream handler spec** (`celerity-docs: content/docs/framework/applications/resources/celerity-handler.mdx`), four resolve-phase obligations are **not yet implemented**. They block correct downstream emit: `CELERITY_HANDLER_TYPE` / `CELERITY_HANDLER_TAG` injection, VPC subnet placement, and fatal rejection of an unrunnable handler.

Root technical fact (verified): `linktypes.ResolvedLink` carries **no annotations** (only `Source/Target/SourceType/TargetType/SelectorKeys`). All `celerity.handler.*` annotations live on the **handler resource's own** `Metadata.Annotations` (a `*schema.StringOrSubstitutionsMap`), and **nothing in the repo reads them today**. The `pluginutils.GetStringAnnotation/GetBoolAnnotation` helpers are unusable here — they expect resolved-subs `*core.MappingNode`, not the raw pre-substitution map the resolve phase sees.

## The gaps (analysis)

| # | Gap | Source of requirement | Current state |
|---|---|---|---|
| 1 | **Empty runtime after inheritance is not fatal** | contract §1.1 ("a handler that resolves to an empty runtime after inheritance … is a fatal transform error"); design line 43 | `resolveHandler` always returns `(resolved, nil)`; runtime has no default, so it silently stays unset |
| 2 | **`EventSource` discriminator not derived** | design line 35; contract §2.2 (`CELERITY_HANDLER_TYPE` always set) | not derived; no annotation reading |
| 3 | **`RoutingTag` not derived** | design line 36; contract §2.2 (`CELERITY_HANDLER_TAG`) | not derived |
| 4 | **`VPCSubnetType` not derived** | design line 37; mdx `celerity.handler.vpc.subnetType` (public\|private, default public) | VPC edge is classified, but the annotation is never read |
| – | (enabler) **no annotation-reading helper exists** | — | underlies #2–#4 |
| – | (file-org) `ResolvedHandler` should move to `handler_resolved.go` | design line 30 | still in `handler_resolve.go` |

**Out of scope** (correctly belongs elsewhere or is post-v0):
- *Unrecognised-but-present* runtime ID → already a fatal diagnostic in **emit** (`getTargetRuntime`), not resolve.
- `celerity/workflow` and `celerity/channel` inbound links / their annotations → **v1+** per the mdx.
- Cardinality + annotation-schema enforcement (empty link defs in `links/`) → a separate link-defs workstream (design gap #2); resolve "trusts" it for now.
- Typed `Runtime/Memory/Timeout/…` fields on `ResolvedHandler` (design line 34) → **deferred follow-up** (decision below); the rewriter still needs the spec MappingNode, so adopting them now only adds a parallel representation.

## Decisions (confirmed)
- **Scope of resolved fields: minimal.** Add only `EventSource`, `RoutingTag` (+`HasRoutingTag`), `VPCSubnetType` as typed fields. Leave runtime/memory/etc. as the existing spec-mutation. Emit stays untouched; the 9 existing tests stay green. Full typed-field migration (design line 34) is a tracked follow-up.
- **Multi-source ambiguity: deterministic default.** When a handler is linked from multiple kinds and no disambiguating annotation is set, pick by precedence **api > consumer > schedule** and pin it with a test. A stricter `ValidateLinks` diagnostic is a future enhancement.
- **Missing-runtime: return a fatal `error` from `resolveHandler`** (not a `core.Diagnostic`), consistent with the project principle that resolve returns a value or a run-aborting error. This still satisfies the design's "fatal" intent.

## Plan

### New file — `resources/handler/handler_annotations.go` (package `handler`)
Pure annotation parsing over `*schema.StringOrSubstitutionsMap`. A value counts as a usable literal only when it is a single pure-literal element (no `${...}` substitution); substitution-valued annotations are treated as "not set" (resolve can't see resolved subs).

- key constants: `celerity.handler.http`, `.websocket`, `.consumer`, `.schedule`, `.consumer.route`, `.vpc.subnetType`; subnet enum consts `public`/`private`.
- `rawAnnotation(meta, key) (*substitutions.StringOrSubstitutions, bool)` — nil-safe map lookup.
- `literalOf(sos) (string, bool)` — true only for a one-element pure `StringValue`.
- `annotationStringLiteral(meta, key) (string, bool)`.
- `annotationBool(meta, key) (present, value bool)` — two-bool form so callers distinguish absent vs explicit-false; `"true"`/`"false"` (case-insensitive) accepted, anything else → not present.

Imports: `.../blueprint/substitutions` (new), `.../blueprint/schema` (existing).

### New file — `resources/handler/handler_resolved.go`
Move `ResolvedHandler` + `ResourceName()`/`ResourceType()` here from `handler_resolve.go`. Add:
- `EventSource EventSource` (new `type EventSource string` + consts `http|websocket|consumer|schedule|custom`).
- `RoutingTag string`, `HasRoutingTag bool`.
- `VPCSubnetType string`.

### Modify — `resources/handler/handler_resolve.go`
- Add `"fmt"` import; remove the moved struct.
- After `resolveInheritedSpec`, add the fatal check (runs **after** inheritance so a runtime supplied only by handlerConfig/sharedHandlerConfig still passes):
  ```go
  if resolvedRuntime(resolved) == "" {
      return nil, fmt.Errorf(
          "celerity/handler %q resolves to an empty runtime after inheritance; "+
          "set spec.runtime on the handler, a linked celerity/handlerConfig, or "+
          "metadata.sharedHandlerConfig", name)
  }
  ```
- Add `resolvedRuntime(*ResolvedHandler) string` (reads the resolved spec field).
- Add three pure derivers, called after edge classification, reading `target` edges + `target.Resource.Metadata`:
  - `deriveEventSource(target) EventSource` — enumerate present kinds (`APILink != nil`, `len(Consumers)>0`, `len(Schedules)>0`). Zero kinds → `custom`. One kind → that kind (api → step below). Multiple → explicit `true` flag selects (api via http/websocket flag, else consumer, else schedule); if none set → default precedence **api > consumer > schedule**. Resolve "api" → `websocket` if `websocket=true`, else `http` (default; both-true → `http`).
  - `deriveRoutingTag(target, eventSource) (string, bool)` — only for `consumer` source with a literal `celerity.handler.consumer.route`; **schedule key left unset** in this PR (no annotation defined in v0 mdx — flagged spec gap); http/websocket/custom never tagged.
  - `deriveVPCSubnetType(target) string` — `""` when no VPC; else literal value if `public`/`private`; malformed or absent → `public`.
- Assign all three onto `resolved` before returning.

### Tests — `resources/handler/handler_resolve_test.go` (keep the 9; add)
- Helpers: `baseHandlerWithAnnotations(map[string]string, extra...)` building `Metadata.Annotations` as literal `StringOrSubstitutions`; a substitution-valued annotation builder; extend `fakeLinkGraph` with an inbound (`to`) map for api/consumer/schedule/vpc.
- `resolveForTest` must tolerate an error (or add `resolveForTestExpectingError`).
- Cases: missing-runtime fatal; runtime-via-handlerConfig / via-sharedConfig **not** fatal; EventSource single-kind (api→http, api+websocket→websocket, consumer, schedule, none→custom); multi-kind (each flag; none→`http` default pinned); both http+websocket→`http`; substitution-valued flag treated as unset; RoutingTag (consumer.route set/unset, route-but-http→unset); VPCSubnetType (private, default public, invalid→public, no-vpc→"").

## Critical files
- `resources/handler/handler_resolve.go` (modify)
- `resources/handler/handler_resolved.go` (new)
- `resources/handler/handler_annotations.go` (new)
- `resources/handler/handler_resolve_test.go` (extend)
- Reference only: [handler-aws-serverless-design.md](handler-aws-serverless-design.md) (lines 30–43), [../contract/index.md](../contract/index.md) (§1.1, §2.2), `celerity-handler.mdx` (annotations ~330–470). Per the contract maintenance rule, fix design line 37's "link annotation" wording → handler metadata annotation in the same PR.

## Verification
- `go build ./...` and `go vet ./...`.
- Unit tests with the existing tag: `go test -tags unit ./resources/handler/...` (the file is `//go:build unit`). All 9 prior + new cases green.
- Spot-check: a handler with no `runtime` anywhere now fails the transform with the named error; an api-linked handler resolves `EventSource=http`; a vpc-linked handler with `celerity.handler.vpc.subnetType=private` resolves `VPCSubnetType=private`.

## Tracked follow-ups (not in this PR)
- Migrate runtime/memory/timeout/tracingEnabled/codeLocation/env to typed `ResolvedHandler` fields and point emit at them (design line 34).
- Schedule routing-key source (no v0 annotation exists) — resolve with SDK/CLI owners.
- `ValidateLinks` diagnostics for: substitution-valued discriminator annotations, http+websocket conflict, multi-kind with no disambiguator, orphan `consumer.route`.
