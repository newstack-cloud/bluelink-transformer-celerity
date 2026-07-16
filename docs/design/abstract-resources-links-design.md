# Abstract Resource Link Validation & References — Design

## Context

The Celerity transformer plugin works with abstract resources (e.g. `celerity/handler`, `celerity/api`) that users link together in blueprints. At deploy time these are transformed into concrete provider resources. Today the core bluelink blueprint framework does **not** validate links involving abstract resources before transformation, and offers no way to visualize the declared link graph pre-transform. This document captures:

- What the framework actually does and doesn't do with abstract links and references today.
- Where responsibility for abstract-link validation, visualization, and reference handling should land.
- A short-term path that works with zero framework changes, and a longer-term path that adds two small framework hooks.
- The shape of a future SDK adapter for declarative link rules — deliberately deferred until hand-written validators exist in the transformer.

The goal is a model in which the framework owns what it can own cheaply (sub-ref validation, plumbing diagnostics) and the transformer owns what only it can know (link semantics, output mapping), with a clean declarative surface emerging later once patterns are visible in real code.

---

## Established facts about `libs/blueprint`

### Pipeline order

Parse → validate resources (incl. abstract via `CustomValidate`) → **transform** → build `LinkInfoProvider` (post-transform, concrete only) → `ValidateLinkAnnotations` (concrete only).

- `libs/blueprint/container/loader.go:888-1018` — overall pipeline.
- `libs/blueprint/container/loader.go:651-735` — `loadSpecAndLinkInfo`, which builds `LinkInfoProvider` at line 663, **after** transformation completes at line 985.

### Abstract vs. concrete distinction

Abstract resources are structural — they come from `transform.SpecTransformer.AbstractResource()`, not `provider.Provider`. There is no `IsAbstract()` flag.

- `libs/blueprint/transform/transform.go:23,45-88` — `SpecTransformer` and `AbstractResource` interfaces.
- `libs/blueprint/provider/resource.go:17-80` — `Resource` interface (concrete).

### Link validation gap

The core framework **never calls `AbstractResource.CanLinkTo`**. Link validation (`checkCanLinkTo`) only looks up resource types in the concrete `resourceProviders` map. Abstract resources that weren't registered as concrete return `canLinkTo=false` silently, with no error. Since `LinkInfoProvider` is only built post-transform, by that point all abstract resources have been replaced and the lookup succeeds on the transformed graph — but the original abstract-to-abstract edges were never validated.

- `libs/blueprint/links/speclinkinfo.go:319-375` — `checkCanLinkTo`.
- `libs/blueprint/transform/transform.go:64` — `AbstractResource.CanLinkTo`, defined but uninvoked.

### What the framework does invoke on abstract resources pre-transform

`AbstractResource.CustomValidate` runs pre-transform via `resourcehelpers/registry.go:389`. **It receives only the single `schema.Resource` being validated plus a `TransformerContext` (config/context variables).** It does **not** receive the parent `schema.Blueprint`, so a validator cannot see other resources' link selectors or metadata labels from inside `CustomValidate`.

- `libs/blueprint/transform/transform.go:105-108` — `AbstractResourceValidateInput` struct.
- `libs/blueprint/resourcehelpers/registry.go:376-405` — invocation site.

### Substitution references

bluelink uses **`.spec.*` only** for resource references — there is no `.state` syntax. Computed/output fields are marked by a `Computed: true` flag on individual schema attributes, not by a separate schema tree.

- `libs/blueprint/substitutions/ref_patterns.go:13-84` — reference regex pattern.
- `libs/blueprint/provider/resource.go:461-664` — `ResourceSpecDefinition` / `ResourceDefinitionsSchema` with `Computed` flag at line 627.

Critically: **substitution-ref validation already works for abstract resources, pre-transform, for free.** The validation path at `libs/blueprint/validation/substitution_validation.go:266-489` calls `resourceRegistry.GetSpecDefinition`, which transparently dispatches to either `provider.Resource` or `transform.AbstractResource` via `resourcehelpers/registry.go:235-267`. There is no abstract/concrete branch. If an abstract resource declares its computed fields honestly in `GetSpecDefinition`, then `${resources.someHandler.spec.arn}` is type-checked against that schema at load time.

### Link annotation definitions

Concrete `LinkImplementation`s declare annotation schemas via `LinkAnnotationDefinition` (`libs/blueprint/provider/link.go:344-371`). Ten fields — nine plain data (`Name`, `Label`, `Type`, `Description`, `DefaultValue`, `AllowedValues`, `Examples`, `Required`, `AppliesTo`), plus one **optional** `ValidateFunc func(...) []*core.Diagnostic`. The function field is nil-checked at the call site (`libs/blueprint/validation/link_annotation_validation.go:248-254`), so definitions constructed in Go code with `ValidateFunc: nil` work identically to provider-supplied ones.

The framework's type and allowed-values validators (`validation/link_annotation_validation.go:261-391`) are **self-contained** — they operate purely on the `LinkAnnotationDefinition` struct and don't need `LinkImplementation` context. This means the same type can be reused as-is by the transformer for abstract-link annotation rules, with no parallel definition type needed.

### Declared-link enumeration does not exist

`SpecLinkInfo` (`libs/blueprint/links/speclinkinfo.go:20-31`) exposes only `Links(ctx)` and `Warnings(ctx)`, both operating on the post-transform graph. There is no `DeclaredLinks()` accessor, no pre-transform constructor, no DOT/graph export. `refgraph/` is about reference-cycle detection, not the link graph.

### Existing SDK templates in `plugin-framework/sdk`

The bluelink plugin framework SDK (at `~/projects2025/bluelink/libs/plugin-framework/sdk/`) already provides templates that the new design should slot into rather than replace:

- **`transformerv1/plugin_definition.go` — `TransformerPluginDefinition`.** Top-level transformer template, implements `transform.SpecTransformer`. Holds `AbstractResources map[string]transform.AbstractResource` and a `TransformFunc`. The registration point for abstract link definitions will live here (new field).
- **`transformerv1/abstract_resource_definition.go` — `AbstractResourceDefinition`.** Per-resource template. Notable: its `CanLinkTo` method (lines 122-131) returns an empty list with the comment *"The host-side wrapper derives this from registered links. This method exists only to satisfy the transform.AbstractResource interface."* This strongly suggests the plugin framework host *already* has or intends to have a concept of link registration for abstract resources, even though the core framework doesn't currently invoke `AbstractResource.CanLinkTo` during validation. Verify during implementation whether the host-side registration surface actually exists — if so, slot into it rather than run parallel.
- **`providerv1/link_definition.go` — `LinkDefinition`.** Concrete link template. Organized per edge class by `ResourceTypeA` × `ResourceTypeB`. Carries `Kind` (hard/soft), descriptive fields, `AnnotationDefinitions map[string]*provider.LinkAnnotationDefinition` keyed as `{resourceType}::{annotationName}`, `PriorityResource`, and execution hooks (`StageChangesFunc`, `UpdateResourceAFunc`, etc.). The abstract side should mirror this shape minus the execution hooks.
- **`sdk/validation/`** — a package of reusable validation helpers (strings, int, float, url, network, combine, conflicts, etc.). Good precedent for where annotation-value validators could live if extracted.

---

## Division of responsibility

| Concern | Owner | Mechanism |
|---|---|---|
| Abstract resource spec validation | Framework | Existing `GetSpecDefinition` + schema validation |
| Sub-ref path validation (inc. computed fields) | Framework | Existing substitution validator (**works today**) |
| Abstract link type compatibility, cardinality, annotations, custom validation | Transformer (via SDK) | New framework `ValidateLinks` hook on `SpecTransformer`; SDK provides `AbstractLinkDefinition` + default implementation that walks registered definitions |
| Sub-ref rewriting across transform | Transformer | Walks substitutions during `Transform()` |
| Composed output bridging | Transformer | Emits `values` section entries during `Transform()` |
| Annotation propagation abstract → concrete | Transformer | Name-matching + explicit projection for value transforms |
| Concrete link cardinality, custom validation | Framework + Provider (via SDK) | New `ValidateLinkConstraints` in post-transform pipeline; `provider.Link` interface gains `GetCardinality`, `ValidateLink`; SDK `LinkDefinition` provides default implementations |
| Pre-transform link visualization | Framework | New declared-link enumerator, shared with `ValidateLinks` call path |

---

## Boundary rule: no formal AR↔CR links

Celerity transformer convention: **abstract resources only have formal links to other abstract resources within the same transformer plugin.** Crossing the abstract/concrete boundary is done via substitution references (`${resources.foo.spec.x}`), which create dependencies in the ref graph rather than links in the link graph.

Rationale:
- Avoids the exploding complexity of validating mixed AR/CR link edges, which would otherwise require the transformer to know every concrete resource type it might legitimately link to.
- Matches how traditional IaC frameworks handle cross-layer references (Terraform: you don't "link" one resource to another, you reference an attribute and the dependency graph follows).
- Fits the existing framework mechanics — substitution refs are already validated pre-transform (see above) and already drive `refgraph` ordering.

The transformer's link validator can enforce this as a hard rule: if an outbound `linkSelector` resolves to a concrete resource, emit a diagnostic telling the user to use a substitution reference instead.

---

## Required framework additions (`libs/blueprint`)

Five cohesive additions. All five are prerequisites for the full design — not optional.

### 1. Declared-link enumerator

A pure schema-walk helper that resolves `linkSelector.byLabel` against resource metadata labels across the full `schema.Blueprint`, without requiring `resourceProviders` to be populated. Returns a typed link graph — nodes are resources tagged as abstract or concrete (based on which registry their type lives in), edges are resolved `(sourceName, targetName, linkSelectorKey, annotations)` tuples.

The enumerator **must reuse** the existing label-match primitives at `libs/blueprint/links/utils.go:38-61` (`GroupResourcesBySelector`) and `:149-156` (`extractSelectorLabelsForGrouping`). These are pure schema walks with no registry coupling and already implement exactly the `linkSelector.byLabel` × `metadata.labels` matching the enumerator needs. `DeclaredLinkGraph` is a different *view* of the same underlying match — flat edge list vs. post-transform chain tree — and the shared primitive is the guarantee that both views stay in lockstep as `LinkSelector` evolves.

`SpecLinkInfo` itself is not reusable for this: its walker (`checkCanLinkTo`, `links/speclinkinfo.go:317-373`) interleaves label matching with concrete-provider lookups via `resourceProviders` and `linkRegistry.Link()`, and its output shape is `[]*ChainLinkNode` rather than a flat edge list. The two accessors coexist as sibling views, not competitors.

Shared machinery for three consumers:
- The new `ValidateLinks` hook below, which takes the graph as input.
- Pre-transform visualization: a CLI `graph` / `inspect` command or LSP hover rendering.
- The `AbstractResourceDefinition.CanLinkTo` host-wrapper derivation (per the comment in the SDK source; see below).

The enumerator is read-only and has no runtime-state coupling. Exact type shape to be decided during implementation, but the minimum API is:

```
type DeclaredLinkGraph interface {
    Edges() []ResolvedLink
    EdgesFrom(resourceName string) []ResolvedLink
    EdgesTo(resourceName string) []ResolvedLink
    Resource(name string) (schema.Resource, ResourceClass, bool) // class = abstract | concrete
}

type ResolvedLink struct {
    Source, Target   string
    SourceType, TargetType string
    SelectorKey      string // the label key that matched
    Annotations      map[string]*core.ScalarValue // user-declared annotations on the link
}

func EnumerateDeclaredLinks(spec *schema.Blueprint, resourceRegistry resourcehelpers.Registry) DeclaredLinkGraph
```

The `resourcehelpers.Registry` argument is used **only** for node classification (abstract vs concrete) — label matching itself is a pure schema walk. Today the public `Registry` interface (`resourcehelpers/registry.go:22-102`) conflates the two origins behind a single `HasResourceType` that ORs `hasProviderResourceType` and `hasAbstractResourceType` (`registry.go:336-374`). The enumerator needs the split, so this addition also requires exposing a new method — `IsAbstractResourceType(ctx, resourceType) (bool, error)` — that surfaces the existing unexported `hasAbstractResourceType` helper. This is the minimum framework change to answer "which registry does this type live in" without re-walking providers and transformers in enumerator code.

#### Directionality convention

Link edges in the declared graph are **oriented by selector ownership**, not by any canonicalization of resource types. `collectDeclaredLinks` (`libs/blueprint/links/declared_link_graph.go:121-139`) sets `ResolvedLink.Source` from `SelectGroup.SelectorResources` (the resource whose `linkSelector.byLabel` produced the match) and `ResolvedLink.Target` from `CandidateResourcesForSelection` (the resource whose `metadata.labels` matched). An edge therefore reads as *"Source declared intent to link to Target"*. This matters because `core.LinkType` (`libs/blueprint/core/utils.go:437-446`) is a plain `"%s::%s"` format — it is **order-preserving, not canonical** — so any lookup keyed on it is direction-sensitive.

The following rules apply and MUST be enforced by the SDK adapter, not left as author discretion:

1. **`AbstractLinkDefinition.ResourceTypeA` is always the selector-owner side**, `ResourceTypeB` is always the selected-target side. The SDK `ValidateLinks` implementation performs `AbstractLinks[core.LinkType(edge.SourceType, edge.TargetType)]`, which only resolves when the registered orientation matches the physical edge direction. Registering `{A: celerity/api, B: celerity/handler}` for a link where handler owns the selector is not equivalent to `{A: celerity/handler, B: celerity/api}` — the former will silently miss every edge. The SDK should validate this at plugin-init time by surfacing "no `AbstractLinkDefinition` found for edge class `X::Y`" as an author-facing error rather than a user-facing diagnostic, since it is a plugin bug, not a blueprint bug.

2. **`CardinalityA` counts outbound-from-A; `CardinalityB` counts inbound-to-B.** Because direction is pinned to selector ownership, these read naturally: `CardinalityA` is "how many distinct B's each A's selector may match," computed via `LinkGraph.EdgesFrom(aInstance)` filtered to `TargetType == B`. `CardinalityB` is "how many distinct A's may select a given B," computed via `LinkGraph.EdgesTo(bInstance)` filtered to `SourceType == A`. Note that `EdgesFrom` and `EdgesTo` are themselves asymmetric in the same sense — `EdgesFrom(x)` returns edges where `x` owns a selector, not edges where `x` happens to appear first alphabetically. Cardinality pass implementations must not conflate the two.

3. **Reciprocal selectors are two distinct edge classes.** If `celerity/handler` declares a `linkSelector` matching `celerity/api` labels *and* `celerity/api` declares a `linkSelector` matching `celerity/handler` labels, the enumerator emits two edges: one `handler → api` and one `api → handler`. Each requires its own `AbstractLinkDefinition` keyed `handler::api` and `api::handler` respectively, with independent cardinality and annotation schemas. The SDK must not attempt to merge these or treat one as the "canonical" direction — doing so would lose information, since the two edges may carry different user-declared annotations and may legitimately have different semantics (e.g. `handler → api` = "handler serves this api", `api → handler` = "api's default-handler fallback"). Transformer authors who want to forbid reciprocal linking must do so explicitly via a `ValidateFunc` escape hatch, or by registering only one of the two edge classes and letting the other fall through to the "no such edge class" diagnostic.

4. **Cross-boundary diagnostics are not symmetric.** §SDK.1a ("abstract source must not link to concrete") is actually two cases with different fixes:
   - **Abstract → Concrete** (abstract resource owns a `linkSelector` matching a concrete resource's labels): diagnostic should tell the user *"abstract resources cannot declare formal links to concrete resources; use a substitution reference `${resources.<name>.spec.<field>}` from inside the abstract resource's own spec instead."*
   - **Concrete → Abstract** (concrete resource owns a `linkSelector` matching an abstract resource's labels): diagnostic should tell the user *"concrete resources cannot link to abstract resources because the abstract resource is replaced during transformation; target the concrete resource(s) it expands into instead, or reference its outputs via `${resources.<name>.spec.<field>}`."*
   The cross-boundary check walks every edge in the declared graph and classifies each endpoint via `LinkGraph.Resource(name)`. Both orientations fire; neither is treated as "the right way round."

A consequence of rule 3 worth flagging for the visualization story: a reciprocal pair should render in the CLI `graph` / `inspect` output as two arrows, not one double-headed arrow, because the annotations on each direction are independent.

### 2. `SpecTransformer.ValidateLinks` hook

New method on the existing `transform.SpecTransformer` interface in `libs/blueprint/transform/transform.go`:

```
ValidateLinks(ctx context.Context, input *SpecTransformerValidateLinksInput) (*SpecTransformerValidateLinksOutput, error)

type SpecTransformerValidateLinksInput struct {
    Blueprint   *schema.Blueprint
    LinkGraph   DeclaredLinkGraph
    Params      core.BlueprintParams
}

type SpecTransformerValidateLinksOutput struct {
    Diagnostics []*core.Diagnostic
}
```

Called once per blueprint during the pre-transform validation pass in `container/loader.go` (adjacent to the existing `CustomValidate` invocation sites). Returns diagnostics that flow through the framework's existing diagnostic collection and out to CLI / LSP.

The hook is on `SpecTransformer`, not per-`AbstractResource`, because:
- Link rules are fundamentally per-edge-class (a pair of types), not per-resource.
- Holistic rules (cardinality, presence invariants) need the whole graph, not one resource's view.
- One registration point, one validation entry point — no scattering across resource definitions.

`TransformerPluginDefinition` in the SDK gains a default implementation that walks its registered `AbstractLinkDefinition`s and the declared link graph; transformer authors who use the SDK template get validation for free once they register their link definitions.

### 3. `AbstractLink` interface and `SpecTransformer.AbstractLink` accessor

The `transform` package already defines `AbstractResource` as a first-class interface with documentation-relevant methods (`GetSpecDefinition`, `GetTypeDescription`, `GetExamples`, etc.) and `SpecTransformer.AbstractResource()` as the accessor. Concrete links have the same surface via `provider.Link` (`GetTypeDescription`, `GetAnnotationDefinitions`, `GetKind`, `GetCardinality`). Abstract links have neither — their metadata lives exclusively on `transformerv1.AbstractLinkDefinition`, a concrete SDK struct invisible to anything operating through `SpecTransformer`.

This means any consumer of `SpecTransformer` — plugin-docgen, LSP hover, CLI `inspect`, the gRPC host wrapper — can call `ListAbstractLinkTypes()` to get a list of link type strings but has **no interface-level way to retrieve descriptions, annotation schemas, cardinality, or any other metadata** for those links. The only escape is a type assertion down to `TransformerPluginDefinition`, which breaks the interface abstraction and doesn't work across the gRPC boundary at all.

#### New interface in `libs/blueprint/transform/transform.go`

```go
// AbstractLink is the interface for an abstract link between two abstract
// resource types that a spec transformer can contain. It exposes
// documentation-relevant metadata and validation constraints through
// the same pattern as AbstractResource — an interface in the transform
// package, implemented by the SDK's AbstractLinkDefinition.
type AbstractLink interface {
    // GetType retrieves the ordered resource type pair for this link,
    // expressed as the "{ResourceTypeA}::{ResourceTypeB}" link type string
    // plus the individual types for each side.
    GetType(
        ctx context.Context,
        input *AbstractLinkGetTypeInput,
    ) (*AbstractLinkGetTypeOutput, error)

    // GetTypeDescription retrieves a human-readable description of this
    // link type for documentation and tooling.
    // Markdown and plain text formats are supported.
    GetTypeDescription(
        ctx context.Context,
        input *AbstractLinkGetTypeDescriptionInput,
    ) (*AbstractLinkGetTypeDescriptionOutput, error)

    // GetAnnotationDefinitions retrieves the annotation schema for this
    // link type. Keyed by "{resourceType}::{annotationName}", same
    // convention as provider.Link.GetAnnotationDefinitions.
    GetAnnotationDefinitions(
        ctx context.Context,
        input *AbstractLinkGetAnnotationDefinitionsInput,
    ) (*AbstractLinkGetAnnotationDefinitionsOutput, error)

    // GetCardinality retrieves the cardinality constraints for both sides
    // of the link relationship. Zero values mean no constraint.
    GetCardinality(
        ctx context.Context,
        input *AbstractLinkGetCardinalityInput,
    ) (*AbstractLinkGetCardinalityOutput, error)
}
```

#### Input/output types

```go
type AbstractLinkGetTypeInput struct {
    TransformerContext Context
}

type AbstractLinkGetTypeOutput struct {
    // The full link type string, e.g. "celerity/handler::celerity/api".
    Type string
    // The type of the source (selector-owner) abstract resource.
    ResourceTypeA string
    // The type of the target (selected) abstract resource.
    ResourceTypeB string
}

type AbstractLinkGetTypeDescriptionInput struct {
    TransformerContext Context
}

type AbstractLinkGetTypeDescriptionOutput struct {
    MarkdownDescription  string
    PlainTextDescription string
    MarkdownSummary      string
    PlainTextSummary     string
}

type AbstractLinkGetAnnotationDefinitionsInput struct {
    TransformerContext Context
}

type AbstractLinkGetAnnotationDefinitionsOutput struct {
    AnnotationDefinitions map[string]*provider.LinkAnnotationDefinition
}

type AbstractLinkGetCardinalityInput struct {
    TransformerContext Context
}

type AbstractLinkGetCardinalityOutput struct {
    CardinalityA provider.LinkCardinality
    CardinalityB provider.LinkCardinality
}
```

#### New method on `SpecTransformer`

```go
type SpecTransformer interface {
    // ...existing methods...

    // AbstractLink returns the abstract link implementation for a given
    // link type string ("{ResourceTypeA}::{ResourceTypeB}").
    // This is the link analogue of AbstractResource — it provides access
    // to link metadata through the interface for documentation generation,
    // tooling, and the gRPC host wrapper.
    AbstractLink(ctx context.Context, linkType string) (AbstractLink, error)
}
```

This completes the symmetry:

| Concern | Resource | Link |
|---|---|---|
| Interface | `transform.AbstractResource` | `transform.AbstractLink` |
| Accessor | `SpecTransformer.AbstractResource(ctx, type)` | `SpecTransformer.AbstractLink(ctx, type)` |
| List | `SpecTransformer.ListAbstractResourceTypes(ctx)` | `SpecTransformer.ListAbstractLinkTypes(ctx)` |
| SDK impl | `transformerv1.AbstractResourceDefinition` | `transformerv1.AbstractLinkDefinition` |

#### SDK implementation

`AbstractLinkDefinition` (already described in §SDK addition) gains method implementations for the `transform.AbstractLink` interface. These are trivial projections of the struct's fields:

```go
func (d *AbstractLinkDefinition) GetType(
    ctx context.Context,
    input *transform.AbstractLinkGetTypeInput,
) (*transform.AbstractLinkGetTypeOutput, error) {
    return &transform.AbstractLinkGetTypeOutput{
        Type:          core.LinkType(d.ResourceTypeA, d.ResourceTypeB),
        ResourceTypeA: d.ResourceTypeA,
        ResourceTypeB: d.ResourceTypeB,
    }, nil
}

func (d *AbstractLinkDefinition) GetTypeDescription(
    ctx context.Context,
    input *transform.AbstractLinkGetTypeDescriptionInput,
) (*transform.AbstractLinkGetTypeDescriptionOutput, error) {
    return &transform.AbstractLinkGetTypeDescriptionOutput{
        MarkdownDescription:  d.FormattedDescription,
        PlainTextDescription: d.PlainTextDescription,
        MarkdownSummary:      d.FormattedSummary,
        PlainTextSummary:     d.PlainTextSummary,
    }, nil
}

func (d *AbstractLinkDefinition) GetAnnotationDefinitions(
    ctx context.Context,
    input *transform.AbstractLinkGetAnnotationDefinitionsInput,
) (*transform.AbstractLinkGetAnnotationDefinitionsOutput, error) {
    return &transform.AbstractLinkGetAnnotationDefinitionsOutput{
        AnnotationDefinitions: d.AnnotationDefinitions,
    }, nil
}

func (d *AbstractLinkDefinition) GetCardinality(
    ctx context.Context,
    input *transform.AbstractLinkGetCardinalityInput,
) (*transform.AbstractLinkGetCardinalityOutput, error) {
    return &transform.AbstractLinkGetCardinalityOutput{
        CardinalityA: d.CardinalityA,
        CardinalityB: d.CardinalityB,
    }, nil
}
```

#### `TransformerPluginDefinition.AbstractLink` dispatch

The existing `TransformerPluginDefinition` gains the accessor method that dispatches into its `AbstractLinks` map:

```go
func (p *TransformerPluginDefinition) AbstractLink(
    ctx context.Context,
    linkType string,
) (transform.AbstractLink, error) {
    def, ok := p.AbstractLinks[linkType]
    if !ok {
        return nil, errAbstractLinkNotFound(linkType, p.TransformName)
    }
    return def, nil
}
```

#### gRPC wire additions

The `Transformer` gRPC service at `plugin-framework/transformerserverv1/transformer.proto` needs RPCs to surface the `AbstractLink` interface across the process boundary:

```protobuf
rpc GetAbstractLinkType(GetAbstractLinkTypeRequest) returns (GetAbstractLinkTypeResponse) {}
rpc GetAbstractLinkTypeDescription(GetAbstractLinkTypeDescriptionRequest) returns (GetAbstractLinkTypeDescriptionResponse) {}
rpc GetAbstractLinkAnnotationDefinitions(GetAbstractLinkAnnotationDefinitionsRequest) returns (GetAbstractLinkAnnotationDefinitionsResponse) {}
rpc GetAbstractLinkCardinality(GetAbstractLinkCardinalityRequest) returns (GetAbstractLinkCardinalityResponse) {}
```

The host-side client wrapper constructs a `transform.AbstractLink` adapter from these RPCs, following the same pattern as `abstract_resource_client_wrapper.go`. For backward compatibility, `Unimplemented` responses are treated as "link not found" — older plugins that don't implement these RPCs degrade to the existing `ListAbstractLinkTypes`-only behavior.

#### Why this is required for plugin-docgen parity

The plugin-docgen tool in `~/projects2025/bluelink` generates documentation for provider plugins by iterating over types and calling interface methods (`GetTypeDescription`, `GetAnnotationDefinitions`, etc.) through the plugin's gRPC boundary. For transformer link documentation to work through the same pipeline:

1. The docgen tool must be able to call `ListAbstractLinkTypes()` to enumerate link types.
2. For each link type, it must be able to call `AbstractLink(linkType)` to get a handle.
3. Through that handle, it must be able to call `GetTypeDescription()`, `GetAnnotationDefinitions()`, and `GetCardinality()` — the documentation-relevant surface for abstract links.

Without the `AbstractLink` interface, the docgen tool would have to special-case transformer plugins and reach into SDK internals, which defeats the point of the interface abstraction and doesn't work for out-of-process plugins at all.

### 4. Exported annotation-value validator

A small exported helper in `libs/blueprint/validation/`:

```
func ValidateAnnotationValue(
    def *provider.LinkAnnotationDefinition,
    value *core.ScalarValue,
) []*core.Diagnostic
```

so the SDK adapter can delegate to the framework's existing type / allowed-values logic at `libs/blueprint/validation/link_annotation_validation.go:261-391` rather than reimplementing it. The function is a thin wrapper over the already-existing internal validators. Keeps annotation validation behaviourally identical between concrete and abstract links.

### 5. Post-transform concrete link constraint validation

A new `ValidateLinkConstraints` function in `libs/blueprint/validation/` that validates cardinality and custom validation for concrete links. This is the concrete-link counterpart to the `ValidateLinks` hook on `SpecTransformer` (§2 above), but runs **post-transform** because concrete links are only materialized after transformation.

```
func ValidateLinkConstraints(
    ctx context.Context,
    linkChains []*links.ChainLinkNode,
    spec speccore.BlueprintSpec,
    params core.BlueprintParams,
) ([]*core.Diagnostic, error)
```

Called in `loadSpecAndLinkInfo` (`container/loader.go:651-735`), after link chains are built via `linkInfo.Links(ctx)` and before `ValidateLinkAnnotations`. At this point the full concrete graph is available and every `ChainLinkNode` has its `LinkImplementations` populated.

The function performs two passes:

1. **Per-edge custom validation.** For each link edge, call `ValidateLink` on the link implementation with the resource specs and annotations. Nil-check follows the existing convention — implementations that return nil or empty diagnostics pass.

2. **Cardinality aggregation.** After the full walk, for each `(ResourceTypeA, ResourceTypeB)` pair, count outbound edges per A-side instance and inbound edges per B-side instance. Compare against `CardinalityA` and `CardinalityB` from `GetCardinality`. Min violations are only detectable after counting all edges. The existing `ChainLinkNode.LinksTo` and `LinkedFrom` fields provide the edge traversal needed.

Updated pipeline sequence in `loadSpecAndLinkInfo`:

```
1. loadSpec (parse + validate resources + transform)
2. NewDefaultLinkInfoProvider (builds concrete link graph)
3. linkInfo.Links(ctx) (resolves chains)
4. NEW: ValidateLinkConstraints (cardinality, custom validate)
5. ValidateLinkAnnotations (existing, unchanged)
6. ValidateResourceEachDependencies (existing, unchanged)
```

### Framework state these additions rely on

- **Diagnostic severity.** Already plumbed. `core.Diagnostic` carries a `Level` field with `DiagnosticLevelError` / `DiagnosticLevelWarning` / `DiagnosticLevelInfo` (`libs/blueprint/core/diagnostics.go:13-42`), and existing validators already return `[]*core.Diagnostic` alongside errors — see the `CustomValidate` integration path at `validation/resource_definitions_validation.go:1505-1528`. `ValidateLinks` can use the same channel with no new infrastructure.
- **`GroupResourcesBySelector` / `extractSelectorLabelsForGrouping` purity.** Confirmed. `libs/blueprint/links/utils.go` imports only `schema`, `speccore`, and common `core` — no registry, no provider lookups. The enumerator builds on these directly rather than re-implementing `LinkSelector.ByLabel` matching.
- **`LinkAnnotationDefinition.ValidateFunc` nil-safety.** All five invocation sites nil-check before calling (`validation/resource_definitions_validation.go:530,651,772,851`; `validation/link_annotation_validation.go:248`). The new `ValidateAnnotationValue` wrapper must preserve this guarantee.
- **Host-side `CanLinkTo` derivation surface.** The gRPC `CanAbstractResourceLinkTo` RPC already exists (`plugin-framework/transformerserverv1/transformer.proto:49`, `abstract_resource_client_wrapper.go:141-178`), but the SDK default currently returns an empty list with a `// The host-side wrapper derives this from registered links` comment (`sdk/transformerv1/abstract_resource_definition.go:122-131`) — the derivation itself is unimplemented. There is no parallel registration surface today, so `TransformerPluginDefinition.AbstractLinks` becomes the single source that both feeds `ValidateLinks` and backfills `CanLinkTo` via the SDK default.
- **`SpecTransformer` / gRPC wire extension.** `SpecTransformer` (`libs/blueprint/transform/transform.go:23-43`) has no `ValidateLinks` method, no `AbstractLink` accessor, and the `Transformer` gRPC service (`transformer.proto:12-70`) has no matching RPCs. All must be added together. The change is additive — the host treats an `Unimplemented` response from older plugins as "no declared-link validation / no link metadata" rather than a hard failure, so no version negotiation is required.
- **`provider.Link` / gRPC wire extension.** The same `Unimplemented` fallback pattern applies to the two new methods on `provider.Link` (`GetCardinality`, `ValidateLink`). Older provider plugins that don't implement them return `Unimplemented`, which the client wrapper maps to zero-value (no constraint) responses. The `ValidateLinkConstraints` function (§4 above) consumes these responses and skips constraints when zero-valued, so existing providers are unaffected.
- **`ChainLinkNode` graph traversal.** `ValidateLinkConstraints` walks the post-transform link chains built by `SpecLinkInfo.Links()`. The `ChainLinkNode` struct (`libs/blueprint/links/speclinkinfo.go`) already exposes `LinksTo` and `LinkedFrom` fields, plus `LinkImplementations` for accessing the `provider.Link` instances. No new fields are needed on `ChainLinkNode` — the existing structure supports the cardinality counting and per-edge validation passes.

---

## Shared `LinkCardinality` type

`LinkCardinality` is used by both abstract links (`transformerv1.AbstractLinkDefinition`) and concrete links (`providerv1.LinkDefinition`) to express min/max constraints on each side of a link relationship. To avoid a cross-package dependency between the two SDK packages, the type lives in the `provider` package — `libs/blueprint/provider/link.go` — alongside the other shared link types (`LinkKind`, `LinkAnnotationDefinition`, `LinkPriorityResource`).

```go
// In libs/blueprint/provider/link.go

// LinkCardinality specifies the minimum and maximum number of links
// allowed for one side of a link relationship.
type LinkCardinality struct {
    // Min is the minimum number of links required. 0 means no minimum.
    Min int
    // Max is the maximum number of links allowed. 0 means unlimited.
    Max int
}
```

Both `AbstractLinkDefinition.CardinalityA/B` and `LinkDefinition.CardinalityA/B` reference this type as `provider.LinkCardinality`. The zero value (`{Min: 0, Max: 0}`) means "no constraint" — existing definitions that omit cardinality are unaffected.

---

## SDK addition: `AbstractLinkDefinition`

The declarative rule surface lives in `~/projects2025/bluelink/libs/plugin-framework/sdk/transformerv1/` as a new file `abstract_link_definition.go`, mirroring the existing `providerv1/link_definition.go` shape. The key structural choice is **per-edge-class**, not per-resource: one `AbstractLinkDefinition` per ordered pair of abstract resource types, symmetric with how concrete `LinkDefinition` is organized. This eliminates the source-vs-target canonicalization problem entirely — each edge class has exactly one home.

### Type definition

```go
// AbstractLinkDefinition declares a link between two abstract resource types.
// It is the abstract-resource analogue of providerv1.LinkDefinition, minus
// the execution hooks (abstract links don't deploy anything — they are
// expanded into concrete links during transformation).
type AbstractLinkDefinition struct {
    // The type of the source abstract resource in the link relationship.
    // Example: "celerity/handler".
    ResourceTypeA string

    // The type of the target abstract resource in the link relationship.
    // Example: "celerity/api".
    ResourceTypeB string

    // Human-readable descriptions for docs and tooling.
    PlainTextSummary      string
    FormattedSummary      string
    PlainTextDescription  string
    FormattedDescription  string

    // Annotation schema for this edge class, keyed by
    // "{resourceType}::{annotationName}" — same convention as
    // providerv1.LinkDefinition.AnnotationDefinitions. Values are the
    // framework's provider.LinkAnnotationDefinition type, reused as-is.
    AnnotationDefinitions map[string]*provider.LinkAnnotationDefinition

    // Cardinality on each side of the edge class.
    // CardinalityA: how many B's each A may link to.
    //   Example: celerity/handler → celerity/api, CardinalityA = {Min: 0, Max: 1}
    //     means a handler may link to at most one api.
    // CardinalityB: how many A's may link to each B.
    //   Example: celerity/api ← celerity/handler, CardinalityB = {Min: 1, Max: 0}
    //     means every api must have at least one handler linking to it
    //     (Max: 0 = unlimited).
    CardinalityA provider.LinkCardinality
    CardinalityB provider.LinkCardinality

    // Escape hatch for constraints that can't be expressed declaratively.
    // Called once per resolved edge matching this definition. Runs after
    // the declarative checks; additional diagnostics are concatenated.
    ValidateFunc func(
        ctx context.Context,
        input *AbstractLinkValidateInput,
    ) (*AbstractLinkValidateOutput, error)
}

type AbstractLinkValidateInput struct {
    Edge      transform.ResolvedLink
    LinkGraph transform.DeclaredLinkGraph
    Params    core.BlueprintParams
}

type AbstractLinkValidateOutput struct {
    Diagnostics []*core.Diagnostic
}
```

### Registration

Add an `AbstractLinks` field to the existing `TransformerPluginDefinition`:

```go
type TransformerPluginDefinition struct {
    // ...existing fields...
    AbstractResources map[string]transform.AbstractResource

    // AbstractLinks is the set of link definitions between abstract
    // resource types owned by this transformer. Each entry is keyed by
    // "{ResourceTypeA}::{ResourceTypeB}" for fast edge-class lookup.
    AbstractLinks map[string]*AbstractLinkDefinition
}
```

The transformer author populates this map at plugin-init time alongside `AbstractResources`. Keying by `{A}::{B}` mirrors the convention used by `core.LinkType(a, b)` in the framework's concrete link machinery.

### SDK implementation of `SpecTransformer.ValidateLinks`

`TransformerPluginDefinition` gets a default `ValidateLinks` method that drives the declarative rules. Transformer authors who use the SDK template get full validation for free:

```go
func (p *TransformerPluginDefinition) ValidateLinks(
    ctx context.Context,
    input *transform.SpecTransformerValidateLinksInput,
) (*transform.SpecTransformerValidateLinksOutput, error) {
    diags := []*core.Diagnostic{}

    // 1. Per-edge pass: type compatibility, annotations, custom validation.
    for _, edge := range input.LinkGraph.Edges() {
        // 1a. Cross-boundary check: abstract source must not link to concrete.
        //     If source is abstract and target is concrete (or vice versa),
        //     emit a diagnostic telling the user to use a substitution ref.
        //     This check is automatic — not something the rule author writes.
        if crossesAbstractConcreteBoundary(edge, input.LinkGraph) {
            diags = append(diags, crossBoundaryDiagnostic(edge))
            continue
        }

        // 1b. Look up the edge class definition.
        key := core.LinkType(edge.SourceType, edge.TargetType)
        def, ok := p.AbstractLinks[key]
        if !ok {
            diags = append(diags, noSuchEdgeClassDiagnostic(edge))
            continue
        }

        // 1c. Annotation validation — delegates to the framework helper
        //     ValidateAnnotationValue, so behaviour is identical to
        //     concrete-link annotation validation.
        for name, def := range def.AnnotationDefinitions {
            value, _ := edge.Annotations[name]
            diags = append(diags, validation.ValidateAnnotationValue(def, value)...)
        }

        // 1d. Custom ValidateFunc escape hatch.
        if def.ValidateFunc != nil {
            out, err := def.ValidateFunc(ctx, &AbstractLinkValidateInput{
                Edge: edge, LinkGraph: input.LinkGraph, Params: input.Params,
            })
            if err != nil {
                return nil, err
            }
            diags = append(diags, out.Diagnostics...)
        }
    }

    // 2. Cardinality pass: for each abstract resource instance, count
    //    outbound edges per (sourceType, targetType) against CardinalityA
    //    and inbound edges per (targetType, sourceType) against CardinalityB.
    //    Emit diagnostics for min/max violations.
    diags = append(diags, validateCardinality(p.AbstractLinks, input.LinkGraph)...)

    return &transform.SpecTransformerValidateLinksOutput{Diagnostics: diags}, nil
}
```

The implementation walks the graph once for per-edge checks, then once more for cardinality aggregation. The separation matters because cardinality is inherently a post-walk operation — you can only know "is this below the minimum?" after counting all edges.

### Why this shape is strictly better than a per-resource rules table

- **Canonicalization is automatic.** One definition per edge class = no duplication, no "should this rule live on the source or the target?" decision.
- **Mirrors the existing concrete-link SDK pattern.** Transformer authors who've seen `LinkDefinition` already understand `AbstractLinkDefinition` at a glance. Same fields, minus execution hooks and `Kind` (which only applies to deployment ordering), plus cardinality.
- **Annotation keying convention already exists.** Reuse `{resourceType}::{annotationName}` from `LinkDefinition.AnnotationDefinitions`.
- **Cardinality naturally applies to both sides.** `CardinalityA` and `CardinalityB` are symmetric, unlike a per-resource model that forces you to express "my outbound cardinality" vs. "my inbound cardinality" separately.
- **Registration slots into the existing `TransformerPluginDefinition`** — one new field, no new top-level type, no changes to how transformer plugins are assembled.

### Second-order uses of the definition table

Because `AbstractLinkDefinition` is plain data (plus one optional function field), the same map drives more than just validation:

- **Pre-transform visualization.** CLI `graph` / `inspect` command renders the declared link graph, annotated with rule metadata: "missing required inbound handler", "allowed annotations: accessLevel, readOnly", "cardinality 1..N". Any target-side filtering beyond `linkSelector.byLabel` is handled by `ValidateFunc`, not a declarative surface.
- **LSP completions and hovers.** Suggest allowed link targets when the user is authoring a `linkSelector`; show annotation schemas on hover; flag cardinality violations in real time.
- **Documentation generation.** Plugin-docgen calls `ListAbstractLinkTypes()` then `AbstractLink(type)` to access `GetTypeDescription()`, `GetAnnotationDefinitions()`, `GetCardinality()`, etc. through the `transform.AbstractLink` interface (§3 above) — the same pattern used for concrete `provider.Link` docs. Works across the gRPC boundary with no type assertions.
- **Host-side `CanLinkTo` derivation.** If the host-side wrapper comment in `AbstractResourceDefinition.CanLinkTo` is accurate, the wrapper can walk `AbstractLinks` and return the set of target types as the `CanLinkTo` answer — providing that infrastructure actually gets wired into framework validation down the line.

All of these are free provided the definition shape is inspectable data, which the design above preserves.

---

## Concrete link validation parity: `providerv1.LinkDefinition` additions

The same declarative validation features available on abstract links — cardinality and custom validation — should also be available on concrete links. Without them, provider authors who need to enforce link constraints must do so ad-hoc inside `StageChanges`, which runs at deployment staging time rather than during the validation pass that catches blueprint errors early. This section describes the additions to `provider.Link`, `providerv1.LinkDefinition`, and the framework validation pipeline.

Neither abstract nor concrete links carry a `Severity` field. Declarative constraint violations (cardinality) are always errors — the constraint was declared because it's a hard rule. `ValidateFunc` (abstract) and `ValidateLink` (concrete) return `[]*core.Diagnostic` where each diagnostic carries its own `Level`, so authors who need warning-level custom checks express that directly.

### `provider.Link` interface additions

Two new methods on the `Link` interface at `libs/blueprint/provider/link.go`. Both are optional-behaviour methods where implementations can return zero values to signal "no constraint."

```go
// GetCardinality retrieves the cardinality constraints for both sides
// of the link relationship. A zero-valued cardinality on either side
// means no constraint is applied.
GetCardinality(
    ctx context.Context,
    input *LinkGetCardinalityInput,
) (*LinkGetCardinalityOutput, error)

// ValidateLink runs custom validation logic for this link at blueprint
// validation time (pre-deploy). This is distinct from StageChanges which
// runs at deployment staging time. ValidateLink receives the resource specs
// and annotations and returns diagnostics.
// Returning nil output or empty diagnostics means validation passed.
ValidateLink(
    ctx context.Context,
    input *LinkValidateInput,
) (*LinkValidateOutput, error)
```

**New input/output types** (in `provider/link.go`):

```go
type LinkGetCardinalityInput struct {
    LinkContext LinkContext
}

type LinkGetCardinalityOutput struct {
    CardinalityA LinkCardinality
    CardinalityB LinkCardinality
}

type LinkValidateInput struct {
    // ResourceASpec is the parsed spec of resource A in the link.
    ResourceASpec *core.MappingNode
    // ResourceBSpec is the parsed spec of resource B in the link.
    ResourceBSpec *core.MappingNode
    // ResourceAName is the logical name of resource A in the blueprint.
    ResourceAName string
    // ResourceBName is the logical name of resource B in the blueprint.
    ResourceBName string
    // ResourceAType is the type of resource A.
    ResourceAType string
    // ResourceBType is the type of resource B.
    ResourceBType string
    // Annotations are the link-related annotations from both resources
    // in the link, keyed by "{resourceType}::{annotationName}".
    Annotations map[string]*core.ScalarValue
    // LinkContext provides access to provider config and context variables.
    LinkContext LinkContext
}

type LinkValidateOutput struct {
    Diagnostics []*core.Diagnostic
}
```

**Why `ValidateLink` is not `StageChanges`.** `StageChanges` receives `*Changes` and `*state.LinkState` — deployment-time data — and returns `*LinkChanges`. Validation-time custom logic needs resource specs and annotations (schema-level data) and returns diagnostics. The inputs, outputs, and lifecycle position are fundamentally different. A provider author who wants to express "this link pair is invalid because resource B's spec.engine is not 'aurora'" should not need to wait until deployment staging to emit that error.

### `providerv1.LinkDefinition` field additions

New fields on the struct at `sdk/providerv1/link_definition.go`:

```go
// Add to LinkDefinition struct, after AnnotationDefinitions:

// Cardinality on the A side of the link: how many B's each A may link to.
// Zero values mean no constraint.
CardinalityA provider.LinkCardinality

// Cardinality on the B side of the link: how many A's may link to each B.
// Zero values mean no constraint.
CardinalityB provider.LinkCardinality

// ValidateFunc is a custom validation function that runs at blueprint
// validation time (pre-deploy). It receives the resource specs and
// annotations for the link pair and returns diagnostics.
// This is distinct from StageChangesFunc which runs at deployment
// staging time. Nil means no custom validation.
ValidateFunc func(
    ctx context.Context,
    input *provider.LinkValidateInput,
) (*provider.LinkValidateOutput, error)
```

**Method implementations** for the two new `provider.Link` interface methods:

```go
func (l *LinkDefinition) GetCardinality(
    ctx context.Context,
    input *provider.LinkGetCardinalityInput,
) (*provider.LinkGetCardinalityOutput, error) {
    return &provider.LinkGetCardinalityOutput{
        CardinalityA: l.CardinalityA,
        CardinalityB: l.CardinalityB,
    }, nil
}

func (l *LinkDefinition) ValidateLink(
    ctx context.Context,
    input *provider.LinkValidateInput,
) (*provider.LinkValidateOutput, error) {
    if l.ValidateFunc == nil {
        return &provider.LinkValidateOutput{}, nil
    }
    return l.ValidateFunc(ctx, input)
}
```

### gRPC wire additions

The provider gRPC service at `plugin-framework/providerserverv1/provider.proto` needs two new RPCs:

```protobuf
rpc GetLinkCardinality(LinkRequest) returns (LinkCardinalityResponse) {}
rpc ValidateLink(ValidateLinkRequest) returns (ValidateLinkResponse) {}
```

The `link_client_wrapper.go` gets two new methods implementing the `provider.Link` interface additions. For backward compatibility with older plugins, `Unimplemented` responses are treated as "no constraint / no validation" (zero-value returns), matching the existing pattern used for `GetIntermediaryExternalState`.

### Backward compatibility

**Zero values = no constraint.** This is the fundamental backward compatibility guarantee:

- `LinkCardinality{Min: 0, Max: 0}` means no cardinality constraint. Existing `LinkDefinition` structs that don't set these fields get zero values automatically.
- `ValidateFunc: nil` means no custom validation (the `ValidateLink` method returns empty diagnostics).

**Interface addition strategy.** Adding methods to `provider.Link` is a breaking change for any external implementations of the interface. However, the SDK's `providerv1.LinkDefinition` is the canonical implementation used by all provider plugins built with the plugin framework. External plugins that implement `provider.Link` directly (rare) will need to add the two new methods. The gRPC client wrapper handles this transparently for out-of-process plugins via the `Unimplemented` fallback.

**Existing providers continue to work unchanged.** `ValidateLinkConstraints` (§Required framework additions, item 4) checks for zero/nil before applying any constraint, so providers that don't populate the new fields experience no behavioral change.

### Parity summary

| Feature | `AbstractLinkDefinition` (`transformerv1`) | `LinkDefinition` (`providerv1`) |
|---|---|---|
| `CardinalityA/B` | `provider.LinkCardinality` | `provider.LinkCardinality` |
| `ValidateFunc` | `func(ctx, *AbstractLinkValidateInput)` — SDK-internal escape hatch; controls its own diagnostic levels | `func(ctx, *provider.LinkValidateInput)` — exposed via `provider.Link.ValidateLink`; controls its own diagnostic levels |
| `Kind` | N/A (abstract links don't deploy — `Kind` drives deployment ordering on concrete links only) | `provider.LinkKind` (already exists) |
| `AnnotationDefinitions` | Already exists | Already exists |
| Execution hooks | N/A (abstract links don't deploy) | Already exists (`StageChanges`, etc.) |
| Validation timing | Pre-transform (`ValidateLinks` on `SpecTransformer`) | Post-transform (`ValidateLinkConstraints` in loader) |

---

## Transformer reference handling during `Transform()`

Three mechanical walks over the blueprint during transformation:

### 1. Sub-ref rewriting

Walk every substitution reference in the spec. For each ref targeting an abstract resource, rewrite it to the concrete resource that now carries that field. For simple 1:1 mappings (e.g. `celerity/handler.spec.arn` → `<handler>_function.spec.arn`), the rewrite is a direct name swap.

### 2. Composed output bridging via `values`

For abstract outputs that compose multiple concrete outputs (e.g. `celerity/api.spec.url` = gateway domain + stage + path), emit an entry in the blueprint's `values` section during transform that holds the composition, then rewrite the original ref to `${values.<name>}`. The `values` section is the transformer's scratch space for these bridges — no new framework mechanism needed.

### 3. Annotation propagation

For each abstract link being expanded into one or more concrete links, project the user's annotations onto the concrete `LinkAnnotationDefinition` map. Because both sides use the framework's `LinkAnnotationDefinition` type:

- Annotations whose names and types match propagate mechanically.
- Annotations that need value transformation (e.g. abstract `accessLevel: readWrite` → concrete IAM action list) get explicit projection code.
- Purely abstract-level annotations are consumed by the transformer and don't appear on the concrete link.

---

## Key file references

### Core framework (`libs/blueprint`)

- `transform/transform.go:23,45-88,105-126` — `SpecTransformer`, `AbstractResource`, `AbstractLink`, `AbstractResourceValidateInput`, `AbstractLinkGet*Input/Output` types.
- `container/loader.go:888-1018` — parse/validate/transform pipeline.
- `container/loader.go:651-735` — `loadSpecAndLinkInfo`, post-transform `LinkInfoProvider` construction.
- `links/speclinkinfo.go:20-31,319-375` — `SpecLinkInfo` interface, `checkCanLinkTo`.
- `validation/substitution_validation.go:266-489` — sub-ref validation (works for abstract and concrete uniformly).
- `resourcehelpers/registry.go:235-267,376-405` — registry dispatch, `CustomValidate` invocation.
- `provider/link.go:344-371` — `LinkAnnotationDefinition` struct; also home of shared `LinkCardinality` type and new `LinkValidateInput`/`LinkValidateOutput`, `LinkGetCardinalityInput`/`Output` types.
- `provider/link.go:11-92` — `Link` interface; gains `GetCardinality`, `ValidateLink` methods.
- `validation/link_annotation_validation.go:74,169-258,261-391` — annotation validation logic.
- `validation/link_constraint_validation.go` — **new file**, `ValidateLinkConstraints` for post-transform concrete link cardinality and custom validation.
- `provider/resource.go:461-664` — `ResourceSpecDefinition`, `ResourceDefinitionsSchema`, `Computed` flag at line 627.
- `substitutions/ref_patterns.go:13-84` — substitution reference pattern (`.spec` only).

### Plugin framework SDK (`libs/plugin-framework`)

- `sdk/transformerv1/plugin_definition.go` — `TransformerPluginDefinition`, where `AbstractLinks map[string]*AbstractLinkDefinition` is added and the default `ValidateLinks` implementation lives.
- `sdk/transformerv1/abstract_resource_definition.go:122-131` — note the `CanLinkTo` host-wrapper comment; implementation should verify and integrate with whatever registration surface the host exposes.
- `sdk/transformerv1/abstract_link_definition.go` — **new file**, the `AbstractLinkDefinition` type; implements `transform.AbstractLink` interface.
- `sdk/providerv1/link_definition.go` — concrete `LinkDefinition`; gains `CardinalityA/B`, `ValidateFunc` fields and `GetCardinality`, `ValidateLink` method implementations.
- `transformerserverv1/transformer.proto` — gRPC service; gains `GetAbstractLinkType`, `GetAbstractLinkTypeDescription`, `GetAbstractLinkAnnotationDefinitions`, `GetAbstractLinkCardinality` RPCs for the `AbstractLink` interface.
- `transformerserverv1/abstract_link_client_wrapper.go` — **new file**, gRPC client wrapper implementing `transform.AbstractLink` across the process boundary with `Unimplemented` fallback.
- `providerserverv1/provider.proto` — gRPC service; gains `GetLinkCardinality`, `ValidateLink` RPCs.
- `providerserverv1/link_client_wrapper.go` — gRPC client wrapper; gains methods for the two new RPCs with `Unimplemented` fallback for backward compatibility.
- `sdk/validation/` — reusable validator helpers; natural home for any annotation-value validation wrappers if needed beyond the framework's own exported helper.

### Celerity transformer (currently open in IDE)

- `resources/handler/handler_resource.go` — abstract resource definition for `celerity/handler`.
- `resources/handler/handler_aws.go` — AWS-specific concrete expansion.

The Celerity transformer's contribution is: (a) populating `TransformerPluginDefinition.AbstractLinks` with link definitions between its abstract resources (`celerity/handler ↔ celerity/api`, `celerity/handler ↔ celerity/datastore`, etc.), (b) implementing substitution-reference rewriting and `values`-bridging inside its `TransformFunc`, and (c) optionally supplying `ValidateFunc` escape hatches on any link definition where declarative rules aren't expressive enough.
