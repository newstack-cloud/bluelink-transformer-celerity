# Resolve-Emit Transformation Pipeline Design

## Context

The current `transformBlueprint()` iterates the resource map and dispatches each resource independently via a switch statement. This breaks down when:
- Resources like `handlerConfig` don't produce their own output — they fold into handlers
- A handler needs to know about inbound links (consumers, schedules, VPC) to build event source mappings, triggers, and networking config
- Both sides of a link (e.g., `handler→queue`) would see the same edge, leading to unclear ownership of processing

This document covers both the framework prerequisites needed to unblock the transformer and the transformer's internal resolve→emit architecture.

---

## Part 1: Framework Prerequisites

These changes go into the blueprint and plugin-framework libs. They should be batched together to avoid repeated version bumps across downstream libraries and applications.

### 1. Link Graph Edges in Transform Request

`SpecTransformerTransformInput` currently does not include a `DeclaredLinkGraph`. The link graph is only available in `SpecTransformerValidateLinksInput` for the `ValidateLinks` method. The transformer needs link structure during transformation, not just validation.

**Changes:**

- **Proto** (`transformerserverv1/transformer.proto`): The proto already has a `DeclaredLinkGraph` message (used by `ValidateLinksRequest`) with flat `edges` + a `resources` map keyed by name. There is also a dangling `LinkGraphEdges link_graph_edges = 2` reference on `BlueprintTransformRequest` whose message is never defined. Resolve both at once by extending the existing `DeclaredLinkGraph` with pre-indexed edge maps and reusing it for both RPCs:

  ```protobuf
  message DeclaredLinkGraph {
      // All edges in the graph (for Edges()).
      repeated ResolvedLink edges = 1;
      // Resources in the graph, keyed by resource name. The plugin uses
      // this with the blueprint's resource map to answer Resource(name).
      map<string, DeclaredLinkGraphEntry> resources = 2;
      // Edges keyed by source resource name (for EdgesFrom(name)).
      map<string, ResolvedLinkList> edges_from = 3;
      // Edges keyed by target resource name (for EdgesTo(name)).
      map<string, ResolvedLinkList> edges_to = 4;
  }

  message ResolvedLinkList {
      repeated ResolvedLink links = 1;
  }

  message BlueprintTransformRequest {
      schema.Blueprint input_blueprint = 1;
      DeclaredLinkGraph link_graph = 2;  // replaces undefined LinkGraphEdges
      string host_id = 3;
      TransformerContext context = 4;
  }
  ```

  Edges contain only tuples (source, target, sourceType, targetType, selectorKeys) — no embedded resource specs. Sending the indexes alongside the flat edge list preserves the host's already-built indexes and gives the plugin O(1) `EdgesFrom`/`EdgesTo` lookups. `ValidateLinks` plugins benefit from the same indexed lookups — cardinality and per-edge validation passes both query `EdgesFrom`/`EdgesTo` heavily. The change is additive on `DeclaredLinkGraph` (fields 3 and 4 are new), so existing `ValidateLinks` callers stay compatible.

- **Plugin-framework SDK**: Reconstruct `linktypes.DeclaredLinkGraph` on the plugin side from the proto `DeclaredLinkGraph` + the blueprint's resource map (which is already deserialized). The `Resource(name)` method delegates to `blueprint.Resources.Values[name]` so resource specs aren't duplicated. `EdgesFrom(name)` and `EdgesTo(name)` are direct map lookups. One reconstruction path serves both `ValidateLinks` and `Transform`.
- **Framework host side**: Call `EnumerateDeclaredLinks()` before the Transform RPC, serialize the graph's edges + indexes (no resource specs). Same serialization is reused for the `ValidateLinks` RPC.
- **Go types** (`libs/blueprint/transform/transform.go`): Add `LinkGraph linktypes.DeclaredLinkGraph` to `SpecTransformerTransformInput`.

### 2. Diagnostics in Transform Response

`BlueprintTransformResponse` currently supports only success or total failure (oneof blueprint | error). The transformer needs to report partial warnings (e.g., missing build manifest, deprecated config) alongside a successful transformation.

**Changes:**

- **Proto**: Change `BlueprintTransformResponse` to include diagnostics alongside the blueprint, matching the pattern used by `ValidateLinksResponse`.
- **Go types** (`libs/blueprint/transform/transform.go`): Add `Diagnostics []*core.Diagnostic` to `SpecTransformerTransformOutput`.
- **Use cases**: Missing build manifest (warning, proceed with stubs), ambiguous handlerConfig match (error), deprecated resource config (warning).

### 3. Substitution Reference Rewriting Utilities

When an abstract resource expands into concrete resources, substitution references in carried-through spec fields (e.g., user-defined environment variables) still use abstract resource names and property paths. These need to be rewritten to point to the correct concrete resources and properties during the emit phase.

No walker/visitor exists today — the substitution engine resolves values but never rewrites the tree.

#### 3a. Substitution Walker (Blueprint Lib)

**Location**: `libs/blueprint/subwalk/walk.go` (separate package to avoid a `substitutions → core` import cycle — `core.MappingNode` already imports `substitutions`, so the walker over both lives downstream of both).

A generic visitor that traverses `StringOrSubstitutions` values. Each `Substitution` is a discriminated union with these variants: `SubstitutionResourceProperty`, `SubstitutionVariable`, `SubstitutionValueReference`, `SubstitutionFunctionExpr`, `SubstitutionDataSourceProperty`, `SubstitutionChild`, `SubstitutionElemReference`, and literals.

```go
// SubstitutionVisitor is called for each substitution encountered during traversal.
// Returning a non-nil *Substitution replaces the original (rewrite); returning nil keeps it.
type SubstitutionVisitor func(sub *Substitution) *Substitution

// WalkStringOrSubstitutions traverses a single StringOrSubstitutions value,
// calling the visitor for each Substitution found (including nested ones
// inside function arguments).
//
// Traversal order: bottom-up (post-order). Leaf substitutions
// (literals, resource refs, variable refs, value refs, etc.) are visited
// first; SubstitutionFunctionExpr nodes are visited only after each of
// their arguments has been visited and any returned replacements have
// been wrapped back into the function expression. This guarantees that:
//
//   1. A rewriter replacing an inner resource ref like
//      ${upper(resources.X.spec.name)} sees the inner SubstitutionResourceProperty
//      first and can return a replacement; the function expression is then
//      visited with the rewritten argument already in place.
//
//   2. Top-down would short-circuit children if the outer node is replaced,
//      so nested resource refs inside function arguments would never be
//      visited. Bottom-up avoids this entirely.
func WalkStringOrSubstitutions(
    sos *StringOrSubstitutions,
    visitor SubstitutionVisitor,
) *StringOrSubstitutions

// WalkMappingNode recursively traverses a core.MappingNode tree,
// finding all embedded StringOrSubstitutions values and calling the visitor.
// MappingNodes store resource specs as untyped trees — substitution references
// like ${resources.X.spec.Y} live inside scalar leaves.
//
// Traversal is bottom-up at every level: child MappingNodes are walked
// before their parents, and within each StringOrSubstitutions value the
// substitution tree itself is walked bottom-up (see above).
func WalkMappingNode(
    node *core.MappingNode,
    visitor SubstitutionVisitor,
) *core.MappingNode
```

The walker handles recursion into `SubstitutionFunctionExpr.Arguments` (which contain nested `Substitution` values) and `core.MappingNode` children, in both cases visiting children before parents so rewrites compose cleanly from the leaves outward.

**Design decisions:**
- Visitor is a function, not an interface — keeps it simple when most rewrites only care about one substitution variant.
- Returns new values rather than mutating in place — safer for shared pointers in the blueprint tree.
- No blueprint-level walker — rewriting happens inline during emit on specific fields, not as a post-processing pass on the whole output blueprint.

#### 3b. Resource Reference Rewriter (Plugin Framework)

**Location**: `libs/plugin-framework/sdk/pluginutils/rewrite_refs.go`

The rewriting is more than name mapping — abstract spec properties may map to different concrete property names at any nesting depth, and some abstract properties become blueprint `values` (derivative properties resolved at deploy time) rather than direct concrete resource refs.

Examples:
- `${resources.myHandler.spec.memory}` → `${resources.myHandler_lambda.spec.memorySize}` (property rename)
- `${resources.myHandler.spec.arn}` → `${values.myHandler_lambda_arn}` (resource ref → value ref)
- `${resources.myQueue.spec.name}` → `${resources.myQueue_sqs.spec.queueName}` (name + property change)
- `${resources.myHandler.spec.vpc.securityGroups[0]}` → `${resources.myHandler_lambda.spec.vpcConfig.securityGroupIds[0]}` (nested property restructure)
- `${resources.myHandler.spec.environmentVariables[2].value}` → `${resources.myHandler_lambda.spec.environment.variables[2].value}` (structural reshape with array index preservation)

```go
// ResourcePropertyRewriter is called for each SubstitutionResourceProperty
// found during traversal. It receives the full SubstitutionResourceProperty
// (resource name, property path at any depth, template index) and returns:
// - A replacement *Substitution (any variant: resource ref, value ref, etc.)
// - nil to keep the original unchanged
type ResourcePropertyRewriter func(
    ref *SubstitutionResourceProperty,
) *Substitution

// RewriteResourcePropertyRefs builds a SubstitutionVisitor from a
// ResourcePropertyRewriter. Only SubstitutionResourceProperty substitutions
// are passed to the rewriter; all other substitution types pass through.
func RewriteResourcePropertyRefs(rewriter ResourcePropertyRewriter) SubstitutionVisitor

// ChainResourcePropertyRewriters combines multiple rewriters into one.
// The first rewriter to return a non-nil substitution wins.
func ChainResourcePropertyRewriters(
    rewriters ...ResourcePropertyRewriter,
) ResourcePropertyRewriter
```

The `SubstitutionResourceProperty.Path` is a `[]SubstitutionPathItem` where each item has `FieldName` (string) and `ArrayIndex` (optional int). The path represents the full depth of the reference.

| Abstract reference | Path items |
|---|---|
| `.spec.memory` | `[{spec}, {memory}]` |
| `.spec.vpc.securityGroups[0]` | `[{spec}, {vpc}, {securityGroups}, {[0]}]` |
| `.spec.environmentVariables[2].value` | `[{spec}, {environmentVariables}, {[2]}, {value}]` |

**Path matching helpers:**

```go
// PathMatches returns true if the ref's path starts with the given field names,
// ignoring array indices. Useful for prefix matching on nested structures.
func PathMatches(ref *SubstitutionResourceProperty, fieldPathSegments ...string) bool

// PathExact returns true if the ref's path matches the given field names exactly
// (ignoring array indices between field names).
func PathExact(ref *SubstitutionResourceProperty, fieldPathSegments ...string) bool
```

**Substitution construction helpers:**

These build replacement `*Substitution` values from a source ref. They have no resource-specific knowledge — they're structural manipulators on `SubstitutionResourceProperty` and `Substitution`, so they live alongside the matchers and are reused by every per-resource rewriter.

```go
// RetargetRef returns a SubstitutionResourceProperty identical to ref but
// pointing at newResourceName. The path (including array indices) is preserved.
// Use when only the resource name changes.
func RetargetRef(ref *SubstitutionResourceProperty, newResourceName string) *Substitution

// RewriteFields is the declarative high-level helper: rename fields in
// ref.Path one-to-one, with N-dimensional array indices auto-preserved at
// their original relative positions. The i-th field-name item in ref.Path
// becomes newFields[i]; index items sandwiched between fields are kept at
// the same relative slot (between the renamed fields they followed). Source
// items beyond len(newFields) field positions are appended unchanged.
//
// Examples (paths shown logically; "[i]" stands for any source array index):
//   .spec.memory                  -> .spec.memorySize
//       newFields = "spec", "memorySize"
//   .spec.routes[i].method        -> .spec.paths[i].httpMethod
//       newFields = "spec", "paths", "httpMethod"     (one-dimensional)
//   .spec.rules[i].targets[j].arn -> .spec.rules[i].destinations[j].arn
//       newFields = "spec", "rules", "destinations", "arn"   (two-dimensional)
//
// Use MakeRef when the rewrite needs to insert or remove fields, restructure
// nesting depth (e.g. environmentVariables -> environment.variables), or
// introduce literal array indices that don't exist in the source path —
// see the section 3d escape-hatch cases for examples.
func RewriteFields(
    ref *SubstitutionResourceProperty,
    newResourceName string,
    newFields ...string,
) *Substitution

// MakeRef is the low-level constructor: build a SubstitutionResourceProperty
// pointing at newResourceName with the given path. The caller assembles the
// path explicitly from Field / Index items and (where useful) slices of
// ref.Path. Used for cases that don't fit RewriteFields' 1:1 model.
func MakeRef(newResourceName string, path []SubstitutionPathItem) *Substitution

// Field is sugar for a literal field-name path item:
//   SubstitutionPathItem{FieldName: name}.
func Field(name string) SubstitutionPathItem

// Index is sugar for a literal array-index path item:
//   SubstitutionPathItem{ArrayIndex: &n}.
func Index(n int) SubstitutionPathItem

// ValueRef returns a SubstitutionValueReference. With no path items it is
// the flat form ${values.<name>}; trailing path items target a nested field
// or array element when the transformer-generated value is a complex object
// or list. Path items are built with Field / Index, identical to resource refs.
//
// Examples:
//   ValueRef("ordersHandler_lambda_arn")
//       -> ${values.ordersHandler_lambda_arn}
//   ValueRef("ordersDb_connection", Field("host"))
//       -> ${values.ordersDb_connection.host}
//   ValueRef("api_endpoints", Index(0), Field("url"))
//       -> ${values.api_endpoints[0].url}
func ValueRef(valueName string, path ...SubstitutionPathItem) *Substitution
```

#### 3c. Usage in the Transformer

Rewriting happens **inline during emit**. When an emit function copies or transforms spec fields from an abstract resource into a concrete resource, any user-written substitution references in those fields still use abstract names and property paths. The emit function applies the rewriter to each carried-through field.

```go
// resources/handler/handler_aws.go — during emit

func EmitAWS(resolved *ResolvedHandler, ctx transform.Context) (map[string]*schema.Resource, error) {
    rewriter := HandlerPropertyRewriter(resolved.Name, resolved.Name + "_lambda")

    // User-defined env vars carry through — rewrite abstract refs inline
    envVars := substitutions.WalkMappingNode(
        getSpecField(resolved.Resource, "environmentVariables"),
        pluginutils.RewriteResourcePropertyRefs(rewriter),
    )

    lambdaSpec := buildLambdaSpec(resolved, envVars)
    // ...
}
```

Each resource package owns its own property mapping (handler knows `memory→memorySize`, queue knows `name→queueName`). Rewriters are composable for fields that reference resources handled by different emit functions:

```go
// Handler env vars may reference a queue: ${resources.ordersQueue.spec.arn}
combined := pluginutils.ChainResourcePropertyRewriters(
    handler.HandlerPropertyRewriter("ordersHandler", "ordersHandler_lambda"),
    queue.QueuePropertyRewriter("ordersQueue", "ordersQueue_sqs"),
)
```

Fields the emitter constructs fresh (e.g., IAM policy ARNs) use concrete names directly and don't need rewriting.

**Blueprint-level rewriting.** Substitution refs to abstract resources appear in many places outside resource specs — per the blueprint spec, in `exports`, `values`, `include`, `datasources`, and top-level `metadata`. They all need the same rewriting treatment, but no single resource package owns them, so it happens at the emit driver instead of inline. The driver collects each resolved primary's per-package rewriter, chains them with `ChainResourcePropertyRewriters`, and calls `pluginutils.RewriteBlueprintRefs(blueprint, visitor)` (3e) once. The same chain is also passed to per-resource emits for cross-resource refs in spec fields, so a `${resources.ordersHandler.spec.arn}` in an export, in a free-form metadata field, and nested inside a Lambda env var all rewrite to identical concrete refs. Only `variables` and `version` are pure passthrough. The `transform` list is also passed through, but with the current transformer's identifier removed so re-running the framework pipeline doesn't apply this transformer to its own output.

#### 3d. Per-resource Rewriter Shape

Each resource package owns a constructor that closes over the abstract→concrete name pair and returns a `ResourcePropertyRewriter`. The body is a `switch` over `PathExact` / `PathMatches` cases, one per abstract property the resource exposes.

```go
// resources/handler/handler_rewriter.go

// HandlerPropertyRewriter returns a rewriter for substitution references that
// target an abstract handler. It maps abstract spec properties onto the
// concrete Lambda's spec (renaming or restructuring as needed), and routes
// properties with no direct concrete equivalent to derived blueprint values.
func HandlerPropertyRewriter(
    abstractName, lambdaName string,
) pluginutils.ResourcePropertyRewriter {
    return func(ref *substitutions.SubstitutionResourceProperty) *substitutions.Substitution {
        if ref.ResourceName != abstractName {
            return nil  // not our resource — chain falls through to next rewriter
        }

        switch {
        // .spec.memory -> _lambda.spec.memorySize        (1:1 field rename)
        case pluginutils.PathExact(ref, "spec", "memory"):
            return pluginutils.RewriteFields(ref, lambdaName, "spec", "memorySize")

        // .spec.timeout -> _lambda.spec.timeout          (resource rename only)
        case pluginutils.PathExact(ref, "spec", "timeout"):
            return pluginutils.RetargetRef(ref, lambdaName)

        // .spec.vpc.securityGroups[i] -> _lambda.spec.vpcConfig.securityGroupIds[i]
        // 1:1 field rename; the [i] is auto-preserved at its original position.
        case pluginutils.PathMatches(ref, "spec", "vpc", "securityGroups"):
            return pluginutils.RewriteFields(ref, lambdaName, "spec", "vpcConfig", "securityGroupIds")

        // .spec.environmentVariables[i].value -> _lambda.spec.environment.variables[i].value
        // Field count differs (1 abstract field becomes 2 concrete fields), so
        // RewriteFields can't express it — drop to MakeRef and assemble the
        // path explicitly: new prefix + tail copied from source via slice.
        case pluginutils.PathMatches(ref, "spec", "environmentVariables"):
            F := pluginutils.Field
            return pluginutils.MakeRef(lambdaName, append(
                []substitutions.SubstitutionPathItem{F("spec"), F("environment"), F("variables")},
                ref.Path[2:]...,  // [i].value carried over from source
            ))

        // .spec.layer -> _lambda.spec.layers[0]          (scalar -> first slot of array)
        // Introduces a literal index that doesn't exist in the source path.
        case pluginutils.PathExact(ref, "spec", "layer"):
            F := pluginutils.Field
            return pluginutils.MakeRef(lambdaName, []substitutions.SubstitutionPathItem{
                F("spec"), F("layers"), pluginutils.Index(0),
            })

        // .spec.arn -> ${values.<name>_lambda_arn}       (resource ref -> flat value ref)
        case pluginutils.PathExact(ref, "spec", "arn"):
            return pluginutils.ValueRef(lambdaName + "_arn")

        // .spec.functionUrl -> ${values.<name>_lambda_url.url}
        // The transformer-generated value is a complex object {url, authType};
        // the rewriter points the abstract scalar at the .url field within it.
        case pluginutils.PathExact(ref, "spec", "functionUrl"):
            return pluginutils.ValueRef(lambdaName + "_url", pluginutils.Field("url"))
        }
        return nil  // unknown property on a known resource — leave untouched
    }
}
```

```go
// resources/queue/queue_rewriter.go

func QueuePropertyRewriter(
    abstractName, sqsName string,
) pluginutils.ResourcePropertyRewriter {
    return func(ref *substitutions.SubstitutionResourceProperty) *substitutions.Substitution {
        if ref.ResourceName != abstractName {
            return nil
        }

        switch {
        case pluginutils.PathExact(ref, "spec", "name"):
            return pluginutils.RewriteFields(ref, sqsName, "spec", "queueName")
        case pluginutils.PathExact(ref, "spec", "arn"):
            return pluginutils.ValueRef(sqsName + "_arn")
        case pluginutils.PathExact(ref, "spec", "url"):
            return pluginutils.ValueRef(sqsName + "_url")
        }
        return nil
    }
}
```

Per-resource rewriters stay declarative because the structural manipulation lives in the framework — `pluginutils.RewriteFields` covers the common case (1:1 field renames with N-dimensional array indices auto-preserved), and `pluginutils.RetargetRef` / `ValueRef` / `MakeRef` plus the `Field` / `Index` path-item builders cover the rest.

For example, an API-gateway rewriter mapping `.spec.routes[i].method` → `_apigw.spec.paths[i].httpMethod` is just a field rename — `RewriteFields` handles the index transparently:

```go
case pluginutils.PathMatches(ref, "spec", "routes", "method"):
    return pluginutils.RewriteFields(ref, apigwName, "spec", "paths", "httpMethod")
```

A two-dimensional case like `.spec.rules[i].targets[j].arn` → `.spec.rules[i].destinations[j].arn` follows the same pattern:

```go
case pluginutils.PathMatches(ref, "spec", "rules", "targets", "arn"):
    return pluginutils.RewriteFields(ref, sameName, "spec", "rules", "destinations", "arn")
```

Both `[i]` and `[j]` keep their original relative positions automatically — the rewriter never spells them out.

Two design notes:

- The rewriter returns `nil` for *unknown* properties on a *known* resource rather than erroring. Diagnostics for "user referenced `handler.spec.bogus`" come from a separate validation step — keeping the rewriter pure makes `ChainResourcePropertyRewriters` predictable (first non-nil wins; nil means "not mine").
- `RewriteFields` is the right tool for any rewrite that's a 1:1 field rename, regardless of how many array indices are interleaved through the path — they're auto-preserved at their original relative positions. Drop down to `MakeRef` only when field count changes, nesting depth changes, or a literal array index needs to be introduced. There's no second helper for those cases on purpose: each one is a different shape, and a unified "do everything" function would just hide the assembly behind harder-to-read knobs. Explicit path construction with `Field` / `Index` / `ref.Path[i:]` keeps the rare cases obvious at the call site.

#### 3e. Blueprint-Level Rewriter

**Location**: `libs/plugin-framework/sdk/pluginutils/rewrite_blueprint.go`

Substitution references to abstract resources are valid in many top-level blueprint sections, not just resource specs. Per the blueprint specification, refs may appear in `exports`, `values`, `include`, `datasources`, and top-level `metadata`. Each section has its own structural shape (exports have `value` and `description`; includes have `path`, `variables`, `metadata`, `description`; data sources have filter expressions and per-source metadata; etc.), so a per-section walker exists for each — but the emit driver doesn't want to call five helpers and remember the list. The framework provides one call that knows the section list.

```go
// RewriteBlueprintRefs returns a shallow copy of blueprint with every
// substitution-bearing top-level section walked by visitor. Sections walked:
//
//   - Exports[*]      — every value (direct field reference, not in ${..}) and description StringOrSubstitutions
//   - Values[*]       — every value and description StringOrSubstitutions
//   - Include[*]      — path, variables, metadata, description
//   - DataSources[*]  — filter search values, metadata, description
//   - Metadata        — top-level free-form mapping
//
// Sections passed through unchanged (no substitution-bearing fields per spec):
//   - Variables, Version
//
// Resources and Transform are also passed through here; the transformer
// owns those separately:
//   - Resources: spec-level rewrites happen inline during per-resource emit,
//     where each emitter has access to resource-specific structural
//     transformations (memory -> memorySize, etc.). The driver replaces
//     this section wholesale with emitted output.
//   - Transform: the transformer strips its own identifier from this list.
//
// Returns a shallow copy — the input blueprint is not mutated, but
// pointer-shared sub-trees that weren't rewritten remain shared.
func RewriteBlueprintRefs(
    blueprint *schema.Blueprint,
    visitor substitutions.SubstitutionVisitor,
) *schema.Blueprint
```

The exact set of sub-fields walked per section is locked to the blueprint schema — this helper is the single place to update if a future schema revision adds a new ref-bearing field anywhere. The transformer never has to track that list itself.

### Not Needed for v0

- **Blueprint-level `metadata.sharedHandlerConfig`**: Already available in the blueprint passed to Transform — the transformer reads it directly from `blueprint.Metadata`.
- **Primary/auxiliary resource role annotations**: Would require sweeping changes across SDK + deploy CLIs. Existing `pluginutils.TransformerBaseAnnotations()` (`source.abstractName`, `source.abstractType`, `resourceCategory`) is sufficient.
- **Resolve→emit in framework**: The resolve→emit pattern is specific to link-aware resource composition. The framework provides primitives (link graph, substitution walking, diagnostics) but does not dictate transformer internal architecture.

---

## Part 2: Transformer Architecture — Resolve → Aggregate → Emit

### Overview

Absorption is target-specific, not structural. Under `aws-serverless`, each handler emits its own Lambda and the API emits its own API Gateway; under `aws` (v1, containerized), every handler **and** every API fold into a single ECS task definition + service + cluster (+ ALB). The same source resource is a primary emitter in one target and a contributor-fragment in another.

To keep that decision out of the structural traversal, the pipeline is split into three phases. Resolve is target-agnostic; aggregate is target-specific and decides which resolved structs become emit primaries; emit walks the resulting plan.

```text
transformBlueprint(ctx, input)
  |
  +-- Phase 1: Resolve   (target-agnostic)
  |     input.LinkGraph (from framework)
  |     Walk resources once
  |     Each resolver reads EdgesFrom (outbound) + specific EdgesTo (inbound contributory)
  |     Output: []ResolvedResource — every resource gets a resolved struct.
  |             Inbound contributory data (consumers, schedules, vpc, handlerConfig)
  |             is attached to the resolved struct that absorbs it (e.g. ResolvedHandler.Consumers),
  |             but no Emits() flag is set yet — absorption decisions belong to Phase 2.
  |
  +-- Phase 2: Aggregate (target-specific)
  |     Input: []ResolvedResource, deployTarget
  |     Output: EmitPlan — ordered list of primaries to emit
  |     - aws-serverless: filter pass — drops handlerConfig/consumer/schedule resolved structs
  |       (their data lives inside ResolvedHandler.Consumers/Schedules/HandlerConfig);
  |       VPC stays a primary (it emits the concrete VPC that handlers, caches and
  |       databases reference for subnet placement), and its data is also attached to
  |       ResolvedHandler.VPC; handler/api/queue/topic/etc. become primaries
  |     - aws (v1): fold pass — collects every ResolvedHandler + ResolvedAPI into a single
  |       ResolvedService primary; queues/topics/datastores/etc. remain primaries unchanged
  |
  +-- Phase 3: Emit      (target-specific)
        Walk EmitPlan.Primaries
        Build chained rewriter from primaries with target-specific rewriter constructors
          (handler under aws-serverless -> Lambda refs; ResolvedService under aws v1 -> service refs)
        Dispatch on (resolved type, target) to the matching emit function
        Each emit returns: concrete resources + transformer-derived value definitions
                           + diagnostics
        Driver applies the chained rewriter to every top-level blueprint section that
        can carry abstract resource references — `exports`, `values`, `include`,
        `datasources`, and top-level `metadata` (per the blueprint specification, all
        of these accept substitutions). One framework call (`RewriteBlueprintRefs`)
        owns the section list so the driver doesn't enumerate it.
        Only `variables` and `version` pass through unchanged.
        `transform` has the current transformer's identifier stripped (the rest of the
        list passes through) so a downstream re-run of the framework's transform pipeline
        won't apply this transformer to its own output.
        Output: full transformed Blueprint (resources + merged values + rewritten
                exports/include/datasources/metadata + passthrough variables/version + pruned transform) + Diagnostics
```

### Edge Ownership

Each link type is processed by exactly one resource. No edge is processed twice for the same purpose.

A resource processes its **outbound edges** (`EdgesFrom`) to gather references to things it depends on, and its **inbound edges** (`EdgesTo`) only for specific "contributory" link types where the source is structurally subordinate (e.g., handler attaches consumer, schedule, vpc, handlerConfig inbound edges to its resolved struct). "Contributory" here is a Phase-1 fact about how the link graph is shaped — independent of whether Phase 2 ultimately treats the contributing resource as a stand-alone emit unit. Under `aws-serverless` the contributors get folded into the handler's Lambda; under `aws` v1 they get folded into the ECS service; the Phase-1 attachment is identical either way.

Both sides of a link CAN see the edge, but they extract different information. For `handler→queue`: handler extracts IAM/env var data from the edge, queue's resolver ignores that inbound edge — it has no use for it. This is intentional non-overlapping responsibility, not duplication.

| Link | Processed by | Direction | Purpose |
|------|-------------|-----------|---------|
| `handler->queue` | handler | outbound | IAM + env var for queue |
| `handler->topic` | handler | outbound | IAM + env var for topic |
| `handler->datastore` | handler | outbound | IAM + env var for table |
| `handler->sqlDatabase` | handler | outbound | IAM + connection config |
| `handler->bucket` | handler | outbound | IAM + env var for bucket |
| `handler->cache` | handler | outbound | IAM + connection config |
| `handler->config` | handler | outbound | config store reference |
| `consumer->handler` | handler | inbound | event source mapping |
| `schedule->handler` | handler | inbound | trigger config |
| `vpc->handler` | handler | inbound | VPC networking config |
| `api->handler` | api | outbound | route -> handler mapping |
| `api->config` | api | outbound | config store reference |
| `consumer->config` | handler (via consumer) | absorbed | config for consumer's handler |
| `schedule->config` | handler (via schedule) | absorbed | config for schedule's handler |
| `queue->queue` | queue | outbound | dead-letter queue config |
| `queue->consumer` | queue | outbound | message routing |
| `queue->topic` | queue | outbound | topic forwarding |
| `bucket->queue` | bucket | outbound | notification config |
| `bucket->topic` | bucket | outbound | notification config |
| `bucket->consumer` | bucket | outbound | event routing |
| `datastore->consumer` | datastore | outbound | stream routing |
| `vpc->cache` | cache | inbound | VPC networking config |
| `vpc->sqlDatabase` | sqlDatabase | inbound | VPC networking config |

### File Structure

The convention: `*_resolve.go` files are target-agnostic. Emit and rewriter files are target-suffixed (`_aws_serverless.go`, `_aws.go`). Resources that emit identically across targets (queue/topic/datastore in aws-serverless and aws v1) keep a single shared `_aws.go`. The `service/` package is aws-v1-only and exists solely to hold `ResolvedService` and its emit/rewriter — it has no `*_resolve.go` because it's constructed by `aggregate_aws.go`, not from raw blueprint resources.

```text
transformer/
  transformer.go               -- NewTransformer() (unchanged); transformBlueprint() calls resolve -> aggregate -> emit
  resolve.go                   -- resolveBlueprint(blueprint, linkGraph) -> []ResolvedResource (target-agnostic)
  resolved_types.go            -- ResolvedResource interface (no Emits() method), per-resource resolved structs, LinkedResource
  aggregate.go                 -- aggregate([]ResolvedResource, deployTarget) -> EmitPlan; dispatches per target
  aggregate_aws_serverless.go  -- filter pass: drops handlerConfig/consumer/schedule from primaries (VPC stays a primary)
  aggregate_aws.go             -- fold pass: handlers + APIs -> single ResolvedService primary
  emit.go                      -- emitResources(blueprint, EmitPlan, deployTarget, ctx) -> output

resources/
  handler/
    handler_resource.go              -- Resource() definition (unchanged)
    handler_resource_schema.go       -- schema stub (unchanged)
    handler_resolve.go               -- target-agnostic: EdgesFrom (outbound deps) + EdgesTo (consumer, schedule, vpc, handlerConfig)
    handler_aws_serverless.go        -- EmitAWSServerless(ResolvedHandler) -> Lambda + event source mappings + IAM
    handler_aws_serverless_rewriter.go -- AWSServerlessRewriter(abstractName, lambdaName)
  api/
    api_resolve.go                   -- target-agnostic: EdgesFrom (handlers, config)
    api_aws_serverless.go            -- EmitAWSServerless(ResolvedAPI) -> API Gateway + routes
    api_aws_serverless_rewriter.go   -- AWSServerlessRewriter(abstractName, apigwName)
  service/                           -- aws-v1-only; constructed by aggregate_aws.go
    service_resolved.go              -- ResolvedService struct (handlers + APIs aggregated)
    service_aws.go                   -- EmitAWS(ResolvedService) -> ECS task def + service + cluster + ALB + IAM
    service_handler_rewriter.go      -- HandlerRewriter(abstractHandlerName, "celerity_service")
    service_api_rewriter.go          -- APIRewriter(abstractAPIName, "celerity_alb")
  bucket/
    bucket_resolve.go                -- EdgesFrom (notification targets)
    bucket_aws.go                    -- EmitAWS (shared serverless + v1)
    bucket_aws_rewriter.go
  queue/
    queue_resolve.go                 -- EdgesFrom (DLQ, consumer, topic)
    queue_aws.go                     -- EmitAWS (shared)
    queue_aws_rewriter.go
  topic/
    topic_resolve.go                 -- leaf
    topic_aws.go                     -- EmitAWS (shared)
    topic_aws_rewriter.go
  datastore/
    datastore_resolve.go             -- EdgesFrom (consumer)
    datastore_aws.go                 -- EmitAWS (shared)
    datastore_aws_rewriter.go
  sqldatabase/
    sqldatabase_resolve.go           -- EdgesTo (vpc)
    sqldatabase_aws.go               -- EmitAWS (shared)
    sqldatabase_aws_rewriter.go
  cache/
    cache_resolve.go                 -- EdgesTo (vpc)
    cache_aws.go                     -- EmitAWS (shared)
    cache_aws_rewriter.go
  consumer/
    consumer_resolve.go              -- target-agnostic; aws-serverless drops it during aggregate, aws v1 references it through ResolvedHandler.Consumers
  schedule/
    schedule_resolve.go              -- target-agnostic; same shape as consumer
  vpc/
    vpc_resolve.go                   -- target-agnostic; both targets drop it during aggregate
  config/
    config_resolve.go                -- EdgesTo (to know who references it, for store setup)
    config_aws.go                    -- EmitAWS (shared) -> SSM Parameter / Secrets Manager
    config_aws_rewriter.go
  handlerconfig/
    handlerconfig_resolve.go         -- target-agnostic; both targets drop it during aggregate
```

### Core Types

```go
// transformer/resolved_types.go

// ResolvedResource is the target-agnostic output of Phase 1. There is no
// Emits() method — whether a resolved resource becomes a top-level emit
// unit is decided in Phase 2 (Aggregate) per deploy target.
type ResolvedResource interface {
    ResourceName() string
    ResourceType() string
}

type ResolvedHandler struct {
    Name     string
    Resource *schema.Resource
    // Outbound dependencies (from EdgesFrom)
    Queues      []*LinkedResource
    Topics      []*LinkedResource
    Datastores  []*LinkedResource
    Databases   []*LinkedResource
    Buckets     []*LinkedResource
    Caches      []*LinkedResource
    Configs     []*LinkedResource
    // Inbound contributory (from EdgesTo) — Phase 2 decides what becomes
    // of these per target (e.g. event source mappings on Lambda under
    // aws-serverless, polling code generation on ECS under aws v1).
    Consumers     []*LinkedResource  // event source triggers
    Schedules     []*LinkedResource  // scheduled triggers
    VPC           *LinkedResource    // network config (nil if none)
    HandlerConfig *LinkedResource    // inherited defaults (nil if none)
}

// LinkedResource pairs a resolved link edge with the target resource spec.
type LinkedResource struct {
    Name     string
    Resource *schema.Resource
    Edge     *linktypes.ResolvedLink
}
```

```go
// transformer/aggregate.go

// EmitPlan is the output of Phase 2. It is the ordered list of resolved
// resources that will produce concrete output in Phase 3 — per-target
// aggregators decide membership. Resolved structs not in Primaries are
// neither emitted directly nor referenceable by name in the output blueprint;
// their data has been folded into a primary already (e.g. ResolvedHandler.Consumers
// under aws-serverless, ResolvedService.Handlers under aws v1).
type EmitPlan struct {
    Primaries []ResolvedResource
}
```

```go
// resources/service/service_resolved.go (aws-v1-only)

// ResolvedService is the synthesized primary that aggregate_aws.go produces
// from every ResolvedHandler + ResolvedAPI in the blueprint. It does not
// exist under aws-serverless; aws-v1 emit dispatches solely on this type
// for handler/API output.
type ResolvedService struct {
    Handlers []*handler.ResolvedHandler
    APIs     []*api.ResolvedAPI
    // Aggregate carries no fields that aren't already reachable via
    // Handlers[i] / APIs[i] — the per-handler Consumers/Schedules/VPC
    // remain on the original ResolvedHandler structs and EmitAWS walks
    // them when building the task definition.
}

func (s *ResolvedService) ResourceName() string { return "celerity_service" }
func (s *ResolvedService) ResourceType() string { return "aws/ecs/service" }
```

### Key Change to transformBlueprint

```go
func transformBlueprint(
    ctx context.Context,
    input *transform.SpecTransformerTransformInput,
) (*transform.SpecTransformerTransformOutput, error) {
    resolved, err := resolveBlueprint(input.InputBlueprint, input.LinkGraph)
    if err != nil {
        return nil, err
    }

    deployTarget := getDeployTarget(input.TransformerContext)
    plan, err := aggregate(resolved, deployTarget)
    if err != nil {
        return nil, err
    }

    return emitResources(input.InputBlueprint, plan, deployTarget, input.TransformerContext)
}
```

### Resolve Phase

Resolve is target-agnostic. Every resource in the blueprint becomes a `ResolvedResource`; inbound contributory edges are attached to the absorbing struct (e.g., a consumer's resolved data hangs off `ResolvedHandler.Consumers`), but no resource is dropped here — that's Phase 2's job.

```go
// transformer/resolve.go

func resolveBlueprint(
    blueprint *schema.Blueprint,
    linkGraph linktypes.DeclaredLinkGraph,
) ([]ResolvedResource, error) {
    resources := getResources(blueprint)
    var resolved []ResolvedResource

    for name, resource := range resources {
        r, err := resolveResource(name, resource, linkGraph, blueprint)
        if err != nil {
            return nil, err
        }
        resolved = append(resolved, r)
    }
    return resolved, nil
}

func resolveResource(...) (ResolvedResource, error) {
    switch getResourceType(resource) {
    case "celerity/handler":
        return handler.Resolve(name, resource, linkGraph, blueprint)
    case "celerity/handlerConfig":
        return handlerconfig.Resolve(name, resource)
    // ... etc — every type returns a resolved struct, no exceptions
    }
}
```

### Aggregate Phase

Aggregate is the only phase that knows about target-specific structural decisions. It rearranges the flat `[]ResolvedResource` into an ordered `EmitPlan.Primaries`. The two implementations look very different:

```go
// transformer/aggregate.go

func aggregate(
    resolved []ResolvedResource,
    deployTarget string,
) (EmitPlan, error) {
    switch deployTarget {
    case "aws-serverless":
        return aggregateAWSServerless(resolved), nil
    case "aws":
        return aggregateAWS(resolved), nil
    default:
        return EmitPlan{}, fmt.Errorf("unsupported deploy target: %s", deployTarget)
    }
}
```

```go
// transformer/aggregate_aws_serverless.go — filter pass

func aggregateAWSServerless(resolved []ResolvedResource) EmitPlan {
    primaries := make([]ResolvedResource, 0, len(resolved))
    for _, r := range resolved {
        switch r.(type) {
        // Contributory-only types: their data is already attached to a
        // ResolvedHandler via Phase 1; drop them from the primary list.
        case *handlerconfig.ResolvedHandlerConfig,
             *consumer.ResolvedConsumer,
             *schedule.ResolvedSchedule,
             *vpc.ResolvedVPC:
            continue
        default:
            primaries = append(primaries, r)
        }
    }
    return EmitPlan{Primaries: primaries}
}
```

```go
// transformer/aggregate_aws.go — fold pass

func aggregateAWS(resolved []ResolvedResource) EmitPlan {
    var handlers []*handler.ResolvedHandler
    var apis []*api.ResolvedAPI
    var others []ResolvedResource

    for _, r := range resolved {
        switch v := r.(type) {
        case *handler.ResolvedHandler:
            handlers = append(handlers, v)
        case *api.ResolvedAPI:
            apis = append(apis, v)
        case *handlerconfig.ResolvedHandlerConfig,
             *consumer.ResolvedConsumer,
             *schedule.ResolvedSchedule,
             *vpc.ResolvedVPC:
            // Same drop as aws-serverless — contributory-only.
            continue
        default:
            others = append(others, r)
        }
    }

    primaries := others
    if len(handlers) > 0 || len(apis) > 0 {
        primaries = append(primaries, &service.ResolvedService{
            Handlers: handlers,
            APIs:     apis,
        })
    }
    return EmitPlan{Primaries: primaries}
}
```

### Emit Phase

The emit driver does five things: (1) builds a chained rewriter from the plan's primaries with **target-specific** rewriter constructors so spec rewrites and blueprint-level rewrites share one mapping; (2) walks every ref-bearing top-level section once via `pluginutils.RewriteBlueprintRefs`; (3) invokes the per-(target, type) emit function for each primary; (4) merges transformer-derived value definitions into the rewritten input values; (5) assembles the output blueprint with the rewritten sections, the emitted resources replacing the input's resources, the merged values, and the current transformer's identifier stripped from `transform`.

```go
// transformer/emit.go

// EmitResult is what each per-resource emit returns. Concrete resources
// and transformer-derived values both feed into the final output blueprint;
// diagnostics bubble up to the response.
type EmitResult struct {
    Resources     map[string]*schema.Resource
    DerivedValues map[string]*schema.Value     // e.g. ordersHandler_lambda_arn (serverless), celerity_service_arn (v1)
    Diagnostics   []*core.Diagnostic
}

func emitResources(
    blueprint *schema.Blueprint,
    plan EmitPlan,
    deployTarget string,
    ctx transform.Context,
) (*transform.SpecTransformerTransformOutput, error) {
    // Build the chain once, up front. Per-resource emits use it for
    // cross-resource refs inside spec fields; the driver uses it again
    // for the outputs/values rewrite. Single source of truth.
    //
    // Construction is target-specific: under aws-serverless a handler
    // primary contributes a Lambda-targeted rewriter; under aws v1 a
    // ResolvedService primary contributes one rewriter per nested handler
    // and one per nested API, all targeting the shared service / ALB.
    rewriters, err := buildRewriters(plan, deployTarget)
    if err != nil {
        return nil, err
    }
    chained := pluginutils.ChainResourcePropertyRewriters(rewriters...)

    emittedResources := &schema.ResourceMap{Values: map[string]*schema.Resource{}}
    derivedValues := map[string]*schema.Value{}
    var diagnostics []*core.Diagnostic

    for _, p := range plan.Primaries {
        result, err := emitResource(p, deployTarget, chained, ctx)
        if err != nil {
            return nil, err
        }
        for k, v := range result.Resources {
            emittedResources.Values[k] = v
        }
        for k, v := range result.DerivedValues {
            derivedValues[k] = v
        }
        diagnostics = append(diagnostics, result.Diagnostics...)
    }

    // Walk every ref-bearing top-level section in one call. The framework
    // helper owns the section list (exports, values, include, datasources,
    // top-level metadata) so this code never has to enumerate it.
    rewriteVisitor := pluginutils.RewriteResourcePropertyRefs(chained)
    rewritten := pluginutils.RewriteBlueprintRefs(blueprint, rewriteVisitor)

    // Strip our own identifier from `transform` so re-running the framework
    // pipeline on this output doesn't apply this transformer a second time.
    prunedTransform := stripTransformerID(rewritten.Transform, ctx.TransformerID())

    return &transform.SpecTransformerTransformOutput{
        TransformedBlueprint: &schema.Blueprint{
            Version:     rewritten.Version,                                // passthrough
            Transform:   prunedTransform,                                  // current transformer ID removed
            Variables:   rewritten.Variables,                              // passthrough
            Values:      mergeValues(rewritten.Values, derivedValues),     // rewritten + transformer-added
            Include:     rewritten.Include,                                // refs rewritten
            Resources:   emittedResources,                                 // concrete only (replaces input.Resources entirely)
            DataSources: rewritten.DataSources,                            // refs rewritten
            Exports:     rewritten.Exports,                                // refs rewritten
            Metadata:    rewritten.Metadata,                               // refs rewritten
        },
        Diagnostics: diagnostics,
    }, nil
}
```

#### Target-Specific Rewriter Construction

Each target has its own `buildRewriters` so the same abstract reference produces the right concrete reference per target. `${resources.ordersHandler.spec.arn}` rewrites to `${values.ordersHandler_lambda_arn}` under `aws-serverless` and to `${values.celerity_service_arn}` under `aws` v1 — both in resource specs and in exports/values, automatically, because the chain construction is the only thing that varies.

```go
// transformer/emit.go

func buildRewriters(
    plan EmitPlan,
    deployTarget string,
) ([]pluginutils.ResourcePropertyRewriter, error) {
    switch deployTarget {
    case "aws-serverless":
        return buildRewritersAWSServerless(plan), nil
    case "aws":
        return buildRewritersAWS(plan), nil
    default:
        return nil, fmt.Errorf("unsupported deploy target: %s", deployTarget)
    }
}

func buildRewritersAWSServerless(plan EmitPlan) []pluginutils.ResourcePropertyRewriter {
    rs := make([]pluginutils.ResourcePropertyRewriter, 0, len(plan.Primaries))
    for _, p := range plan.Primaries {
        switch v := p.(type) {
        case *handler.ResolvedHandler:
            rs = append(rs, handler.AWSServerlessRewriter(v.Name, v.Name+"_lambda"))
        case *api.ResolvedAPI:
            rs = append(rs, api.AWSServerlessRewriter(v.Name, v.Name+"_apigw"))
        case *queue.ResolvedQueue:
            rs = append(rs, queue.AWSRewriter(v.Name, v.Name+"_sqs"))
        // ... shared cases (topic, datastore, sqlDatabase, cache, bucket, config)
        }
    }
    return rs
}

func buildRewritersAWS(plan EmitPlan) []pluginutils.ResourcePropertyRewriter {
    rs := make([]pluginutils.ResourcePropertyRewriter, 0)
    for _, p := range plan.Primaries {
        switch v := p.(type) {
        case *service.ResolvedService:
            // Every handler folded into the service rewrites to the shared
            // service's refs; every API rewrites to the ALB.
            for _, h := range v.Handlers {
                rs = append(rs, service.HandlerRewriter(h.Name, "celerity_service"))
            }
            for _, a := range v.APIs {
                rs = append(rs, service.APIRewriter(a.Name, "celerity_alb"))
            }
        case *queue.ResolvedQueue:
            rs = append(rs, queue.AWSRewriter(v.Name, v.Name+"_sqs"))
        // ... shared cases
        }
    }
    return rs
}
```

Per-target divergence on what a property even *means* (e.g. `.spec.functionUrl` is serverless-only) is handled inside the rewriter: the aws-v1 handler rewriter returns `nil` for unsupported abstract properties, and a separate validation pass flags unresolved refs in the rewritten tree as diagnostics.

#### Per-Resource Emit Dispatch

`emitResource` is a two-level dispatch — outer on `deployTarget`, inner on resolved type. Resources that emit identically across targets keep one shared `EmitAWS`; resources that diverge get target-suffixed emitters.

```go
// transformer/emit.go

func emitResource(
    r ResolvedResource,
    deployTarget string,
    chained pluginutils.ResourcePropertyRewriter,
    ctx transform.Context,
) (*EmitResult, error) {
    switch deployTarget {
    case "aws-serverless":
        return emitResourceAWSServerless(r, chained, ctx)
    case "aws":
        return emitResourceAWS(r, chained, ctx)
    default:
        return nil, fmt.Errorf("unsupported deploy target: %s", deployTarget)
    }
}

func emitResourceAWSServerless(
    r ResolvedResource,
    chained pluginutils.ResourcePropertyRewriter,
    ctx transform.Context,
) (*EmitResult, error) {
    switch v := r.(type) {
    case *handler.ResolvedHandler:        return handler.EmitAWSServerless(v, chained, ctx)
    case *api.ResolvedAPI:                return api.EmitAWSServerless(v, chained, ctx)
    case *queue.ResolvedQueue:            return queue.EmitAWS(v, chained, ctx)            // shared
    case *topic.ResolvedTopic:            return topic.EmitAWS(v, chained, ctx)            // shared
    case *bucket.ResolvedBucket:          return bucket.EmitAWS(v, chained, ctx)           // shared
    case *datastore.ResolvedDatastore:    return datastore.EmitAWS(v, chained, ctx)        // shared
    case *sqldatabase.ResolvedSqlDatabase: return sqldatabase.EmitAWS(v, chained, ctx)     // shared
    case *cache.ResolvedCache:            return cache.EmitAWS(v, chained, ctx)            // shared
    case *config.ResolvedConfig:          return config.EmitAWS(v, chained, ctx)           // shared
    default:
        return nil, fmt.Errorf("no aws-serverless emitter for resolved type %T", r)
    }
}

func emitResourceAWS(
    r ResolvedResource,
    chained pluginutils.ResourcePropertyRewriter,
    ctx transform.Context,
) (*EmitResult, error) {
    switch v := r.(type) {
    case *service.ResolvedService:        return service.EmitAWS(v, chained, ctx)          // ECS task def + service + cluster + ALB + IAM
    case *queue.ResolvedQueue:            return queue.EmitAWS(v, chained, ctx)            // shared
    case *topic.ResolvedTopic:            return topic.EmitAWS(v, chained, ctx)            // shared
    case *bucket.ResolvedBucket:          return bucket.EmitAWS(v, chained, ctx)           // shared
    case *datastore.ResolvedDatastore:    return datastore.EmitAWS(v, chained, ctx)        // shared
    case *sqldatabase.ResolvedSqlDatabase: return sqldatabase.EmitAWS(v, chained, ctx)     // shared
    case *cache.ResolvedCache:            return cache.EmitAWS(v, chained, ctx)            // shared
    case *config.ResolvedConfig:          return config.EmitAWS(v, chained, ctx)           // shared
    default:
        return nil, fmt.Errorf("no aws emitter for resolved type %T", r)
    }
}
```

Notes:
- The aws-v1 inner switch never sees `*handler.ResolvedHandler` or `*api.ResolvedAPI` — Phase 2 already folded them into `*service.ResolvedService`. If one slips through, the `default` errors immediately rather than silently dropping output.
- `mergeValues` errors on key collision — a user-defined value that shadows a transformer-derived name is a bug, not a silent override.
- `pluginutils.RewriteBlueprintRefs` (3e) is the single touchpoint for blueprint-level rewriting. If the blueprint schema gains a new ref-bearing field on any section, the helper is updated in one place and every transformer picks it up.
- `stripTransformerID(list, id)` returns a copy of the transform list with `id` removed (no error if absent — the framework guarantees this transformer was invoked because its ID is in the list, but being defensive is cheap). If the resulting list is empty, the field is set to nil so the output blueprint omits it cleanly.
- `ctx.TransformerID()` returns the identifier this transformer registered under (e.g. `"celerity-2026-04-29"`). The framework provides it so the transformer doesn't have to hard-code its own name in two places.

---

## Worked Example: Implementing `celerity/queue` End-to-End

A small but complete walkthrough of a single abstract resource. Queue is a good vehicle: it has 1→many fanout (an SQS queue, plus an SQS queue policy when external producers exist), it gathers outbound edges (DLQ, consumers, topic forwards), and its emit is **shared** between `aws-serverless` and `aws` v1 (SQS is the same in both targets), so the example focuses on the framework pattern rather than target-split mechanics. Handler/API target-split implementations follow the same shape — they just have one `_aws_serverless.go` instead of one `_aws.go`, and a corresponding `_aws_serverless_rewriter.go`.

The four files added under `resources/queue/` (the existing `queue_resource.go` + `queue_resource_schema.go` defining the abstract resource type are unchanged):

1. `queue_resolved.go` — the resolved struct (Phase 1 output shape)
2. `queue_resolve.go` — the Phase 1 `Resolve()` function
3. `queue_aws.go` — the Phase 3 `EmitAWS()` function (shared)
4. `queue_aws_rewriter.go` — the substitution ref rewriter (used by Phase 3 chain construction)

### File 1: `resources/queue/queue_resolved.go`

```go
package queue

import (
    "github.com/two-hundred/celerity/libs/blueprint/linktypes"
    "github.com/two-hundred/celerity/libs/blueprint/schema"
    "<this-repo>/transformer"
)

// ResolvedQueue is the target-agnostic Phase 1 output for a celerity/queue.
// All link-derived facts hang off this struct so emit doesn't re-walk the graph.
type ResolvedQueue struct {
    Name     string
    Resource *schema.Resource

    // Outbound (from EdgesFrom)
    DeadLetter *transformer.LinkedResource    // queue->queue, max cardinality 1
    Consumers  []*transformer.LinkedResource  // queue->consumer
    Topics     []*transformer.LinkedResource  // queue->topic (rare)

    // Inbound producers — bucket->queue notifications and similar.
    // Drives whether we need an aws/sqs/queueInlinePolicy to grant the producer
    // service principal SendMessage. Captured at Phase 1 even though the
    // bucket's own emit also sees its outbound edge: this side needs the
    // policy, the bucket side needs the notification config — same edge,
    // different responsibilities.
    Producers []*transformer.LinkedResource
}

func (q *ResolvedQueue) ResourceName() string { return q.Name }
func (q *ResolvedQueue) ResourceType() string { return "celerity/queue" }
```

### File 2: `resources/queue/queue_resolve.go`

```go
package queue

import (
    "github.com/two-hundred/celerity/libs/blueprint/linktypes"
    "github.com/two-hundred/celerity/libs/blueprint/schema"
    "<this-repo>/transformer"
)

func Resolve(
    name string,
    resource *schema.Resource,
    linkGraph linktypes.DeclaredLinkGraph,
    blueprint *schema.Blueprint,
) (transformer.ResolvedResource, error) {
    resolved := &ResolvedQueue{Name: name, Resource: resource}

    // Outbound: every link FROM this queue.
    for _, edge := range linkGraph.EdgesFrom(name) {
        target := blueprint.Resources.Values[edge.Target]
        link := &transformer.LinkedResource{Name: edge.Target, Resource: target, Edge: edge}
        switch edge.TargetType {
        case "celerity/queue":
            // queue->queue is the DLQ. Cardinality (max 1) is enforced by
            // ValidateLinks; trust it here.
            resolved.DeadLetter = link
        case "celerity/consumer":
            resolved.Consumers = append(resolved.Consumers, link)
        case "celerity/topic":
            resolved.Topics = append(resolved.Topics, link)
        }
    }

    // Inbound: only producers that need an SQS resource policy.
    // bucket->queue and topic->queue both fall into this category.
    // handler->queue does NOT — IAM lives on the handler side.
    for _, edge := range linkGraph.EdgesTo(name) {
        switch edge.SourceType {
        case "celerity/bucket", "celerity/topic":
            target := blueprint.Resources.Values[edge.Source]
            resolved.Producers = append(resolved.Producers, &transformer.LinkedResource{
                Name: edge.Source, Resource: target, Edge: edge,
            })
        }
    }

    return resolved, nil
}
```

### File 3: `resources/queue/queue_aws.go`

```go
package queue

import (
    "github.com/two-hundred/celerity/libs/blueprint/core"
    "github.com/two-hundred/celerity/libs/blueprint/schema"
    "github.com/two-hundred/celerity/libs/blueprint/substitutions"
    "github.com/two-hundred/celerity/libs/blueprint/transform"
    "github.com/two-hundred/celerity/libs/plugin-framework/sdk/pluginutils"
    "<this-repo>/transformer"
)

func EmitAWS(
    resolved *ResolvedQueue,
    chained pluginutils.ResourcePropertyRewriter,
    ctx transform.Context,
) (*transformer.EmitResult, error) {
    sqsName := resolved.Name + "_sqs"
    rewriteVisitor := pluginutils.RewriteResourcePropertyRefs(chained)

    out := map[string]*schema.Resource{}

    // 1. The SQS queue itself — always emitted.
    out[sqsName] = &schema.Resource{
        Type:     stringPtr("aws/sqs/queue"),
        Metadata: pluginutils.TransformerBaseAnnotations(resolved.Name, "celerity/queue", "primary"),
        Spec:     buildSQSSpec(resolved, rewriteVisitor),
    }

    // 2. SQS queue policy — only if there are external service-principal producers.
    //    Folded under the "auxiliary" resourceCategory annotation so deploy-time
    //    tooling knows it's part of the same abstract resource.
    if len(resolved.Producers) > 0 {
        policyName := resolved.Name + "_sqs_policy"
        out[policyName] = &schema.Resource{
            Type:     stringPtr("aws/sqs/queueInlinePolicy"),
            Metadata: pluginutils.TransformerBaseAnnotations(resolved.Name, "celerity/queue", "auxiliary"),
            Spec:     buildSQSPolicySpec(resolved, sqsName),
        }
    }

    // 3. Derived values — refs that abstract `${resources.<name>.spec.{arn,url}}`
    //    will rewrite to. The DerivedValues map is merged into the output
    //    blueprint's `values` section by the emit driver.
    derivedValues := map[string]*schema.Value{
        sqsName + "_arn": stringValueFromRef(sqsName, "spec", "arn"),
        sqsName + "_url": stringValueFromRef(sqsName, "spec", "url"),
    }

    return &transformer.EmitResult{
        Resources:     out,
        DerivedValues: derivedValues,
        Diagnostics:   nil,
    }, nil
}

// buildSQSSpec composes the concrete SQS spec from the abstract spec.
// User-written substitution refs in carried-through fields (e.g. encryption.kmsKeyArn
// pointing at a KMS key resource) are rewritten **inline** with the chained
// visitor so the concrete spec only contains rewritten refs — no second pass needed.
func buildSQSSpec(
    resolved *ResolvedQueue,
    rewriteVisitor substitutions.SubstitutionVisitor,
) *core.MappingNode {
    abstractSpec := pluginutils.GetSpec(resolved.Resource)

    spec := &core.MappingNode{Fields: map[string]*core.MappingNode{
        "queueName":              copyField(abstractSpec, "name"),
        "fifoQueue":               copyField(abstractSpec, "fifoOrdering"),
        "messageRetentionPeriod": copyField(abstractSpec, "messageRetentionDays"),
        "visibilityTimeout":      copyField(abstractSpec, "visibilityTimeoutSeconds"),
        // KMS key ref might be `${resources.kmsKey.spec.arn}` — rewrite now,
        // not later, because the field is owned by this resource.
        "kmsMasterKeyId": substitutions.WalkMappingNode(
            getNested(abstractSpec, "encryption", "kmsKeyArn"),
            rewriteVisitor,
        ),
    }}

    // DLQ wiring: the abstract DeadLetter link points at another celerity/queue.
    // Build a substitution ref against the **abstract** name and walk it through
    // the chained rewriter — that rewriter contains the DLQ's own AWSRewriter,
    // which turns `${resources.theDLQ.spec.arn}` into `${values.theDLQ_sqs_arn}`.
    if resolved.DeadLetter != nil {
        spec.Fields["redrivePolicy"] = &core.MappingNode{Fields: map[string]*core.MappingNode{
            "deadLetterTargetArn": substitutions.WalkMappingNode(
                refMapping("resources", resolved.DeadLetter.Name, "spec", "arn"),
                rewriteVisitor,
            ),
            "maxReceiveCount": literalInt(linkParamInt(resolved.DeadLetter, "maxReceiveCount", 5)),
        }}
    }

    return spec
}

// buildSQSPolicySpec emits an SQS resource policy granting each producer
// service principal SendMessage on the queue. The resource ref to the queue
// uses the **concrete** name directly (sqsName) because this spec is freshly
// constructed by the emitter — no user-written ref to rewrite.
func buildSQSPolicySpec(resolved *ResolvedQueue, sqsName string) *core.MappingNode {
    statements := make([]*core.MappingNode, 0, len(resolved.Producers))
    for _, p := range resolved.Producers {
        statements = append(statements, policyStatementForProducer(p, sqsName))
    }
    return &core.MappingNode{Fields: map[string]*core.MappingNode{
        "queues":   listOf(refMapping("resources", sqsName, "spec", "url")),
        "document": policyDocument(statements),
    }}
}
```

### File 4: `resources/queue/queue_aws_rewriter.go`

```go
package queue

import (
    "github.com/two-hundred/celerity/libs/blueprint/substitutions"
    "github.com/two-hundred/celerity/libs/plugin-framework/sdk/pluginutils"
)

// AWSRewriter is registered with the emit driver's chain (see emit.go
// `buildRewritersAWSServerless` / `buildRewritersAWS`). The emit driver
// constructs one per ResolvedQueue primary, parameterised with the
// abstract→concrete name pair this resolved instance carries.
func AWSRewriter(abstractName, sqsName string) pluginutils.ResourcePropertyRewriter {
    return func(ref *substitutions.SubstitutionResourceProperty) *substitutions.Substitution {
        if ref.ResourceName != abstractName {
            return nil  // not our resource — chain falls through
        }

        switch {
        // Concrete SQS keeps the user-supplied name; field is renamed.
        case pluginutils.PathExact(ref, "spec", "name"):
            return pluginutils.RewriteFields(ref, sqsName, "spec", "queueName")

        case pluginutils.PathExact(ref, "spec", "fifoOrdering"):
            return pluginutils.RewriteFields(ref, sqsName, "spec", "fifoQueue")

        case pluginutils.PathExact(ref, "spec", "messageRetentionDays"):
            return pluginutils.RewriteFields(ref, sqsName, "spec", "messageRetentionPeriod")

        case pluginutils.PathExact(ref, "spec", "visibilityTimeoutSeconds"):
            return pluginutils.RewriteFields(ref, sqsName, "spec", "visibilityTimeout")

        // ARN/URL are computed at deploy-time; route abstract refs to
        // transformer-derived blueprint values (defined by EmitAWS).
        case pluginutils.PathExact(ref, "spec", "arn"):
            return pluginutils.ValueRef(sqsName + "_arn")

        case pluginutils.PathExact(ref, "spec", "url"):
            return pluginutils.ValueRef(sqsName + "_url")
        }
        return nil  // unknown property on a known resource — leave for diagnostics pass
    }
}
```

### How the four files cooperate at runtime

1. **Phase 1 (resolve)**: For every `celerity/queue` resource in the blueprint, the dispatcher (`transformer/resolve.go`) calls `queue.Resolve(...)`. Output: one `*ResolvedQueue` per queue, with link data attached.

2. **Phase 2 (aggregate)**:
   - Under `aws-serverless`, `aggregateAWSServerless` keeps every `*ResolvedQueue` as a primary.
   - Under `aws` v1, `aggregateAWS` keeps them too — queues are not folded into the service. They emit independently in both targets.

3. **Phase 3 (emit)**: The driver runs in this order:
   - `buildRewritersAWSServerless(plan)` (or `_AWS`) walks `plan.Primaries` and, for each `*queue.ResolvedQueue`, appends `queue.AWSRewriter(v.Name, v.Name+"_sqs")` to the chain. Other primaries' rewriters (handler, api, …) are appended too.
   - The chained rewriter is applied once, at the driver, via `pluginutils.RewriteBlueprintRefs` — this rewrites refs to this queue inside `exports`, user-defined `values`, `include`, `datasources`, and top-level `metadata`.
   - The driver then calls `queue.EmitAWS(resolved, chained, ctx)`. Inside, `buildSQSSpec` uses the same `chained` to rewrite user-written refs in carried-through fields (e.g. an encryption KMS key ref to another resource) and to resolve the DLQ ref to a `${values.<dlq>_sqs_arn}`. The function returns `EmitResult{Resources: {<name>_sqs, optionally <name>_sqs_policy}, DerivedValues: {<name>_sqs_arn, <name>_sqs_url}}`.
   - The driver merges `DerivedValues` into the rewritten input values and writes the concrete resources to the output blueprint.

### Invariants worth preserving

- **The abstract→concrete property mapping lives in two files: `queue_aws.go` (`buildSQSSpec`) and `queue_aws_rewriter.go` (`AWSRewriter`). They must stay in sync.** The rewriter's switch arms double as the contract definition of what abstract properties are supported. A property added to the spec construction without a corresponding rewriter case will cause user refs to that property to remain abstract in the emitted output.
- **Auxiliary resources (queue policy, IAM role, etc.) carry the same `source.abstractName` annotation as their primary** so deploy-time tooling can group them. `pluginutils.TransformerBaseAnnotations` enforces this shape.
- **Cross-resource refs always go through the chained rewriter, never through ad-hoc string concatenation.** The DLQ wiring above is the canonical example: build a substitution ref against the abstract name, walk it through the chain, get the right concrete ref out — automatically correct under any deploy target because the DLQ's own rewriter is in the chain.

---

## Verification

1. `go build ./...` — all new files compile
2. `go vet ./...` — no structural issues
3. Unit tests in `transformer/resolve_test.go` (target-agnostic):
   - Blueprint with handler + consumer + handlerConfig: handler's resolved struct attaches consumer and handlerConfig via EdgesTo
   - Blueprint with handler + queue: handler sees queue via EdgesFrom, queue ignores handler inbound edge
   - Every input resource produces a `ResolvedResource` (no drops in resolve)
4. Unit tests in `transformer/aggregate_test.go`:
   - aws-serverless: handlerConfig / consumer / schedule / vpc are dropped from `EmitPlan.Primaries`; handler / api / queue remain
   - aws v1: every handler + api collapses into a single `*service.ResolvedService` primary; queues/topics/datastores remain as their own primaries
   - aws v1 with no handlers and no APIs: no `ResolvedService` is produced
5. Unit tests in `transformer/emit_test.go`:
   - Same blueprint, both targets: `${resources.handler.spec.arn}` rewrites to `${values.handler_lambda_arn}` under aws-serverless and `${values.celerity_service_arn}` under aws v1, in **every** ref-bearing section: an export, a user-defined value, an `include` variable, a data source filter, top-level metadata, and a Lambda env var. All five places end up with identical concrete refs per target.
   - User-defined value collision with a transformer-derived value name returns an error from `mergeValues`
   - Output blueprint's `transform` list has the current transformer's ID removed; other transformer IDs in the list are preserved; field is nil when the input listed only this transformer
6. Unit tests in `libs/plugin-framework/sdk/pluginutils/rewrite_blueprint_test.go`:
   - Each ref-bearing section (exports, values, include, datasources, metadata) is walked; variables and version are not visited
   - Blueprint with no top-level sections set returns a shallow copy without panicking on nil sections
7. Integration tests:
   - Minimal blueprint → aws-serverless → lambda + event source mapping + IAM role in output
   - Same blueprint → aws → ECS task definition + service + cluster + ALB + IAM in output (no Lambda resources)
