# Bluelink Transformer Plugin Framework Architecture

## Audience

This is the architecture of the **transformer plugin framework** that
Bluelink ships in `libs/plugin-framework`. It is *the* documented happy
path for building any transformer plugin in the Bluelink ecosystem —
Celerity is the first user, but the toolkit is general-purpose.

If you are:
- **Building a Bluelink transformer plugin** — this doc is your foundation.
  The companion authoring guide walks through using the framework
  step-by-step.
- **Maintaining the Celerity transformer** — Celerity is the reference
  implementation; the pipeline doc (`resolve-emit-pipeline-design.md`)
  shows how Celerity uses what's described here.
- **Extending the framework itself** — this doc records the design
  decisions and the layering contract that lets transformers opt in or
  opt out.

This doc is laid out **top-down from the user's entry point**: start at
`TransformerPluginDefinition` (the struct you declare), drop into
`RunTransformPipeline` (what the framework runs underneath), then into
the four pillars that the pipeline consults, then into operational
concerns and reference material. Read sequentially to follow the same
path code goes through; jump to a pillar when you need depth on one
piece.

## Versioning Note

Throughout this doc, **"v0" and "v1" refer to the Celerity transformer's
version scope, not the plugin framework's**. The plugin framework is a
library with its own semver trajectory; it ships the primitives below
ahead of or alongside Celerity v0 and continues to release independently.
What changes between Celerity v0 and v1 is which deploy targets the
transformer registers for — not the framework API.

The framework's stability bar is high: once the v1.0 of `plugin-framework`
ships these primitives, breaking changes follow standard deprecation
cycles. This is a public-API-grade surface, not internal Celerity machinery.

## Goal

The framework provides an opinionated-but-opt-out toolkit for transformer
plugins. The opinionated default — the **pipeline toolkit** — handles the
common shape: take a blueprint with abstract resources, resolve their
links, decide what becomes an emit unit per deploy target, emit concrete
resources, rewrite cross-references, return a transformed blueprint.
Most transformers fit this shape, including every transformer Bluelink
ships and most third-party plugins we anticipate.

The framework also exposes **core primitives** at a lower layer for
transformers that don't fit the pipeline shape (validation-only, format
conversion, schema migration, blueprint composition). Those plugins use
substitution walkers and rewriters directly without taking on the
pipeline's structure.

The framework must absorb growth without architectural refactoring as
new deploy targets, new abstract resource types, and new transformer
plugins are added. The four pillars below are designed for that — each
fixes a dispatch decision once and grows linearly thereafter, never
quadratically, never with churn to existing code.

**Celerity as the proving ground.** Celerity transformer v1 (May 2027)
is full multi-cloud coverage: `aws-serverless`, `aws`,
`gcloud-serverless`, `gcloud`, `azure-serverless`, `azure`. Celerity v0
ships with the AWS pair only. Celerity v1 is implementation against this
already-established framework — not framework redesign. If Celerity v1
work surfaces a need for framework changes, that's a v0 architecture gap
the framework should fix in place (other plugins will hit it too), not
something Celerity should patch around.

**Target naming convention.** `<cloud>` is the non-serverless (container /
managed compute) variant; `<cloud>-serverless` is the serverless
(functions) variant. For non-code-compute resources (queues, topics,
datastores, buckets, caches, sql databases, secrets/config), the same emit
and rewriter serve both variants of each cloud. Code-compute resources
(handler, api) diverge per variant. This is a Celerity convention; the
framework imposes no constraints on target string format beyond
"non-empty unique string."

## Non-Goals

- Defining abstract resource schemas. Those are owned by per-transformer
  schema packages (`celerity-core` for Celerity); the framework is
  schema-agnostic.
- Prescribing what abstract resources must look like (number of types,
  property shapes, link cardinalities). Each transformer decides.
- Forcing the pipeline toolkit on plugins that don't fit. The opt-out
  path is documented and supported.
- Implementing the Celerity v1 targets. Only the framework + Celerity AWS
  pair lands in Celerity v0.
- Locking the transformer-authoring guide structure (a separate
  deliverable that builds on this architecture).

## Layering: Primitives vs Pipeline Toolkit

The framework exposes two layers. Most transformers use both; some need
only Layer 1.

**Layer 1 — Core Primitives.** Pure utilities for working with blueprints.
No pipeline assumptions; no opinion about what a transformer is shaped
like. Located in `libs/plugin-framework/sdk/pluginutils` (and
`libs/blueprint/subwalk` for the substitution walker). Includes:

- Substitution walker (`WalkStringOrSubstitutions`,
  `WalkMappingNode`) — visit every substitution in a blueprint subtree.
- Resource property rewriter (`RewriteResourcePropertyRefs`,
  `ChainResourcePropertyRewriters`, `PathExact`, `PathMatches`,
  `RewriteFields`, `RetargetRef`, `MakeRef`, `ValueRef`,
  `Field`, `Index`) — declare and apply substitution-tree rewrites.
- Blueprint-level rewriter (`RewriteBlueprintRefs`) — walk every
  ref-bearing top-level section in one call.
- Transformer-list helpers (`StripCurrentTransformerID`) — for
  transformers that publish a transformed blueprint.
- Provenance annotations (`TransformerBaseAnnotations`).

These are documented in detail in §3 of the pipeline doc; they are
framework-level primitives that pre-date the pipeline toolkit.

**Layer 2 — Pipeline Toolkit.** Opinionated. Builds on Layer 1 to provide
the resolve → aggregate → emit shape. This is the documented happy path
for transformers that follow it. Includes (in the order they appear in
this doc):

- `transformerv1.TransformerPluginDefinition` — the user-facing wiring
  point. **Already exists** in
  `libs/plugin-framework/sdk/transformerv1/plugin_definition.go`;
  implements `transform.SpecTransformer` directly via methods on the
  struct. Layer 2 adds an optional `Registry` field; existing
  `TransformFunc` field becomes the explicit Layer 1 escape hatch;
  existing `AbstractResources` / `AbstractLinks` / `TransformerConfigDefinition`
  fields are unchanged.
- `pluginutils.RunTransformPipeline(blueprint, linkGraph, target, reg, ctx)`
  — the engine that the existing `Transform` method calls when `Registry`
  is set.
- `TransformerRegistry` (Pillar 1) — (target, resolved-type) → behavior.
- `EmitPlan` + `EmitResult` (Pillar 2) — primaries, shared parents,
  emit results, contributions.
- `PropertyMap` (Pillar 3) — declarative abstract→concrete property tables.
- Capability matrix (Pillar 4) — derived validation surface.

A transformer that adopts Layer 2 writes registrations and property maps;
the framework drives the pipeline. A transformer that needs something
different uses Layer 1 directly and writes its own driver. **The two
layers are independently usable** — Layer 2 functions reference Layer 1
primitives but the converse is not true.

**When to opt out of Layer 2** is covered in detail after the four
pillars (see "When the Pipeline Toolkit Doesn't Fit").

## Wiring: `TransformerPluginDefinition`

`TransformerPluginDefinition` is the entry point and **already exists** in
`libs/plugin-framework/sdk/transformerv1/plugin_definition.go`. It is a
struct that implements `transform.SpecTransformer` directly via methods
attached to `*TransformerPluginDefinition` — no constructor, no wrapper.
Plugin authors populate fields and pass a `&TransformerPluginDefinition{...}`
value to the gRPC plugin server. The Layer 2 design adds optional fields
to this existing struct and to the existing `AbstractResourceDefinition`
struct; nothing about the existing surface is removed.

The existing struct (truncated to existing fields, with one Layer-2-prep
change to `AbstractResources` noted):

```go
package transformerv1

type TransformerPluginDefinition struct {
    TransformName               string
    TransformerConfigDefinition *core.ConfigDefinition
    // CHANGE: was `map[string]transform.AbstractResource`. Promoting to
    // the concrete pointer type lets the framework's Layer 2 auto-derive
    // pass read pipeline fields (Resolve / PropertyMaps / Emitters /
    // Rewriters) directly, without runtime type assertions, and removes
    // the silent-failure case where a non-AbstractResourceDefinition
    // value bypasses Layer 2. The lookup method
    // `AbstractResource(ctx, type) (transform.AbstractResource, error)`
    // keeps its interface return type — *AbstractResourceDefinition
    // satisfies the interface, so Go auto-converts at the return site.
    // Plugins that want custom behavior compose via struct embedding
    // (`type MyResource struct { *transformerv1.AbstractResourceDefinition }`)
    // rather than substituting a different interface implementation.
    // Worth doing now while plugin-framework is pre-v1.0; can't happen later.
    AbstractResources           map[string]*AbstractResourceDefinition
    AbstractLinks               map[string]*AbstractLinkDefinition
    TransformFunc               TransformFunc
}

func (p *TransformerPluginDefinition) Transform(...) (...) {
    if p.TransformFunc != nil {
        return p.TransformFunc(ctx, input)
    }
    return &transform.SpecTransformerTransformOutput{
        TransformedBlueprint: input.InputBlueprint,
    }, nil  // existing default: pass-through, no transformation
}

// ValidateLinks is fully implemented from AbstractLinks — it walks the
// link graph, validates cardinality and annotation requirements, and
// returns diagnostics. Not user-supplied.
func (p *TransformerPluginDefinition) ValidateLinks(...) (...) { /* ... */ }

// AbstractResource lookup, ListAbstractResourceTypes, etc. are also
// already implemented from the AbstractResources map.
```

Layer 2 fits onto this existing surface in two places:
1. **Per-resource pipeline fields on `AbstractResourceDefinition`** —
   `Resolve`, `PropertyMaps`, `Emitters` (and optional `Rewriters` for
   advanced cases). These are the convenient default — populating them
   means the framework auto-derives the `TransformerRegistry`.
2. **`Aggregators` map on `TransformerPluginDefinition`** — per-target
   aggregator registration. Aggregators are the only registration that
   doesn't fit naturally on a per-resource definition (they're target-wide
   decisions).

The existing `Transform` method is extended so that when `TransformFunc`
is nil and the auto-derived registry has at least one aggregator, it
calls `pluginutils.RunTransformPipeline` instead of the existing
pass-through default. The pass-through remains the fallback when neither
`TransformFunc` nor any pipeline registrations are set — preserving
backwards compatibility with existing skeleton plugins.

### Three Modes

**1. Convenient pipeline mode (Layer 2, recommended).** Populate per-target
pipeline fields on each `AbstractResourceDefinition` plus an `Aggregators`
map on the definition. Framework auto-derives the registry. No `Registry`
field, no `TransformFunc`:

```go
func NewTransformer() transform.SpecTransformer {
    return &transformerv1.TransformerPluginDefinition{
        TransformName: "celerity-2026-04-29",
        TransformerConfigDefinition: configDef,

        // AbstractResources keys match abstract resource type strings.
        // Each value is an *AbstractResourceDefinition carrying metadata
        // (Schema, Label, Examples, etc.) AND pipeline fields populated.
        AbstractResources: map[string]*transformerv1.AbstractResourceDefinition{
            "celerity/queue":   queue.Definition(),
            "celerity/handler": handler.Definition(),
            // ...
        },

        // AbstractLinks unchanged — drives the existing ValidateLinks
        // implementation as today.
        AbstractLinks: map[string]*transformerv1.AbstractLinkDefinition{
            "celerity/handler::celerity/queue": handler.HandlerToQueue(),
            // ...
        },

        // Per-target aggregators — the only registration that doesn't fit
        // on AbstractResourceDefinition because it's a target-wide concern.
        Aggregators: map[string]pluginutils.Aggregator{
            "aws-serverless":    aws.AggregateServerless,
            "aws":               aws.AggregateContainer,
            "gcloud-serverless": gcloud.AggregateServerless,
            // ...
        },

        // TransformFunc and Registry both omitted — framework runs
        // RunTransformPipeline with a registry derived from
        // AbstractResources + Aggregators.
    }
}
```

Each `AbstractResourceDefinition` carries its own Layer 2 fields so that
all knowledge about an abstract resource (schema, examples, resolve
behavior, per-target emit, per-target rewriter) lives in one place:

```go
// resources/queue/definition.go
func Definition() *transformerv1.AbstractResourceDefinition {
    return &transformerv1.AbstractResourceDefinition{
        // -------- Existing metadata fields (unchanged) --------
        Type:                 "celerity/queue",
        Label:                "Queue",
        Schema:               queueSchema,
        IDField:              "name",
        FormattedDescription: "...",
        // ... etc.

        // -------- New Layer 2 fields --------

        // Resolve is target-agnostic Phase 1.
        Resolve: Resolve,

        // PropertyMaps: declarative shortcut. Framework derives a
        // RewriterFactory from each one and registers it.
        PropertyMaps: map[string]pluginutils.PropertyMap{
            "aws-serverless":    awsPropertyMap,  // shared between AWS variants
            "aws":               awsPropertyMap,
            "gcloud-serverless": gcloudPropertyMap,
            // ...
        },

        // Emitters: per-target emit functions.
        Emitters: map[string]pluginutils.Emitter{
            "aws-serverless":    EmitAWS,         // shared between AWS variants
            "aws":               EmitAWS,
            "gcloud-serverless": EmitGCloud,
            // ...
        },

        // Rewriters: only set when PropertyMaps isn't sufficient (e.g.
        // compound resolved types). Per-target. Most resources omit this.
        Rewriters: nil,
    }
}
```

**2. Direct registry mode (advanced).** Set `Registry` on
`TransformerPluginDefinition` directly. Use this for compound resolved
types like `*service.ResolvedService` (folded handlers + APIs in `aws`
container target) — those don't correspond to any single
`AbstractResourceDefinition`, so the auto-derivation can't reach them.
The framework merges `Registry`-set registrations with the auto-derived
ones; collisions panic per resolved decision §2.

```go
return &transformerv1.TransformerPluginDefinition{
    // ... AbstractResources / AbstractLinks / Aggregators as in mode 1 ...

    // Additional registrations that don't fit on any single
    // AbstractResourceDefinition (e.g. compound resolved types).
    Registry: func() *pluginutils.TransformerRegistry {
        reg := pluginutils.NewTransformerRegistry()
        pluginutils.RegisterEmit(reg, "aws", service.EmitAWS)        // *ResolvedService
        pluginutils.RegisterRewriter(reg, "aws", service.AWSRewriter) // *ResolvedService
        return reg
    }(),
}
```

**3. Full override (Layer 1).** Set `TransformFunc`. Skip pipeline fields
entirely. Framework defers to your function exactly as it does today:

```go
return &transformerv1.TransformerPluginDefinition{
    TransformName: "policy-validator-2026",
    TransformFunc: runPolicyChecks,
    // No AbstractResources/AbstractLinks/Aggregators/Registry needed.
}
```

`runPolicyChecks` is plain code: walk the blueprint with Layer 1
helpers, accumulate diagnostics, return the input blueprint unchanged.

### Required vs Optional in Default-Pipeline Mode

For mode 1 (the recommended path), the minimum viable
`TransformerPluginDefinition` populates four fields:

```go
&transformerv1.TransformerPluginDefinition{
    TransformName:     "celerity-2026-04-29",
    AbstractResources: ...,  // each entry has Resolve / Emitters / PropertyMaps populated
    AbstractLinks:     ...,
    Aggregators:       ...,
}
```

Fields by category:

| Field | Required for mode 1? | Notes |
|---|---|---|
| `TransformName` | **Yes** | Empty string is valid for `GetTransformName` but useless in practice. |
| `AbstractResources` | **Yes** | At least one entry; each must have `Resolve` and `Emitters` populated for every supported target. |
| `AbstractLinks` | If you have links to validate | Drives existing `ValidateLinks`; not required by the pipeline itself. |
| `Aggregators` | **Yes** for any target you support | Missing target → "transformer does not support deploy target" error per resolved decision §5. |
| `TransformerConfigDefinition` | No | Plugin accepts no config when nil. |
| `Registry` | No (mode 2 only) | Merged with the auto-derived registry. |
| `TransformFunc` | No (mode 3 only) | Wins over pipeline mode if set. |

Per-`AbstractResourceDefinition` Layer 2 fields:

| Field | Required for mode 1? | Notes |
|---|---|---|
| `Resolve` | **Yes** | Auto-registered as the resolver for `Type`. |
| `Emitters` | **Yes** for every target this resource supports | Per-target. |
| `PropertyMaps` | Strongly recommended | Auto-derived rewriter factory; capability matrix derives from this too. |
| `Rewriters` | No, only when `PropertyMaps` isn't sufficient | Per-target. Overrides auto-derivation from `PropertyMaps` for that target. |

### Full Struct Contract (with Layer 2 additions)

```go
package transformerv1

type TransformerPluginDefinition struct {
    // -------- Existing fields (unchanged) --------

    TransformName               string
    TransformerConfigDefinition *core.ConfigDefinition
    AbstractResources           map[string]*AbstractResourceDefinition  // see "Existing struct" note above for the change from interface-typed map
    AbstractLinks               map[string]*AbstractLinkDefinition
    TransformFunc               TransformFunc

    // -------- New fields (Layer 2) --------

    // Aggregators registers per-target aggregators. Required for any
    // target the plugin supports in pipeline mode. Keyed by deploy target
    // string (e.g. "aws-serverless").
    Aggregators map[string]pluginutils.Aggregator

    // Registry holds advanced registrations that don't fit on any
    // AbstractResourceDefinition (most commonly: compound resolved types
    // produced by full-fold aggregators, like *ResolvedService). The
    // framework merges this with the registry auto-derived from
    // AbstractResources + Aggregators at first Transform invocation.
    // Collisions between auto-derived and explicit registrations panic.
    Registry *pluginutils.TransformerRegistry

    // OnRun, if set, runs once per transform run before any phase. The
    // canonical job of OnRun is to load run-scoped state (e.g. a build
    // manifest whose path lives in transform.Context) and stash it on
    // the supplied *Run via transformutils.Provide. Phases retrieve via
    // transformutils.Use[T]. A non-nil error aborts the run before any
    // phase fires. See "Per-Run State" below.
    OnRun pluginutils.OnRun
}

// Run holds per-pipeline-call state. The framework allocates one Run
// per RunTransformPipeline call, threads it as an explicit *Run parameter
// to every phase, and exposes typed Provide/Use accessors for arbitrary
// plugin-defined run-scoped values. Run is not shared across pipeline
// calls; each call allocates a fresh instance, so concurrent runs are
// isolated by construction.
type Run struct {
    // Transform is the per-run transformer context. Phases consult it
    // for config and context variables. Carrying it on Run avoids a
    // separate transformCtx parameter on every phase signature.
    Transform transform.Context

    mu      sync.RWMutex
    storage map[reflect.Type]any
}

// Provide stashes a typed value on the run, keyed by Go type. Intended
// to be called from OnRun (the canonical site), but a phase can also
// Provide values for later phases in the same run if needed.
//
// Two values with the same Go type collide on the same key. If a plugin
// needs to distinguish (e.g. two []string configs), use newtype wrappers
// (`type BuildManifestPath string`) to disambiguate.
func Provide[T any](r *Run, v T)

// Use retrieves a typed value previously stashed by Provide. Returns
// (zero, false) if no value of type T has been provided on this run.
// MustUse[T] panics on absence and is suitable when the absence is a
// wiring bug, not a recoverable condition.
func Use[T any](r *Run) (T, bool)
func MustUse[T any](r *Run) T

// OnRun runs once per RunTransformPipeline call, before any phase. A
// non-nil error aborts the run; no phase registered on the plugin
// fires when OnRun returns an error.
type OnRun func(ctx context.Context, run *Run) error

// AbstractResourceDefinition gains four pipeline fields. All other
// existing fields (Type, Label, Schema, IDField, examples, validate
// hooks, etc.) are unchanged.
type AbstractResourceDefinition struct {
    // -------- Existing fields (unchanged) --------
    Type, Label              string
    Schema                   *provider.ResourceDefinitionsSchema
    IDField                  string
    PlainTextSummary, FormattedSummary       string
    PlainTextDescription, FormattedDescription string
    PlainTextExamples, FormattedExamples     []string
    CommonTerminal           bool
    CommonTerminalFunc       func(...) (...)
    CustomValidateFunc       func(...) (...)

    // -------- New fields (Layer 2) --------

    // Resolve is the target-agnostic Phase 1 resolver. Auto-registered
    // by the framework against this definition's Type when the parent
    // TransformerPluginDefinition is first used.
    Resolve pluginutils.Resolver

    // PropertyMaps registers declarative property maps per target.
    // Consumed in two places:
    //   - The capability matrix (Pillar 4) is auto-derived from each map.
    //   - transformutils.RewriterFromPropertyMap[T] turns a map plus a
    //     per-target concrete-naming function into a RewriterRegistration
    //     for the Rewriters field below.
    // A PropertyMap on its own does not auto-register a rewriter —
    // concrete naming is per-target and must be supplied via
    // RewriterFromPropertyMap (or a hand-written TypedRewriter factory).
    PropertyMaps map[string]transformutils.PropertyMap

    // Emitters registers per-target emit functions. Each value is a
    // typed-emitter closure produced by transformutils.TypedEmitter[T]
    // — that helper captures the concrete resolved type so the framework
    // can defer the typed RegisterEmit call without ever needing T at
    // the field type. The map's value type cannot be the type-erased
    // transformutils.Emitter because the registry's RegisterEmit is
    // generic over a pointer-typed signature; passing a ResolvedResource-
    // typed function to it fails the ResolvedPtr[T] constraint.
    Emitters map[string]transformutils.EmitterRegistration

    // Rewriters registers per-target rewriter factories. Each value is a
    // RewriterRegistration produced by:
    //   - transformutils.RewriterFromPropertyMap[T] — when rewriting is
    //     fully described by a PropertyMap plus a per-target concrete
    //     name (the typical path).
    //   - transformutils.TypedRewriter[T] — when you need a hand-written
    //     factory (compound primaries, multi-rewriter contributions).
    // A target with no Rewriters entry will not have any rewriter
    // registered. A PropertyMap on its own does not auto-register.
    Rewriters map[string]transformutils.RewriterRegistration
}

// TransformFunc unchanged.
type TransformFunc func(
    ctx context.Context,
    input *transform.SpecTransformerTransformInput,
) (*transform.SpecTransformerTransformOutput, error)
```

### Auto-Deriving the Registry

When `Transform` is first called and `TransformFunc` is nil, the framework
builds an effective `TransformerRegistry` from the definition by walking
three sources in fixed order:

1. **`AbstractResources`** — for each `*AbstractResourceDefinition`,
   register:
   - `RegisterResolver(def.Type, def.Resolve)` if `def.Resolve != nil`
   - For each `(target, emitterReg)` in `def.Emitters`, invoke the closure:
     `emitterReg(reg, target)`. The closure was constructed via
     `TypedEmitter[T]` and internally calls `RegisterEmit` with the
     concrete resolved type bound — the framework neither knows nor
     needs `T` at this layer.
   - For each `(target, rewriterReg)` in `def.Rewriters` (if set),
     invoke `rewriterReg(reg, target)`. The closure was constructed via
     `RewriterFromPropertyMap[T]` (typical) or `TypedRewriter[T]`
     (compound / multi-rewriter cases). A target with no `Rewriters`
     entry registers no rewriter — `PropertyMaps` alone do not
     auto-register a rewriter, because concrete naming is per-target
     and only `RewriterFromPropertyMap` supplies it.
   - Capability matrix entry derived from `def.PropertyMaps[target]` for
     each supported target.

   Map-iteration order is non-deterministic in Go, so the builder sorts
   keys (resource type, then target string) before registering. This
   keeps any error or panic message reproducible.

   No type assertions: `AbstractResources` is concretely typed as
   `map[string]*AbstractResourceDefinition`, so every entry exposes the
   pipeline fields directly.
2. **`Aggregators`** — for each `(target, fn)` (sorted by target),
   `RegisterAggregator(target, fn)`.
3. **`Registry`** (if non-nil) — additional registrations merged on top.
   Collisions with auto-derived registrations panic.

The order matters because step 3 must observe the registrations from
steps 1–2 to detect collisions. It also means any failure introduced by
the explicit `Registry` overlay is reported with the auto-derived state
fully formed, so error messages can name both sides of the collision.

How this build is invoked, cached, and synchronised is the subject of the
next subsection.

Custom behavior on a `transform.AbstractResource` (typically: custom
validation logic, dynamic terminal-resource detection) is supplied via
the existing `CustomValidateFunc` and `CommonTerminalFunc` fields on
`AbstractResourceDefinition` — these are user-supplied functions that
the struct's own method implementations call when set. The other
interface methods (`GetType`, `GetTypeDescription`, `GetExamples`,
`GetSpecDefinition`) are pure projections of struct fields, so there's
nothing for an alternate implementation to override that the struct
doesn't already cover. In practice the concrete-typed map gives up
nothing.

### Building the Effective Registry

The walk above runs inside a single `effectiveRegistry()` method that
caches its result for the lifetime of the definition. Implementation:
four unexported fields on `TransformerPluginDefinition` and one
`sync.Once`-guarded build.

```go
type TransformerPluginDefinition struct {
    // ...existing exported fields (unchanged)...

    // Lazy-derivation cache. Populated on first Transform; immutable
    // thereafter. Not part of the public API.
    deriveOnce         sync.Once
    derivedRegistry    *transformutils.TransformerRegistry
    derivedHasPipeline bool
    derivedErr         error
}

func (p *TransformerPluginDefinition) effectiveRegistry() (
    *transformutils.TransformerRegistry, bool, error,
) {
    p.deriveOnce.Do(func() {
        p.derivedRegistry, p.derivedHasPipeline, p.derivedErr = p.buildRegistry()
    })
    return p.derivedRegistry, p.derivedHasPipeline, p.derivedErr
}
```

`buildRegistry` is the function that performs the three-source walk
described above. It returns:

- `*transformutils.TransformerRegistry` — a freshly constructed registry,
  populated from snapshots of the source maps (see "Snapshotting" below).
  Always non-nil, even when no fields were populated.
- `hasPipeline bool` — `true` if any source contributed any registration:
  `Aggregators` non-empty, any `AbstractResourceDefinition` with at least
  one Layer-2 field set, or a non-nil explicit `Registry` with at least
  one entry. `false` otherwise; `Transform` falls through to the no-op
  pass-through.
- `err error` — non-nil for any condition the validation table below
  classifies as "Error" (as opposed to "Panic"). Surfaced to every
  subsequent `Transform` call as the same error.

**Snapshotting.** `buildRegistry` reads the source maps once and copies
the registrations into the registry it returns. Mutations to
`AbstractResources`, `Aggregators`, or `Registry` after first `Transform`
are inert — the cached registry is independent of subsequent edits.
This makes the freeze-on-first-`Transform` contract mechanical rather
than convention-based; authors don't have to remember a "don't mutate
after first use" rule.

**Concurrency.** `sync.Once` guarantees exactly one builder run under
concurrent first-call from multiple goroutines. All callers receive the
same `(registry, hasPipeline, err)` triple. After the first call,
`effectiveRegistry()` is a pair of unsynchronised reads of fields that
the `Once.Do` happens-before relation has already published, so the hot
path is lock-free.

**Copy safety.** The embedded `sync.Once` makes
`*TransformerPluginDefinition` non-copy-safe — `go vet` will flag any
attempt to copy a value after first `Transform`. Existing plugin-host
wiring already passes the definition by pointer end-to-end, so this is
satisfied in practice; it's worth stating because adding the embedded
synchroniser changes the struct's copy semantics from the pre-Layer-2
shape.

**Panic propagation.** Cases the validation table classifies as "Panic"
(mixed Layer-1 + Layer-2, duplicate registration in `Registry` overlay)
panic inside `buildRegistry` and therefore inside `deriveOnce.Do`.
`sync.Once` records the panic and re-panics on every subsequent call —
so a misconfigured plugin keeps panicking instead of silently advancing
to no-op mode. This is intentional: a panic on first `Transform` is
already a programmer error; converting it to a one-shot panic followed
by silent no-ops would mask the bug.

**No constructor required.** All of this state has useful zero values
(zero `sync.Once`, nil registry, false `hasPipeline`, nil error), so
plugin authors continue to instantiate `TransformerPluginDefinition` via
struct literal exactly as today. The lazy-init pattern is what lets the
struct stay declaratively constructible while still doing the work that
would normally need a constructor.

### Validation at First Use

Framework checks happen inside `buildRegistry` (i.e. inside the
`deriveOnce.Do` invoked by `effectiveRegistry()`), which means they fire
on first `Transform` call. Eager checks at construction time aren't
possible without a constructor, and the existing struct has no init
hook. Misconfiguration produces fail-fast errors with deterministic
messages (the builder sorts source-map keys before walking, so two runs
on the same definition produce the same diagnostic order):

| Configuration | Behavior |
|---|---|
| `TransformFunc` set, pipeline fields ALSO populated | Panic: "TransformerPluginDefinition has both TransformFunc and Layer-2 pipeline registrations; pick one." |
| Pipeline fields populated, `Aggregators` empty | Error on first Transform: "no aggregators registered." |
| Pipeline fields populated, deploy target not in `Aggregators` | Error on Transform with that target: "transformer does not support deploy target X" (resolved decision §5). |
| `AbstractResources` entry's `Emitters[target]` is nil but `Aggregators[target]` is set | Error: "no emitter registered for celerity/queue on target aws-serverless." |
| `Registry` collides with auto-derived registration | Panic: "duplicate registration for (target, type)" (resolved decision §2). |
| All Layer-1 and Layer-2 fields nil/empty | **No-op pass-through** — preserves existing skeleton-plugin behavior. |

Per-resource `Resolve` / `Emitters` consistency is checked at the same
time: every `AbstractResources` entry that has any Layer-2 field set must
have at least `Resolve` and at least one `Emitters` entry, otherwise the
framework errors with a clear "incomplete pipeline registration" message.

### How the Framework Consumes the Definition

The existing methods on `*TransformerPluginDefinition` already implement
`transform.SpecTransformer`. Layer 2 only modifies the `Transform` method
to add the pipeline branch:

```go
// Inside transformerv1/plugin_definition.go after Layer 2 lands.
package transformerv1

func (p *TransformerPluginDefinition) Transform(
    ctx context.Context,
    input *transform.SpecTransformerTransformInput,
) (*transform.SpecTransformerTransformOutput, error) {
    if p.TransformFunc != nil {
        return p.TransformFunc(ctx, input)  // Layer 1 — existing behavior
    }

    reg, hasPipeline, err := p.effectiveRegistry()  // sync.Once-guarded; see "Building the Effective Registry"
    if err != nil {
        return nil, err  // surfaced identically to every concurrent first-caller
    }
    if hasPipeline {
        target := transformutils.Target(getDeployTarget(input.TransformerContext))
        return transformutils.RunTransformPipeline(
            input.InputBlueprint,
            input.LinkGraph,
            target,
            reg,
            input.TransformerContext,
        )
    }

    // Existing default — preserves backwards compatibility with skeleton plugins.
    return &transform.SpecTransformerTransformOutput{
        TransformedBlueprint: input.InputBlueprint,
    }, nil
}
```

`ValidateLinks`, `AbstractResource`, `ListAbstractResourceTypes`, and the
other existing methods are **unchanged**. They continue to use
`AbstractResources` and `AbstractLinks` exactly as they do today.

### Why This Shape

- **Single source of truth per abstract resource.** Schema, examples,
  validate hooks, resolver, emitters, and rewriters all live on the same
  `AbstractResourceDefinition`. Keeps the per-resource code together;
  no parallel maps to keep consistent.
- **No runtime type assertions in framework code.** `AbstractResources`
  is concretely typed `map[string]*AbstractResourceDefinition`, so the
  auto-derivation pass reads `def.Resolve` / `def.PropertyMaps` /
  `def.Emitters` directly. Eliminates the silent-failure case where a
  custom `transform.AbstractResource` implementation would have bypassed
  Layer 2 with no error; eliminates per-call assertion overhead; makes
  framework code self-documenting at the read site.
- **Layer 2 = default, Layer 1 = explicit opt-out.** Opinionation shows up
  at the very first decision the plugin author makes (which fields to
  populate). Mixed mode (TransformFunc + pipeline fields both set)
  panics — there's no quiet way to fall into a wrong mode.
- **Backwards-compatible.** Existing transformer plugins that use
  `TransformerPluginDefinition` today (with no Layer 2 fields populated)
  keep working unchanged via the existing pass-through default.
- **Authoring guide simplifies.** "Hello, Transformer" is: define your
  abstract resources with their pipeline fields, define your aggregators,
  return the definition. No registry construction, no driver code.
- **Compound resolved types still supported.** `Registry` field provides
  the escape hatch for things that can't live on an
  `AbstractResourceDefinition` (e.g. `*ResolvedService` for full-fold
  aggregators). Merged with auto-derived; collisions panic.

### Per-Run State

`Aggregators`, `Emitters`, `Rewriters`, and `Resolvers` are all registered
at plugin **load time** — the function values registered then have to
serve every subsequent run. That's fine when a phase's behavior depends
only on inputs already in its signature (the resolved resource list,
`transform.Context`, etc.), but breaks down when a phase needs a piece of
**run-scoped state that the registered function can't have known about
at load time**. The canonical case is a build manifest loaded from a
path that lives in `transform.Context`: the path isn't known until a run
starts, so the manifest can't be baked into the registration.

The framework answers this by threading a `*Run` value through every
phase. `Run` carries `transform.Context` plus a typed storage area that
plugins populate via an optional `OnRun` hook and consume from any
phase via `Provide` / `Use`. The phase graph is built **once** at
plugin load time and never rebuilt; only the `*Run` value is allocated
per pipeline call.

```go
def := transformerv1.TransformerPluginDefinition{
    // … load-time phase registrations as in Mode 1 …
    Aggregators: map[transformutils.Target]transformutils.Aggregator{
        "aws-serverless": aggregateAWSServerless, // registered once, never rebuilt
    },

    OnRun: func(ctx context.Context, run *transformutils.Run) error {
        pathScalar, _ := run.Transform.TransformerConfigVariable("celerity.buildManifestPath")
        manifest, err := manifestLoader.Load(ctx, core.StringValueFromScalar(pathScalar), run.Transform)
        if err != nil {
            return fmt.Errorf("load build manifest: %w", err)
        }
        transformutils.Provide(run, manifest) // typed; later phases retrieve via Use[*build.Manifest]
        return nil
    },
}

func aggregateAWSServerless(
    ctx context.Context,
    run *transformutils.Run,
    resolved []transformutils.ResolvedResource,
) (*transformutils.EmitPlan, error) {
    manifest, ok := transformutils.Use[*build.Manifest](run)
    if !ok {
        return nil, errors.New("OnRun must Provide *build.Manifest before aggregate")
    }
    // … use manifest …
}
```

**Semantics.**

- `OnRun` is optional. Plugins with no run-scoped state omit it; the
  pipeline allocates an empty `*Run` carrying just `Transform` and
  proceeds directly to phase work.
- A non-nil error from `OnRun` aborts the run before any resolve /
  aggregate / emit work fires. The error surfaces as the
  `RunTransformPipeline` return value — no diagnostic accumulation,
  no phase invocation.
- `Run` is **per pipeline call**. The framework allocates a fresh
  instance; no state leaks between concurrent runs. Storage is keyed by
  Go type via `reflect.TypeFor[T]`, guarded by `Run`'s `sync.RWMutex`
  so a phase that calls `Provide` mid-run is safe under concurrent
  reads from other phase callers within the framework.
- Two values with the same Go type collide on the same storage key.
  Plugins distinguish via newtype wrappers (`type BuildManifestPath
  string`) the same way any type-keyed registry does.
- `Use[T]` returns `(zero, false)` on absence; `MustUse[T]` panics. Use
  `Use` when absence is a recoverable condition (e.g. an optional
  config), `MustUse` when absence is a wiring bug (e.g. `OnRun` should
  have provided the value).

**Why this shape rather than the alternatives.**

- **Why not `context.Value`?** The official `context` documentation
  restricts `Value` to "request-scoped data that transits processes and
  APIs, not for passing optional parameters to functions." A build
  manifest is something every consumer consciously reads and acts on —
  a parameter, not an ambient cross-cutting concern. `*Run` is an
  explicit parameter on every phase signature, and access is via typed
  generic accessors (`Use[*Manifest]`); reviewers can grep for every
  consumer. `ctx.Value` fails both tests: it's ambient and untyped.
- **Why not widen `Aggregator` to take `transform.Context` and return
  an error, and have each phase load independently?** It works, but
  the moment a second phase needs the same state (an emitter that also
  consults the manifest), the load is duplicated per run. `*Run` +
  `Provide` lets phases share without duplication, while still keeping
  the load explicit (it happens in `OnRun`, not buried in a cache).
- **Why not per-run bindings — i.e. have a hook return fresh phase
  functions with state closed over?** Cleaner in some ways (no
  `reflect.Type` storage key, fully type-safe at consumer sites), but
  it rebuilds the phase graph for every pipeline call: each run pays
  closure-allocation and lookup-wrapper overhead, and the framework
  carries two parallel registration paths (load-time and run-time
  overlay). `*Run` keeps the graph static — only data flows per run.
- **Why not a typed run-state struct via generics on every phase
  type?** That forces every framework type (`Aggregator[S]`,
  `Emitter[S]`, the registry itself) to be parameterized over the
  plugin's state type, and host processes can't easily mix plugins with
  different `S`. `*Run` confines generics to two accessor functions
  (`Provide`/`Use`); the framework's phase and registry types stay
  monomorphic.

## Execution Pipeline: `RunTransformPipeline`

`RunTransformPipeline` is the engine the framework runs underneath when
`TransformerPluginDefinition.Registry` is set. The plugin author does not call
it directly — it's invoked by the auto-generated `Transform` method shown
above. This section documents what it does so the reader understands what
they're getting when they pick pipeline mode.

```go
func RunTransformPipeline(
    blueprint *schema.Blueprint,
    linkGraph linktypes.DeclaredLinkGraph,
    target Target,
    reg *TransformerRegistry,
    ctx transform.Context,
) (*transform.SpecTransformerTransformOutput, error)
```

### Phase Diagram

```
RunTransformPipeline
  |
  +-- 0. Allocate Run; optionally populate via OnRun
  |       run = &Run{Transform: transformCtx}
  |       If TransformerPluginDefinition.OnRun is set:
  |         err = onRun(ctx, run)
  |         if err != nil → fail-fast "per-run init: %w"
  |       OnRun is the canonical site for loading run-scoped state
  |       (e.g. a build manifest) and stashing it on run via
  |       transformutils.Provide. Phases retrieve via Use[T](run).
  |       Run is fresh per pipeline call — never shared between runs.
  |       Phase functions registered on the registry are unchanged from
  |       load time; only the *Run value is per-run.
  |
  +-- 1. Validate target
  |       reg.AggregatorFor(target) → must be non-nil
  |       Otherwise: error("transformer does not support deploy target %q")
  |       This is the registry-level "target not registered" check from
  |       resolved decision §5. Runs BEFORE any phase work.
  |
  +-- 2. Resolve  (target-agnostic; uses reg.ResolverFor per resource type)
  |       For each resource in blueprint.Resources:
  |         resolver = reg.ResolverFor(resource.Type)
  |         resolved += resolver(name, resource, linkGraph, blueprint)
  |       Output: []ResolvedResource
  |       Errors: missing resolver for an input resource type → fail-fast
  |
  +-- 3. Aggregate  (target-specific; uses reg.AggregatorFor)
  |       plan = reg.AggregatorFor(target)(resolved)
  |       Output: EmitPlan{Primaries, SharedParents}
  |       Aggregator decides absorption (filter / fold / partial-fold)
  |
  +-- 4. Build chained rewriter  (target-specific; uses reg.RewriterFactoryFor)
  |       For each primary in plan.Primaries:
  |         factory = reg.RewriterFactoryFor(target, type(primary))
  |         rewriters += factory(primary)
  |       chained = ChainResourcePropertyRewriters(rewriters...)
  |       Errors: no rewriter factory for a primary's type → fail-fast
  |
  +-- 5. Pre-emit reference validation  (uses Pillar 4 capability matrix)
  |       Walk blueprint substitutions, check each SubstitutionResourceProperty
  |       against capabilities for (target, resolved-type-of-the-named-resource).
  |       Unknown abstract path → diagnostic (warning or error per matrix).
  |       Diagnostics accumulate; do not short-circuit unless severity = error.
  |
  +-- 6. Per-primary emit  (target-specific; uses reg.EmitterFor)
  |       For each primary in plan.Primaries:
  |         emitter = reg.EmitterFor(target, type(primary))
  |         result = emitter(primary, chained, ctx)
  |         emittedResources += result.Resources
  |         derivedValues    += result.DerivedValues  (collision → error)
  |         contributions[k] += result.SharedParentContributions[k]
  |         diagnostics      += result.Diagnostics
  |       Errors: no emitter for a primary's type → fail-fast
  |
  +-- 7. Shared-parent merge  (uses Pillar 2 SharedParents)
  |       For each parent in plan.SharedParents:
  |         merged = MergeMappingNodes(parent.SeedSpec, contributions[parent.Key]...)
  |         (conflicting field values → diagnostic, abort that parent's emit)
  |         emittedResources[parent.ResourceName] = {
  |             Type: parent.ResourceType, Spec: merged, Metadata: parent.Annotations,
  |         }
  |
  +-- 8. Blueprint-level rewrite  (uses RewriteBlueprintRefs from Layer 1)
  |       rewritten = RewriteBlueprintRefs(blueprint, RewriteResourcePropertyRefs(chained))
  |       Walks exports, values, include, datasources, top-level metadata.
  |       Variables and version are pure passthroughs.
  |
  +-- 9. Merge values
  |       finalValues = mergeValues(rewritten.Values, derivedValues)
  |       Collision between user value and derived value → error
  |       (user-defined name shadowing transformer-derived is a bug)
  |
  +-- 10. Strip current transformer ID
  |       prunedTransform = StripCurrentTransformerID(rewritten.Transform, ctx.TransformerID())
  |       Removes this transformer's ID from the transform list so a re-run
  |       of the framework's transform pipeline doesn't apply it twice.
  |
  +-- 11. Assemble output blueprint
          *schema.Blueprint{
              Version:     rewritten.Version,    // passthrough
              Transform:   prunedTransform,      // current ID removed
              Variables:   rewritten.Variables,  // passthrough
              Values:      finalValues,          // rewritten + derived
              Include:     rewritten.Include,    // refs rewritten
              Resources:   emittedResources,     // concrete + shared parents
              DataSources: rewritten.DataSources,// refs rewritten
              Exports:     rewritten.Exports,    // refs rewritten
              Metadata:    rewritten.Metadata,   // refs rewritten
          }
          Returned as SpecTransformerTransformOutput with accumulated diagnostics.
```

### Step-by-Step Reference

The following table summarises which registry the framework consults at
each step, what input it expects, and what failure mode dominates. "Fail-fast"
means the pipeline returns an error immediately; "diagnostic" means a
non-error message is appended to the accumulator and the pipeline
continues.

| # | Step | Registry consulted | Failure mode |
|---|---|---|---|
| 0 | Allocate `*Run`; run `OnRun` if set | (no registry; allocates `*Run` carrying `transform.Context`, runs `OnRun` to populate via `Provide`) | Fail-fast: `OnRun` returned error |
| 1 | Target validation | `AggregatorFor(target)` | Fail-fast: target not supported |
| 2 | Resolve | `ResolverFor(resourceType)` per resource | Fail-fast: missing resolver |
| 3 | Aggregate | `AggregatorFor(target)` | Aggregator returns error → fail-fast |
| 4 | Build chain | `RewriterFactoryFor(target, type)` per primary | Fail-fast: missing rewriter |
| 5 | Reference validation | Capability matrix (auto-derived) | Diagnostic (warning/error per matrix) |
| 6 | Per-primary emit | `EmitterFor(target, type)` per primary | Fail-fast: missing emitter; emit-internal errors propagate |
| 7 | Shared-parent merge | (no registry; uses plan's `SharedParents`) | Diagnostic on conflict; aborts that parent |
| 8 | Blueprint-level rewrite | (no registry; uses Layer 1 helpers) | Pure transformation; no failure |
| 9 | Merge values | (no registry) | Fail-fast on key collision |
| 10 | Strip transformer ID | (no registry; uses `ctx.TransformerID()`) | Pure transformation |
| 11 | Assemble output | — | — |

### What `RunTransformPipeline` Does Not Do

To keep the boundary clear:

- **Pre-transform validation of abstract resources** — done by the host
  before `transformBlueprint` is called (resolved decision §4).
- **Post-transform validation of concrete resources** — done by the
  host's validation phase against the returned blueprint (also §4).
- **Resource-spec inline rewriting during emit** — each `EmitterFor`
  function is responsible for walking carried-through user-written
  refs in spec fields with the chained rewriter. The pipeline supplies
  the chain but doesn't reach into emitter internals.
- **Per-target value validation** — out of framework scope (§4). Each
  emitter handles its own target-specific value constraints if any.

## The Four Pillars (Layer 2)

The pipeline above is built on four design decisions, each fixing a
dispatch concern once and growing linearly thereafter — never
quadratically, never with churn to existing code. The remaining sections
document each pillar in pipeline-flow order: the registry that the
pipeline dispatches through (Pillar 1), the plan shape that drives it
(Pillar 2), the helper most rewriters use (Pillar 3), and the
auto-derived validation surface (Pillar 4).

1. **Registry-based dispatch** — replaces hand-written `switch` ladders.
2. **`EmitPlan` and shared-parent primitive** — primaries, partial-fold
   support, and contribution merging.
3. **Table-driven property maps** — replaces per-target rewriter switches.
4. **Capability matrix (lightweight, derived)** — pre-emit validation and
   auto-generated authoring docs as side effects of registration.

Note: an earlier draft of this doc proposed a fifth pillar — build-time
slim per-cloud binaries (`celerity-aws`, `celerity-gcloud`, `celerity-azure`)
selected by Go build constraints. That was removed. Deploy target is a
per-deployment runtime value (the `deployTarget` transformer config
variable on `transform.Context`), so binary selection would have to
follow runtime config, but the plugin host has no mechanism for that and
changing target is meant to be low-friction. Slim binaries would force
practitioners to swap the installed transformer when switching targets,
defeating the runtime-config model. Celerity ships as **one binary with
every cloud's registrations compiled in**; the `deployTarget` config
variable selects the target via the registry at runtime. See
"Distribution Model" below.

## Pillar 1: Registry-Based Dispatch

Today, three places in the driver hand-roll target dispatch:
`aggregate.go` → switch on target → switch on resolved type;
`emit.go::buildRewriters` → same; `emit.go::emitResource` → same. With six
targets and ten resource types, that's 180+ switch arms maintained by hand,
and adding a new resource type touches every target's switches.

Replace all three with a single `TransformerRegistry` that resource packages
populate at registration time. Drivers do lookups, not switches.

```go
// transformutils/registry.go

type Target string  // e.g. "aws-serverless", "azure"

// ResolvedResource is the target-agnostic output of the resolve phase
// that is fed into aggregation, rewriting, and emission. Concrete
// resolved types (e.g. *ResolvedQueue, *ResolvedHandler) live in their
// resource packages and implement this interface with pointer receivers.
type ResolvedResource interface {
    ResourceName() string
    ResourceType() string
}

// Aggregator: target → ([]ResolvedResource → EmitPlan). Receives *Run to
// access per-pipeline-call state (e.g. a build manifest stashed by OnRun);
// returns an error on hard failure so the pipeline can fail fast rather
// than relying on EmitPlan.Diagnostics for fatal conditions.
type Aggregator func(ctx context.Context, run *Run, resolved []ResolvedResource) (*EmitPlan, error)

// Emitter: produces concrete output for one resolved primary for a specific
// target. Reads transform.Context via run.Transform; reads run-scoped state
// via transformutils.Use[T](run).
type Emitter func(ctx context.Context, run *Run, r ResolvedResource, chained ResourcePropertyRewriter) (*EmitResult, error)

// RewriterFactory: produces zero or more rewriters from one resolved primary.
// Returns multiple for compound primaries (e.g. ResolvedService folds N
// handlers + M apis, contributes N+M rewriters).
//
// RewriterFactory is deliberately the only phase type that does not receive
// *Run, ctx, or an error return. Rewriting is a synchronous metadata
// transformation on an already-resolved resource; anything that needs
// run-scoped state belongs in the resolver (which has *Run) or the emitter.
type RewriterFactory func(r ResolvedResource) []ResourcePropertyRewriter

// Resolver: target-agnostic Phase 1 resolver for one abstract resource type.
// Keyed by abstract resource type string (e.g. "celerity/queue") — resolve
// is the same regardless of deploy target, so this registration has no
// Target dimension.
type Resolver func(
    ctx context.Context,
    run *Run,
    name string,
    resource *schema.Resource,
    linkGraph linktypes.DeclaredLinkGraph,
    blueprint *schema.Blueprint,
) (ResolvedResource, error)

type TransformerRegistry struct {
    resolvers    map[string]Resolver                       // keyed by abstract resource type
    aggregators  map[Target]Aggregator
    emitters     map[Target]map[reflect.Type]Emitter
    rewriters    map[Target]map[reflect.Type]RewriterFactory
    capabilities map[Target]map[reflect.Type]Capabilities  // see Pillar 4
}

func NewTransformerRegistry() *TransformerRegistry { /* ... */ }

// Resolver registration is keyed by abstract resource type string (no target).
func (r *TransformerRegistry) RegisterResolver(abstractResourceType string, fn Resolver)

func (r *TransformerRegistry) RegisterAggregator(t Target, a Aggregator)

// Generic helpers ensure type safety on the resolved type. Concrete
// resolved types implement ResolvedResource with pointer receivers, so
// the constraint is expressed via ResolvedPtr[T] — "PR is *T and *T
// satisfies ResolvedResource". Both type parameters are inferred from
// the function-value argument; callers do not write them explicitly.
//
// ResolvedPtr lives alongside ResolvedResource so resource-type packages
// can use the same constraint for their own per-resource generic helpers.
type ResolvedPtr[T any] interface {
    *T
    ResolvedResource
}

func RegisterEmit[T any, PR ResolvedPtr[T]](
    reg *TransformerRegistry,
    t Target,
    fn func(r PR, chained ResourcePropertyRewriter, ctx transform.Context) (*EmitResult, error),
)

func RegisterRewriter[T any, PR ResolvedPtr[T]](
    reg *TransformerRegistry,
    t Target,
    fn func(r PR) []ResourcePropertyRewriter,
)

// EmitterRegistration / RewriterRegistration are deferred-registration
// closures that already capture the concrete resolved type. They exist
// because AbstractResourceDefinition needs a homogeneous map value type,
// but RegisterEmit / RegisterRewriter are generic over PR — those two
// constraints can't be reconciled with a non-generic field type alone.
//
// Authors construct these via TypedEmitter[T] / TypedRewriter[T]; the
// framework calls the closure during effectiveRegistry build, which
// internally invokes RegisterEmit / RegisterRewriter with the typed
// function still bound. Static type safety on the author's emit /
// rewriter function is preserved (the constraint check happens inside
// TypedEmitter / TypedRewriter); the framework never has to know T.
type EmitterRegistration  func(reg *TransformerRegistry, t Target)
type RewriterRegistration func(reg *TransformerRegistry, t Target)

func TypedEmitter[T any, PR ResolvedPtr[T]](
    fn func(r PR, chained ResourcePropertyRewriter, ctx transform.Context) (*EmitResult, error),
) EmitterRegistration {
    return func(reg *TransformerRegistry, t Target) {
        RegisterEmit(reg, t, fn)
    }
}

func TypedRewriter[T any, PR ResolvedPtr[T]](
    fn func(r PR) []ResourcePropertyRewriter,
) RewriterRegistration {
    return func(reg *TransformerRegistry, t Target) {
        RegisterRewriter(reg, t, fn)
    }
}

// RewriterFromPropertyMap is the declarative path for the common case:
// rewriting is fully described by a PropertyMap, and the only per-target
// piece of information that the map can't carry is the concrete resource
// name (e.g. "<abstractName>_sqs" on AWS, "<abstractName>_topic" on Pub/Sub).
// concreteName supplies that. The returned RewriterRegistration goes into
// AbstractResourceDefinition.Rewriters[target].
//
// For compound primaries or multi-rewriter contributions, use TypedRewriter
// with a hand-written factory instead.
func RewriterFromPropertyMap[T any, PR ResolvedPtr[T]](
    pm *PropertyMap,
    concreteName func(r PR) string,
) RewriterRegistration {
    return func(reg *TransformerRegistry, t Target) {
        RegisterRewriter(reg, t, func(r PR) []ResourcePropertyRewriter {
            return []ResourcePropertyRewriter{
                pm.Rewriter(r.ResourceName(), concreteName(r)),
            }
        })
    }
}

// Lookups used by the driver. Return nil if unregistered; caller errors.
// These return the type-erased Emitter / RewriterFactory because that's
// what the per-primary dispatch path consumes — at lookup time the
// concrete resolved type is unknown to the caller.
func (r *TransformerRegistry) ResolverFor(abstractResourceType string) Resolver
func (r *TransformerRegistry) AggregatorFor(t Target) Aggregator
func (r *TransformerRegistry) EmitterFor(t Target, rt reflect.Type) Emitter
func (r *TransformerRegistry) RewriterFactoryFor(t Target, rt reflect.Type) RewriterFactory
```

Resource packages register at construction time:

Resource packages that build a `TransformerRegistry` directly (no
`AbstractResourceDefinition`) call `RegisterEmit` / `RegisterRewriter`
in their own setup function — type parameters are inferred from each
function's signature (e.g. `func(*ResolvedQueue, ...)` →
`T = ResolvedQueue`, `PR = *ResolvedQueue`):

```go
// resources/queue/register.go (low-level path)
func Register(reg *transformutils.TransformerRegistry) {
    reg.RegisterResolver("celerity/queue", Resolve)

    transformutils.RegisterEmit(reg, AWSServerless,    EmitAWS)
    transformutils.RegisterEmit(reg, AWS,              EmitAWS)
    transformutils.RegisterEmit(reg, GCloudServerless, EmitGCloud)
    transformutils.RegisterEmit(reg, GCloud,           EmitGCloud)
    transformutils.RegisterEmit(reg, AzureServerless,  EmitAzure)
    transformutils.RegisterEmit(reg, Azure,            EmitAzure)

    transformutils.RegisterRewriter(reg, AWSServerless,    AWSRewriterFactory)
    transformutils.RegisterRewriter(reg, AWS,              AWSRewriterFactory)
    // ... same pattern for GCloud and Azure pairs
}
```

Resource packages that go through `AbstractResourceDefinition` instead
wrap each typed function in `TypedEmitter` / `TypedRewriter` so the map
holds homogeneous values:

```go
// resources/queue/abstract.go (declarative path)
var Definition = &transformerv1.AbstractResourceDefinition{
    Type:    "celerity/queue",
    Resolve: Resolve,
    PropertyMaps: map[string]transformutils.PropertyMap{
        AWSServerless: awsPropertyMap,
        AWS:           awsPropertyMap,
        // ...
    },
    Emitters: map[string]transformutils.EmitterRegistration{
        AWSServerless:    transformutils.TypedEmitter(EmitAWS),
        AWS:              transformutils.TypedEmitter(EmitAWS),
        GCloudServerless: transformutils.TypedEmitter(EmitGCloud),
        GCloud:           transformutils.TypedEmitter(EmitGCloud),
        AzureServerless:  transformutils.TypedEmitter(EmitAzure),
        Azure:            transformutils.TypedEmitter(EmitAzure),
    },
    Rewriters: map[string]transformutils.RewriterRegistration{
        AWSServerless: transformutils.RewriterFromPropertyMap(&awsPropertyMap,
            func(r *ResolvedQueue) string { return r.Name + "_sqs" }),
        AWS: transformutils.RewriterFromPropertyMap(&awsPropertyMap,
            func(r *ResolvedQueue) string { return r.Name + "_sqs" }),
        // ... GCloud / Azure entries follow the same pattern.
    },
}
```

`TypedEmitter(EmitAWS)` infers `T = ResolvedQueue` and `PR = *ResolvedQueue`
from `EmitAWS`'s signature, validates the `ResolvedPtr[T]` constraint at
compile time, and returns a closure the framework invokes during
`buildRegistry`. `RewriterFromPropertyMap` does the same for the rewriter
side, plus binds the per-target concrete-naming function — that's the
piece a `PropertyMap` literal can't carry on its own. For compound
primaries (one resolved type producing many rewriters), substitute
`TypedRewriter(handWrittenFactory)` for the helper.

The transformer's `main` wires it up:

```go
// transformer/transformer.go
func newRegistry() *transformutils.TransformerRegistry {
    reg := transformutils.NewTransformerRegistry()

    handler.Register(reg)
    queue.Register(reg)
    topic.Register(reg)
    // ... one Register call per resource package

    aws.RegisterAggregators(reg)    // per-cloud aggregator registrations
    gcp.RegisterAggregators(reg)
    azure.RegisterAggregators(reg)
    return reg
}
```

**Adding a new target** is now: `RegisterAggregator(NewTarget, ...)` plus N
`RegisterEmit` and N `RegisterRewriter` calls in each resource package (or
in a target-specific file alongside the resource). Zero changes to driver
code.

**Adding a new resource type** is now: define the resolved struct, define
`Resolve`, define `EmitX` / `RewriterX` per supported target, register them.
Zero changes to driver code or to other resource types.

## Pillar 2: `EmitPlan` and Shared-Parent Primitive

The aggregator's job is to produce an `EmitPlan` from the resolved resource
list. The plan describes what the per-primary emit step will iterate over
and what shared concrete parents (if any) will be assembled from those
primaries' contributions.

A flat `EmitPlan{Primaries []ResolvedResource}` supports two patterns:

- **filter** — drop contributory-only types; primaries emit independently
  (`aws-serverless`).
- **full-fold** — many abstracts → one synthetic `ResolvedService`;
  per-abstract emits no longer exist (`aws`).

It does not, by itself, support **partial-fold**: many primaries that
*each* emit their own concrete resource AND cooperatively contribute to a
shared concrete parent. Real cases:

- **Azure Functions** cluster under one `azure/web/site` (Function App).
  Each handler still emits its own `azure/web/functions` resource, but they
  share a Function App that holds runtime stack + app settings.
- **Cloud Run services** may share a regional Cloud Run config holding
  shared concurrency/timeout/security defaults.
- **VPC connectors** are shared by multiple compute resources but are
  themselves a distinct concrete resource.

`EmitPlan` extends the flat list with a shared-parent declaration.
Aggregate seeds it; emits contribute to it; the driver materialises it as
a concrete resource after all per-primary emits finish.

```go
type EmitPlan struct {
    Primaries     []ResolvedResource
    SharedParents []SharedParent
}

type SharedParent struct {
    // Key is unique within the plan. Convention: "<target>:<purpose>:<scope>".
    // e.g. "azure-serverless:functionApp:default".
    Key          string
    ResourceName string                  // concrete resource name in output
    ResourceType string                  // e.g. "azure/web/site"
    Annotations  *core.MappingNode       // base annotations for the parent
    SeedSpec     *core.MappingNode       // default fields; contributions merge in
}

type EmitResult struct {
    Resources                 map[string]*schema.Resource
    DerivedValues             map[string]*schema.Value
    SharedParentContributions map[string]*core.MappingNode  // key matches SharedParent.Key
    Diagnostics               []*core.Diagnostic
}
```

Driver flow:

1. Aggregate decides which primaries belong to which shared parents and
   adds entries to `EmitPlan.SharedParents`. Primaries don't change shape
   — they don't know they're members.
2. Driver runs per-primary emits. Each may return contributions keyed by
   `SharedParent.Key`. Multiple primaries contributing to the same key →
   all contributions are collected.
3. After all per-primary emits complete, driver merges each shared parent's
   `SeedSpec` with its accumulated contributions (using the framework's
   existing mapping-node merge), wraps as a `*schema.Resource`, and writes
   it to the output blueprint alongside per-primary resources.
4. The shared parent's name is exposed via the rewriter chain so abstract
   refs like `${resources.someHandler.spec.functionAppName}` resolve to it
   via `ValueRef` or a `Patterns` rule.

Aggregator example (Azure):

```go
func aggregateAzureServerless(resolved []ResolvedResource) EmitPlan {
    var primaries []ResolvedResource
    var hasHandlers bool
    for _, r := range resolved {
        switch r.(type) {
        case *handlerconfig.ResolvedHandlerConfig, *consumer.ResolvedConsumer,
             *schedule.ResolvedSchedule, *vpc.ResolvedVPC:
            continue  // contributory-only, drop
        case *handler.ResolvedHandler:
            hasHandlers = true
            primaries = append(primaries, r)
        default:
            primaries = append(primaries, r)
        }
    }

    plan := EmitPlan{Primaries: primaries}
    if hasHandlers {
        plan.SharedParents = append(plan.SharedParents, SharedParent{
            Key:          "azure-serverless:functionApp:default",
            ResourceName: "celerity_function_app",
            ResourceType: "azure/web/site",
            SeedSpec:     defaultFunctionAppSpec(),
        })
    }
    return plan
}
```

Per-handler emit contribution:

```go
func EmitAzureServerless(
    r *ResolvedHandler,
    chained ResourcePropertyRewriter,
    ctx transform.Context,
) (*EmitResult, error) {
    funcName := r.Name + "_function"
    return &EmitResult{
        Resources: map[string]*schema.Resource{
            funcName: { /* azure/web/functions spec */ },
        },
        SharedParentContributions: map[string]*core.MappingNode{
            "azure-serverless:functionApp:default": {
                Fields: map[string]*core.MappingNode{
                    "siteConfig": {
                        Fields: map[string]*core.MappingNode{
                            "appSettings": appSettingsForHandler(r),
                        },
                    },
                },
            },
        },
    }, nil
}
```

**Why partial-fold instead of just full-fold:** under full-fold the
per-handler 1:1 mapping is lost — the spec construction can't easily express
"this handler's concrete code package goes to *this* function" because all
handlers are folded into one resolved struct. Partial-fold preserves the
1:1. Full-fold is still right when abstracts truly collapse to a single
concrete resource (one ECS task definition that contains *all* handlers as
processes, with no per-handler concrete resource). Both patterns coexist;
the framework supports either.

**Merge conflicts.** If two primaries contribute conflicting values to the
same shared-parent path (e.g. one says `runtime: node18`, another says
`runtime: node20`), the driver's merge step produces a diagnostic and aborts
that emit. Identical contributions from multiple primaries merge cleanly —
expected for fields like runtime stack that all sibling functions agree on.

## Pillar 3: Table-Driven Property Maps

A per-resource rewriter for one target is a `switch` with one arm per
supported abstract property. With six targets and a typical resource
exposing 15-30 abstract properties, that's 90-180 switch arms per resource
— most being trivial 1:1 renames that differ only in the right-hand side.

Replace the switch with a declarative table. Pattern-rule logic survives
only for the small minority of properties that don't fit the rename /
value-ref shape.

```go
// libs/plugin-framework/sdk/pluginutils/property_map.go

// PropertyMap is a declarative description of how an abstract resource's
// substitution-referenceable properties map to a target's concrete
// equivalents. The shape splits along path-shape lines:
//
//   - Renames and ValueRefs are keyed by *concrete* dotted paths. They
//     are O(1) map lookups, no wildcards, and pure data — Renames carries
//     no code at all, ValueRefs carries only a small enum (Suffix + Path).
//   - Patterns is the only place where path *patterns* (with wildcards
//     such as "spec.foo[*].bar") and arbitrary Rewrite functions are
//     allowed. Reviewers know to scrutinise this bucket; the other two
//     can be trusted as data.
type PropertyMap struct {
    // 1:1 renames. Key is a concrete dotted abstract path; value is the
    // concrete path segments. Array indices in the abstract path are
    // auto-preserved at their original positions (RewriteFields semantics).
    Renames map[string][]string

    // Abstract refs that should redirect to a transformer-derived value.
    // Key is a concrete dotted abstract path; value describes the value-ref.
    ValueRefs map[string]ValueRefSpec

    // Path-pattern rules for cases that don't fit Renames or ValueRefs:
    // structural reshapes, literal index injection, family-of-paths
    // rewrites. Evaluated in order; first match wins.
    Patterns []PropertyRule
}

type ValueRefSpec struct {
    // Suffix appended to the concrete resource name to form the value name.
    // e.g. "_arn" → ${values.<concreteName>_arn}.
    Suffix string
    // For complex derived values (e.g. {url, authType} objects), Path
    // descends into the value. Empty Path = flat ref.
    Path []SubstitutionPathItem
}

type PropertyRule struct {
    // MatchPaths is the path-pattern set this rule handles. Patterns may
    // contain "[*]" to match any array index (e.g.
    // "spec.environmentVariables[*].value"). MatchPaths is the single
    // source of truth for both runtime matching and capability extraction
    // — there is no separate opaque matcher to drift out of sync with.
    MatchPaths []string

    // Predicate is an optional further filter beyond the path match.
    // Most rules leave this nil. When non-nil, the rule applies only if
    // the substitution's path matches one of MatchPaths AND Predicate
    // returns true. Predicate-conditional matches are intentionally
    // outside the capability matrix's scope: the matrix is path-level
    // only, and Predicate-conditional behavior is implementation detail
    // that does not surface to end users.
    Predicate func(*substitutions.SubstitutionResourceProperty) bool

    Rewrite func(ref *substitutions.SubstitutionResourceProperty, ctx RewriteContext) *substitutions.Substitution
}

type RewriteContext struct {
    AbstractName string
    ConcreteName string
}

// Rewriter materialises the PropertyMap as a ResourcePropertyRewriter
// closure parameterised with the abstract↔concrete name pair from one
// resolved primary.
func (m PropertyMap) Rewriter(abstractName, concreteName string) ResourcePropertyRewriter
```

The queue example collapses to:

```go
// resources/queue/queue_aws_property_map.go

var awsPropertyMap = pluginutils.PropertyMap{
    Renames: map[string][]string{
        "spec.name":                     {"spec", "queueName"},
        "spec.fifoOrdering":              {"spec", "fifoQueue"},
        "spec.messageRetentionDays":      {"spec", "messageRetentionPeriod"},
        "spec.visibilityTimeoutSeconds":  {"spec", "visibilityTimeout"},
    },
    ValueRefs: map[string]pluginutils.ValueRefSpec{
        "spec.arn": {Suffix: "_arn"},
        "spec.url": {Suffix: "_url"},
    },
}

func AWSRewriterFactory(r *ResolvedQueue) []pluginutils.ResourcePropertyRewriter {
    return []pluginutils.ResourcePropertyRewriter{
        awsPropertyMap.Rewriter(r.Name, r.Name+"_sqs"),
    }
}
```

Six lines of map plus one factory function, per target. Compare with the
30-arm switch in the pipeline doc §3d.

The handler's harder cases (`spec.environmentVariables[i].value` →
`spec.environment.variables[i].value`, `spec.layer` → `spec.layers[0]`)
drop into `Patterns` — typically two or three rules, not the whole list.

## Pillar 4: Capability Matrix (Lightweight)

A capability matrix declares which abstract properties each (target,
resource-type) pair supports. It exists for two reasons:

1. **Pre-emit reference validation** — when a blueprint contains
   `${resources.handler.spec.functionUrl}` and the deploy target is
   `azure-serverless` (which has no equivalent), surface an actionable
   diagnostic before emit fails opaquely. This is *reference-level*
   validation only (does the abstract path exist on this target?); it
   does not extend to value validation (max counts, format constraints,
   etc.) — that's host-level pre-/post-transform validation, not the
   framework's job.
2. **Authoring guidance** — the third-party transformer-authoring guide
   needs a way to render "what does my target support?" without writers
   maintaining a separate doc.

The matrix is **derived, not hand-declared**:

```go
type Capabilities struct {
    // SupportedAbstractPaths is the set of dotted abstract property paths
    // this (target, resource-type) pair handles. Derived from a PropertyMap.
    SupportedAbstractPaths []string
}

// CapabilitiesFromPropertyMap derives SupportedAbstractPaths as the union
// of Renames keys, ValueRefs keys, and Patterns[i].MatchPaths.
func CapabilitiesFromPropertyMap(m PropertyMap) Capabilities
```

When `RegisterRewriter` is called with a `RewriterFactory` that wraps a
`PropertyMap`, the framework auto-derives `Capabilities` and stores them on
the registry. The transformer's `ValidateLinks` (or a new
`ValidateBlueprint`) phase walks the blueprint, finds every
`SubstitutionResourceProperty`, and checks the path is registered for the
(target, type) the resource will resolve to. Missing → diagnostic.

This stays lightweight: the matrix is a *side effect* of registration, not
a hand-maintained second list. Property maps are the source of truth.

**Audience boundary.** The capability matrix is intentionally minimal — a
flat path list — because it serves only one end-user-facing concern:
"does this target handle this abstract path?" Two adjacent concerns live
elsewhere by design:

- **End-user-facing conditional deploy semantics** (e.g. "this property
  applies only when a `celerity/vpc` link is also configured") are a
  property of the abstract resource itself and belong on the
  `AbstractResource` / `AbstractLink` definition, where they appear in
  user-facing docs uniformly across targets. A target that cannot honor
  the condition surfaces it as either an unsupported-path diagnostic
  (the path simply isn't in the matrix) or as a host-level pre-/post-
  transform validation failure — never as an inline transformer note.
- **Plugin-developer-facing implementation nuances** (e.g. "AWS handles
  `spec.environmentVariables[*].value` via a `Patterns` rule because the
  concrete path is reshaped") live in the third-party transformer-
  authoring guide as hand-written prose. That guide's audience is small
  and actively maintaining the plugin; auto-derivation buys little, and
  hand-written prose can convey nuance the registry can't.

Keeping the matrix to "supported paths, period" keeps the boundary clean:
end users never see plugin-internal detail, and plugin developers don't
get their guidance auto-derived from a structure that wasn't designed for
prose.

## Transformer Context: Configuration Sourcing

`transform.Context` is the channel for everything the transformer needs
that comes from outside the input blueprint: the deploy target, target-
specific configuration, and the small ambient pool of values the host CLI
injects per deployment. It carries two distinct value sources —
**transformer config variables** and **context variables** — and the
framework's contract specifies what each is for, where in the pipeline
it can be read from, and how plugin authors should think about which
one to reach for.

### The Two Sources

`transform.Context` exposes two accessor pairs (defined in the upstream
`transform.Context` interface in `bluelink/libs/blueprint/transform`):

```go
TransformerConfigVariable(name string)  (*core.ScalarValue, bool)
TransformerConfigVariables()             map[string]*core.ScalarValue

ContextVariable(name string)             (*core.ScalarValue, bool)
ContextVariables()                       map[string]*core.ScalarValue
```

- **Transformer config variables** are the static dotted-key entries the
  practitioner authors once in the host CLI's deploy configuration —
  for the Celerity CLI this is `app.deploy.jsonc` under
  `deployTarget.config` (the prefixed entries: `aws.lambda.memory`,
  `aws.dynamodb.<resourceName>.billingMode`, etc.). The host CLI converts
  these into `transformers.<transformer-id>.<key>` entries on the deploy
  engine's input; the framework surfaces them back to the plugin under
  the original key. Read via `ctx.TransformerConfigVariable`.
- **Context variables** are dynamic, per-deployment values the host CLI
  injects ambiently (`appEnv`, build-artifact pointers like
  `celerity.buildManifest`) plus anything the user explicitly authored
  under `contextVariables`. Read via `ctx.ContextVariable`.

The split is not arbitrary. Transformer config variables describe *what
to build*; context variables describe *what's true about this particular
deployment*. Plugin authors should respect the split because end users
do: prefixed config keys are version-controllable and per-environment
stable, context variables are typically computed or sensitive.

### Mapping from `app.deploy.jsonc` (Celerity CLI Worked Example)

The Celerity CLI's conversion (defined in
`celerity: apps/cli/internal/deployconfig/convert.go`) is the
canonical worked example of how a host CLI populates the two sources.
Plugin authors writing other Celerity-target plugins, or third-party
plugins that follow the same conventions, can rely on this mapping:

| `app.deploy.jsonc` location | Bluelink deploy-engine slot | Source on `transform.Context` | Plugin accessor |
|---|---|---|---|
| `deployTarget.name` | `transformers.celerity.deployTarget` | Transformer config variable | `ctx.TransformerConfigVariable("deployTarget")` |
| `deployTarget.appEnv` | `contextVariables.appEnv` (auto-injected) | Context variable | `ctx.ContextVariable("appEnv")` |
| `deployTarget.config` **prefixed** keys (`aws.*`, `gcloud.*`, `azure.*`) | `transformers.celerity.<dotted-key>` | Transformer config variable | `ctx.TransformerConfigVariable("<dotted-key>")` |
| `deployTarget.config` **non-prefixed** keys (`accessKeyId`, `region`, `profile`, …) | `providers.<provider>.*` | **Not visible to the transformer** | n/a — provider plugin only |
| `contextVariables.<key>` | `contextVariables.<key>` | Context variable | `ctx.ContextVariable("<key>")` |
| `blueprintVariables.<key>` | `blueprintVariables.<key>` | **Not on `transform.Context`** | n/a — resolved through substitutions before transform |

Two facts worth pinning down for plugin authors:

- **The prefix rule is the only sorter.** Provider authentication, region,
  and profile keys never reach the transformer; the host CLI routes them
  to the provider plugin. Plugin authors can rely on "every transformer-
  config-variable lookup is for a `<cloud>.<service>...` key the
  practitioner authored on purpose."
- **`appEnv` is auto-promoted.** The Celerity CLI injects
  `deployTarget.appEnv` as a context variable named `appEnv`; users do
  not write it under `contextVariables` themselves. This makes `appEnv`
  the one ambient value plugin authors can rely on in every Celerity
  deployment for default resolution.

Other host CLIs are free to populate `transform.Context` differently;
the framework treats both accessors as opaque key-value lookups. This
section describes the Celerity CLI convention, not a framework
requirement on hosts.

### Where in the Pipeline `transform.Context` Is Available

The framework hands `transform.Context` to a narrow set of lifecycle
points. It is **not** available to the registry-build phase or to any
of the declarative shapes:

| Phase / shape | Receives `transform.Context`? |
|---|---|
| `Resolver` | No — target-agnostic; sees only `(name, resource, linkGraph, blueprint)` |
| `Aggregator` | No — sees only `[]ResolvedResource` |
| `RewriterFactory` | No — sees only the resolved primary |
| `PropertyMap.Renames` / `PropertyMap.ValueRefs` | No — pure data |
| `PropertyMap.Patterns[i].Rewrite` | No — receives only `RewriteContext{AbstractName, ConcreteName}` |
| `Emitter` | **Yes** — `func(r PR, chained ResourcePropertyRewriter, ctx transform.Context)` |
| `AbstractResourceDefinition.CustomValidate` | Yes — pre-transform, for validity checks only |
| `RunTransformPipeline` entry (framework-internal) | Yes — reads `ctx.TransformerConfigVariable("deployTarget")` once via `getDeployTarget` to dispatch the aggregator/emitter set |

The exclusion is deliberate. The resolver, aggregator, and rewriter
chain are pure transformations of input shape; making them
ctx-aware would couple their outputs to deploy-time values, complicate
testing, and undermine the reviewability of `PropertyMap` as
declarative data. The emitter is where concrete spec values are
written into the output blueprint, and it is the one normal place
deploy-time configuration belongs.

When an abstract property's *value* depends on transformer config or a
context variable, the plugin author has two emit-side options:

1. **Bake the value into the emitted concrete spec directly.** The
   emitter reads `ctx.TransformerConfigVariable(...)` (or
   `ctx.ContextVariable(...)`), computes the value, and writes it into
   the `*schema.Resource` it returns. Right when the value is a static
   scalar consumed only by that resource.
2. **Expose it as a derived `${values.X}` and redirect via `ValueRefs`.**
   The emitter writes the computed value into
   `EmitResult.DerivedValues`; the abstract resource's `PropertyMap.ValueRefs`
   declares that the abstract path redirects to `${values.<concrete>_<suffix>}`.
   Right when multiple substitutions across the blueprint reference the
   value and a single point of truth at the values layer is preferable
   to repeated computation in the rewriter.

### Pattern: Resource-Keyed Config with Fallback

The Celerity resource-spec docs document two scopes for nearly every
provider-specific key — **global** (`aws.<service>.<key>`) and
**per-resource** (`aws.<service>.<resourceName>.<key>`). Per-resource
overrides global; global overrides any plugin-side default. This pattern
recurs across handlers, queues, topics, datastores, caches, sql
databases, and configs. It shows up in three concrete shapes — single
scalar, prefix-keyed sub-map, and indexed sub-object sequence — and the
framework SDK ships one helper per shape so the fallback chain is
expressed identically regardless of which the resource type uses:

```go
// transformutils/config.go

// Single scalar. Resource → service fallback.
//   "aws.dynamodb.ordersTable.billingMode" → "aws.dynamodb.billingMode"
func ResourceConfigVariable(
    ctx transform.Context,
    prefix, resourceName, key string,
) (*core.ScalarValue, error)

// Prefix-keyed sub-map. All entries under
// "<prefix>.<resourceName>.<key>.<*>" returned as a "<*>" → ScalarValue
// map. Falls back to "<prefix>.<key>.<*>". Sub-keys are single-segment;
// deeper-nested keys are ignored.
//   "aws.config.primaryConfig.regionKMSKeys.<region>"
//     → "aws.config.regionKMSKeys.<region>"
func ResourceConfigVariableMap(
    ctx transform.Context,
    prefix, resourceName, key string,
) (map[string]*core.ScalarValue, error)

// Indexed sub-object sequence. For each contiguous numeric index
// starting at 0, collects every "<prefix>.<resourceName>.<key>.<i>.<subkey>"
// into one map. Stops at the first index gap; sequences not starting
// at 0 are treated as absent. Falls back to "<prefix>.<key>.<i>.<subkey>".
//   "aws.sns.ordersTopic.statusLogging.<i>.<subkey>"
//     → "aws.sns.statusLogging.<i>.<subkey>"
func ResourceConfigVariableSeq(
    ctx transform.Context,
    prefix, resourceName, key string,
) ([]map[string]*core.ScalarValue, error)
```

All three return an error (not a bool) for the not-found case so the
miss is loggable at the call site; callers pattern-match on `err == nil`
when the value is optional and substitute a default otherwise. All
three apply the same "resource scope wins as a whole, otherwise fall
back to service scope as a whole" rule — there is no merging of
per-resource entries with service-scoped entries; whichever scope has
any entries is used in full.

Compute defaults often differ between `appEnv: development` and
`appEnv: production` (instance types, scaling caps, replica counts). The
canonical compose is the resource-keyed lookup feeding into an
appEnv-conditioned default:

```go
billingMode, err := transformutils.ResourceConfigVariable(
    ctx, "aws.dynamodb", r.Name, "billingMode")
if err != nil {
    billingMode = defaultBillingModeFor(transformutils.AppEnv(ctx))
    // e.g. dev → PAY_PER_REQUEST, prod → PROVISIONED
}
```

`transformutils.AppEnv(ctx)` is a thin typed accessor for the
`appEnv` context variable, returning `""` when unset.

Plugin authors writing emitters for resource types that follow the
global-plus-per-resource convention should reach for the appropriate
`ResourceConfigVariable*` helper rather than two raw
`TransformerConfigVariable` calls (or hand-rolled `TransformerConfigVariables()`
prefix scans); the helpers keep the fallback chain consistent across
resource types and make the precedence explicit at the call site.

### What Does Not Go Through `transform.Context`

- **Blueprint variables.** `blueprintVariables.<key>` resolves through
  the host's substitution-resolution phase before the transformer sees
  the input blueprint. Plugin authors do not — and cannot — read these
  via a ctx accessor; user-authored substitutions referencing them have
  already been resolved by the time emit runs.
- **Provider authentication, region, and profile.** Non-prefixed
  `deployTarget.config` keys go to the provider plugin and never appear
  on `transform.Context`. A transformer cannot read AWS credentials or
  the deployment region directly; if a transformer needs region-aware
  behavior, the practitioner authors a transformer-scoped key
  explicitly (e.g. `aws.someService.region`).
- **Build artifacts and host-managed state.** The host CLI may inject
  pointers to its own pre-command artifacts (e.g.
  `celerity.buildManifest`) as context variables when the transformer
  needs them. New context variables of this kind require coordinated
  host-CLI-plus-transformer work; the framework does not auto-discover
  them, and plugin authors should not invent ad-hoc context-variable
  conventions without aligning with the host.

### Anti-Patterns

- **Reading deploy-time configuration from the resolver or aggregator.**
  These run before any ctx-aware code and are deliberately ctx-free.
  If your aggregator's decision depends on configuration, encode the
  decision into the resolved primary's fields (which the resolver can
  set from blueprint inputs alone) and let the emitter read both. If
  the decision genuinely cannot be encoded shape-only, it belongs in
  the emit phase, not aggregate.
- **Stuffing data into `contextVariables` to avoid the prefix rule.**
  If a value is per-environment configuration the practitioner
  authors, it belongs under `deployTarget.config` with a proper
  `<cloud>.<service>...` prefix. The prefix rule is what lets plugin
  authors rely on "everything I read via `TransformerConfigVariable`
  is meant for me."
- **Reading ctx in `PropertyMap.Patterns[i].Rewrite`.** The pattern
  rewriter's `RewriteContext` deliberately carries only `AbstractName`
  and `ConcreteName`. If a substitution rewrite truly needs deploy-
  time configuration, that's a signal the value should be exposed as
  a derived `${values.X}` from the emitter (which has ctx) and the
  abstract ref redirected via a `ValueRefs` entry — see "Where in the
  Pipeline" above.

## Distribution Model

Celerity ships as **one binary**. Every cloud's `Register*` calls run at
startup, populating the `TransformerRegistry` for all six targets. The
deploy target is selected per-blueprint via the `deployTarget` transformer
config variable on `transform.Context` (see "Transformer Context:
Configuration Sourcing" above) and dispatched at runtime by the registry.
There are no slim per-cloud builds.

**Why not slim binaries.** The plugin host has no infrastructure for
selecting plugin binary distributions based on runtime config. Deploy target
is a per-deployment runtime value the practitioner is meant to be able to
change with low friction (changing the `deployTarget` config variable in
the deploy configuration, not reinstalling a transformer). A slim-binary
model would force binary swaps to follow target switches — a
discoverability and tooling cost the framework isn't built to absorb.
The marginal binary-size win is not worth the loss of low-friction
target switching.

**Per-cloud release cadence is preserved at the source level.** Each
cloud's subpackage (`internal/aws/`, `internal/gcloud/`, `internal/azure/`)
owns its own resource registrations and can iterate independently within
the Celerity repo. Tagged Celerity releases bundle whatever each cloud has
shipped at tag time. Cloud-specific bug-fix releases ship as Celerity
patch versions; only the affected cloud's code changes between two patch
versions, but the binary is unified.

**Abstract schemas live in `celerity-core`** (one definition, consumed by
every cloud's emit/rewriter code). Drift across clouds is impossible by
construction — a property only exists on the abstract resource if every
cloud either supports it via a property-map entry or explicitly declines
it (surfaces as a "not supported on target X" diagnostic via the capability
matrix in Pillar 4 above).

## When the Pipeline Toolkit Doesn't Fit

Not every transformer is shaped like Celerity. Layer 2 is opinionated for
the abstract-resource-expansion pattern: a blueprint comes in with abstract
resources, the transformer rewrites it into concrete provider resources
for a specific deploy target. That is the most common shape in the
Bluelink ecosystem and the one the toolkit is built for. Other shapes are
legitimate and supported via Layer 1 alone.

**Opting out is declarative.** Set `TransformerPluginDefinition.TransformFunc`
and leave the Layer 2 pipeline fields (`AbstractResources` pipeline fields,
`Aggregators`, `Registry`) unpopulated (see "Wiring" above). The framework
defers to your function entirely. Setting both `TransformFunc` and any
Layer 2 pipeline registrations panics — there's no partial-mode.

Examples of transformers that should opt out:

**Validation-only transformers.** A plugin that audits a blueprint and
emits diagnostics without changing the resource set (e.g. organizational
policy checks, naming conventions, security scanning). No emit. No
aggregate. No `EmitPlan`. Use Layer 1 to walk substitutions and produce
diagnostics; return the input blueprint unchanged. The framework's
`Diagnostics` channel in `SpecTransformerTransformOutput` carries the
results.

**Format-conversion transformers.** A plugin that converts a Bluelink
blueprint into a foreign format (Terraform HCL, CloudFormation YAML,
CDK code). The output isn't a transformed `*schema.Blueprint` at all —
it's a different artifact. These plugins shouldn't be transformers in
the framework sense; they should be a different plugin type or use the
host's blueprint-fetch API directly. If forced to live in the
transformer plugin slot, they use Layer 1 to read the blueprint and
return a blueprint with a sentinel marker pointing at the foreign artifact.
This is awkward; flag the use case to the framework team to design a
proper plugin type.

**Schema-migration transformers.** A plugin that upgrades a blueprint
from an older Bluelink schema version to a newer one. The resource set
may be identical post-migration, just with renamed fields or restructured
specs. Layer 1's `WalkMappingNode` and `WalkBlueprintRefs` are the right
tools; the pipeline shape doesn't apply (no aggregate, no per-target
emit). Register a rewriter visitor and walk the blueprint directly.

**Cross-blueprint composition transformers.** A plugin that merges or
synthesises resources from multiple input blueprints. Pipeline-toolkit
inputs are single-blueprint-shaped; multi-blueprint inputs need direct
host API access. Use Layer 1 substitution utilities for any rewriting;
write your own driver.

**The boundary heuristic.** If your transformer's job is "for each
abstract resource X, produce concrete resources Y on target Z," use
Layer 2. If it's anything else — pure validation, format conversion,
schema migration, multi-blueprint composition — use Layer 1.

**Mixed use.** A pipeline-toolkit transformer can also use Layer 1
primitives directly when it needs to (e.g. an extra blueprint-wide
substitution-walk pass for a cross-cutting concern). Layer 1 is always
available regardless of whether Layer 2 is in use.

## Reference: Celerity as the First User

The remaining sections describe how Celerity exercises this framework.
They are useful as a worked example for plugin authors but they are
**not part of the framework spec** — they describe one transformer's
usage, not requirements imposed on others.

### What the Framework Library Ships (Celerity-v0-Aligned Release)

Framework (in `libs/plugin-framework` — released as a library, then consumed
by Celerity v0):

- `sdk/pluginutils/registry.go` — `TransformerRegistry` +
  `RegisterResolver` / `RegisterAggregator` / `RegisterEmit` /
  `RegisterRewriter` (Pillar 1).
- `sdk/pluginutils/emit_plan.go` — `EmitPlan` with `SharedParents`,
  `EmitResult` with `SharedParentContributions` (Pillar 2).
- `sdk/pluginutils/property_map.go` — `PropertyMap` + `Rewriter`
  materialiser (Pillar 3).
- `sdk/pluginutils/capabilities.go` — derived matrix (Pillar 4).
- `sdk/pluginutils/run_transform_pipeline.go` — `RunTransformPipeline(blueprint,
  linkGraph, target, reg, ctx)`, the engine that orchestrates the
  11-step execution flow described in "Execution Pipeline". Named
  `Transform` rather than `Emit` because the pipeline owns far more than
  emit — it does target validation, resolve, aggregate, chain
  construction, blueprint-level rewriting, emit, shared-parent merging,
  value merging, transform-list pruning, and output assembly. The
  pipeline doc's current hand-written driver lifts into this function.
- `sdk/transformerv1/plugin_definition.go` (existing file) —
  `TransformerPluginDefinition` gains `Aggregators` and `Registry` fields.
  `Transform` method extended to call `RunTransformPipeline` when pipeline
  registrations are present and `TransformFunc` is nil. Existing
  pass-through default preserved when both are absent. Existing
  `ValidateLinks` / `AbstractResource` / metadata methods unchanged.
- `sdk/transformerv1/abstract_resource_definition.go` (existing file) —
  `AbstractResourceDefinition` gains `Resolve`, `PropertyMaps`, `Emitters`,
  and `Rewriters` fields. Framework auto-derives registry entries from
  these on first `Transform` call. Existing schema/examples/validate
  fields unchanged.

Celerity transformer (Celerity-v0):

- `aws-serverless` and `aws` target registrations using the framework
  above. **No hand-written dispatch switches in the transformer's driver.**
- Single unified binary distribution (no slim builds — see "Distribution
  Model"). Both targets compiled in; the `deployTarget` transformer
  config variable selects at runtime.

### What Celerity v1 Adds

Pure Celerity scope expansion — no framework changes:

- `gcloud-serverless` (Cloud Functions) — registrations + property maps + emits.
- `gcloud` — registrations + property maps + emits + a Cloud Run
  shared parent if relevant.
- `azure-serverless` (Azure Functions) — registrations + Function App
  shared parent.
- `azure` (Azure Container Apps) — registrations + Container Apps env
  shared parent.
- Same single binary; the four new targets are compiled in alongside the
  AWS pair and selected by the `deployTarget` config variable at runtime.

If Celerity v1 work surfaces a need for framework changes, that's a signal
v0 missed something — investigate before patching the framework, since other
downstream transformers will hit the same gap.

### Migration from Current Celerity v0 Pipeline Design

The pipeline doc (`resolve-emit-pipeline-design.md`) Part 2 mostly stands.
Concrete changes:

| Before (current pipeline doc) | After (this architecture) |
|---|---|
| Hand-written `resolveResource(resourceType)` switch | `reg.ResolverFor(resourceType)` (called by `RunTransformPipeline`) |
| Hand-written `aggregate(resolved, target)` switch | `reg.AggregatorFor(target)(resolved)` (called by `RunTransformPipeline`) |
| Hand-written `buildRewriters(plan, target)` switches | Loop calling `reg.RewriterFactoryFor(target, type)(p)` (in `RunTransformPipeline`) |
| Hand-written `emitResource(r, target, ...)` switches | `reg.EmitterFor(target, type)(p, chained, ctx)` (in `RunTransformPipeline`) |
| Per-resource `AWSServerlessRewriter` switch | `awsServerlessPropertyMap.Rewriter(name, concrete)` |
| `EmitPlan{Primaries}` flat | `EmitPlan{Primaries, SharedParents}` |
| `EmitResult{Resources, DerivedValues, Diagnostics}` | `+ SharedParentContributions` |
| `transformer/emit.go` driver (custom) | `pluginutils.RunTransformPipeline` (framework owns all 11 steps) |
| `transformBlueprint` function exists at all | Deleted. Framework's existing `(*TransformerPluginDefinition).Transform` method is extended to call `RunTransformPipeline` when pipeline fields are populated. |
| `NewTransformer()` body of ~30 LOC | `NewTransformer()` returns `&TransformerPluginDefinition{...}` with `AbstractResources` (each carrying `Resolve` / `Emitters` / `PropertyMaps`) and `Aggregators` populated. No driver code anywhere; no separate `Registry` construction needed for the common case. |

The pipeline doc's queue worked example becomes ~30% shorter (the rewriter
file collapses to a property map). A new "Registration" subsection shows
how `Register(reg)` is called from the transformer's `main`. After
migration, the pipeline doc is genuinely about Celerity's choices
(which abstract resources, which targets, which aggregators) — not about
plumbing.

## Resolved Design Decisions

The following questions came up during design and are now resolved. Recording
them here so the rationale isn't lost when implementation lands.

1. **Aggregator default behavior.** An unregistered target **errors**.
   The framework exports a `pluginutils.NoOpAggregator` constant that
   targets opt into explicitly when filter-only behavior is what they
   want. Silent fallback was rejected because it hides
   target-misconfiguration bugs.

2. **Duplicate registration.** `Register*` **panics on duplicate** (target,
   type) pairs. Replacement is implicit conflict that has to be made
   explicit — callers can `Unregister` first when they actually mean to
   override. Panic at registration time fails fast at startup rather than
   producing surprising behavior at emit time.

3. **Compound primary rewriter discovery.** **Manual.** A compound primary's
   `RewriterFactory` walks its contained children and returns the appropriate
   rewriters itself; the framework does not introspect compound shapes.
   Compound primaries are rare and target-specific; framework cleverness
   here would over-fit and force a child-tree contract on every primary.

4. **Per-target value validators.** **Out of scope for the framework.**
   Validation of emitted concrete resources is handled by the host's
   post-transform validation phase (which already runs against concrete
   resources from any source). Validation of abstract resources is handled
   by the host's pre-transform validation phase (which runs against the
   input blueprint before the transformer is invoked). The framework
   doesn't need a separate hook between these — anything a transformer
   would catch internally is either already caught by host validation or
   is target-emit-internal and lives inside the relevant `EmitX` function.

5. **Unsupported-target error UX.** **Generic "target not supported"
   error from the registry**, returned at the driver entry point before any
   resolve / aggregate / emit work begins.
   Note: deploy target comes from **transformer config or context
   variables**, not from blueprint metadata. Bluelink CLIs are expected to
   surface unsupported-target errors before invoking the transformer at
   all, but in decoupled environments where the CLI hand-off can't catch
   it, the plugin reports it cleanly. Implementation: `RunTransformPipeline`'s
   first check is `reg.AggregatorFor(target) == nil` →
   `fmt.Errorf("transformer does not support deploy target %q", target)`.
   No capability-matrix or per-resource diagnostics involved; this is
   strictly a registration-presence check.

6. **Threading run-scoped state into phases.** **Explicit `*Run`
   parameter on every phase, populated by an optional `OnRun` hook,
   accessed via typed `Provide` / `Use` generics.** Phases that need
   run-scoped state (e.g. a loaded build manifest whose path lives in
   `transform.Context`) receive it via `transformutils.Use[T](run)` after
   `OnRun` has stashed it with `transformutils.Provide(run, value)`. The
   phase graph is built **once** at plugin load time and never rebuilt;
   only `*Run` is allocated per pipeline call. Rejected alternatives:
   stuffing values into `context.Context` (the official `context` docs
   scope `Value` to data that transits processes and APIs, not
   parameters consumers read consciously — and access would be ambient
   and untyped); leaving phase signatures narrow and having every phase
   that needs the manifest load it independently (works for one phase,
   duplicates I/O the moment a second phase needs the same state); a
   per-run *bindings* overlay where `OnRun` returns fresh phase
   functions (cleaner in some ways but rebuilds the phase graph for
   every pipeline call and forces the framework to carry two parallel
   registration paths); and parameterising every framework type over a
   plugin-defined state type `S` (heavy generics on every registry and
   phase type, mixes badly when a host process loads multiple plugins
   with different `S`). `*Run` keeps the graph static, the framework
   monomorphic, the data flow explicit at every consumer signature, and
   confines generics to two accessor functions. Implementation: see
   "Per-Run State" under "Wiring."

## Verification Sketch

- `pluginutils.TransformerRegistry` unit tests:
  registration, lookup, duplicate-registration panic, generic-helper type safety.
- `pluginutils.PropertyMap` unit tests: rename, value-ref, pattern rule
  (with and without Predicate); array-index preservation; unknown-path
  returns nil.
- `pluginutils.RunTransformPipeline` integration tests with a fake registry:
  filter aggregate, full-fold aggregate, partial-fold (shared parents),
  contribution merge conflict diagnostic.
- Celerity end-to-end tests for `aws-serverless` and `aws` go through
  the framework; no hand-written dispatch is exercised.
- A "target not registered" test: a Celerity v0 binary (AWS-only
  registrations) invoked with the `deployTarget` config variable set to
  `gcloud` produces a clean "no aggregator registered for target gcloud"
  error at the driver entry point, not a runtime nil dereference.
- `*Run` / `OnRun` / `Provide` / `Use` tests:
  - `Provide` then `Use[T]` round-trips a value within one run; `Use[T]`
    on an unset key returns `(zero, false)`; `MustUse[T]` panics.
  - Two values with the same Go type collide; newtype wrappers
    disambiguate.
  - An `OnRun` returning a non-nil error aborts the pipeline before any
    phase fires (verify by registering an aggregator that panics if
    invoked).
  - Concurrent `RunTransformPipeline` calls receive independent `*Run`
    instances; a `Provide` in one run is not visible from a `Use` in
    the other.
  - `Provide` during a phase (not just during `OnRun`) is visible to
    later phases in the same run, under the documented mutex.
