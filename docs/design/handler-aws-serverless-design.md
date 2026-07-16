# `celerity/handler` Implementation on `aws-serverless` — Design

## Context

`celerity/handler` is the central abstract resource in the Celerity transformer. Today it has skeletal scaffolding (`Resource()` is registered, schema is empty, resolver doesn't process link edges, emit produces a placeholder Lambda code section, every link def is `&AbstractLinkDefinition{}`). The aws-serverless contract ([../contract/aws-serverless.md](../contract/aws-serverless.md)) and the upstream handler spec (`celerity-docs: content/docs/framework/applications/resources/celerity-handler.mdx`) together pin down what the finished handler must produce: N `aws/lambda/function` (one per handler), shared `aws/iam/role` (deduped by **link-set** fingerprint), shared `aws/lambda/layerVersion` (deduped by `contentHash`), an `aws/events/rule` / `aws/apigatewayv2/{api,stage}` / `aws/flex/vpc` for absorbed inbound schedules, API attachments and VPC placement, plus the **declared concrete links** that wire them — and full env-var injection per [../contract/index.md §2.2](../contract/index.md#22-sdk-runtime-contract-shared-env-vars). The transformer emits nodes and declares edges; `bluelink-provider-aws` links supply the event source mappings, invoke permissions, API integrations/routes, `vpcConfig` and per-link IAM. See [../contract/aws-serverless.md §3.5](../contract/aws-serverless.md#35-emit-resources-declare-links--the-provider-does-the-wiring).

The outcome of executing this design is a fully-working handler emit on `aws-serverless`, plus the cross-cutting framework wiring required for any handler to actually run end-to-end. The cross-cutting pieces are deliberately documented alongside the handler files so the gaps in the current framework surface plainly.

## Pipeline view (resolve → aggregate → emit)

The framework's `transformutils.RunTransformPipeline` drives this loop (`bluelink: libs/plugin-framework/sdk/transformutils/pipeline.go`). [../../transformer/transformer.go](../../transformer/transformer.go) registers `shared.AWSServerless: createAWSServerlessAggregator()`, so the pipeline runs through the existing aggregator wiring rather than short-circuiting to passthrough.

What each phase actually owns:

- **Resolve** ([../../resources/handler/handler_resolve.go](../../resources/handler/handler_resolve.go)) — target-agnostic, **per resource type**. Reads `linkGraph.EdgesFrom` / `EdgesTo`, classifies edges, applies inheritance, returns one `*ResolvedHandler` carrying everything the handler emit will need. Lives in the handler package because it operates on the handler's spec.
- **Aggregate** (new `transformer/aggregate_aws_serverless.go`) — target-specific, **structural and coordinative**. Sees a flat `[]transformutils.ResolvedResource` (interface-only — `ResourceName()` / `ResourceType()` and nothing else). Three jobs, all structural:
  1. **Filter / fold primaries** — under `aws-serverless` it drops contributory-only types from primaries (`*handlerconfig.ResolvedHandlerConfig`, `*consumer.ResolvedConsumer`, `*schedule.ResolvedSchedule`); VPC resolved structs remain primaries (they emit their own concrete VPC). Under `aws-v1` it folds handler+API resolved structs into a synthesized `ResolvedService`. This is the one place where target-specific structural decisions live, per [resolve-emit-pipeline-design.md §Aggregate Phase](resolve-emit-pipeline-design.md).
  2. **Coordinate SharedParent declaration** — `EmitPlan.SharedParents` must be declared upfront because the framework's `mergeSharedParents` only merges contributions whose key matches a declared parent (unknown keys get silently dropped, see `transformutils.RunTransformPipeline → mergeSharedParents`). The aggregator can't compute IAM fingerprints or layer hashes itself — that requires knowing handler-specific fields, which means type assertions, which is exactly the thing aggregate is too generic to do well. Instead, it delegates: each resource package that needs SharedParents exposes a helper like `handler.AWSServerlessSharedParents(ctx, primaries, deps) []SharedParent` that does its own type assertion (filtering primaries to `*ResolvedHandler`) and returns the parents seeded for handlers. The aggregator just calls these helpers and concatenates the results.
  3. **Resolve any cross-resource invariants the framework can't catch** — name collisions across primaries, plan-level diagnostics. Phase 1 has none of these for handler.
- **Emit** ([../../resources/handler/handler_aws_serverless_emit.go](../../resources/handler/handler_aws_serverless_emit.go)) — target-specific, per primary. Receives one `*ResolvedHandler`, builds the Lambda spec, and returns **no** `SharedParentContributions`: the `iam-role:<fp>` seed is already complete (provider links inject their per-link statements at deploy time), and the `layer:<hash>` seed carries its full `compatibleRuntimes` union. The SharedParents seeded in step (2) therefore stand as emitted, with nothing to merge in.

The split is: **resolve owns per-resource link reading, emit owns per-resource concrete output, aggregate owns structural decisions that cross resources**. The IAM/layer SharedParent enumeration sits awkwardly between aggregate and emit (it needs cross-resource visibility, so emit can't do it; it needs handler-type knowledge, which aggregate can't have without type assertions); the handler-package helper called from the aggregator is the cleanest answer.

## Handler resource files

### [resources/handler/handler_resource_schema.go](../../resources/handler/handler_resource_schema.go) (modified)

Today it's an empty `ResourceDefinitionsSchema`. Spec out the full schema from `celerity-handler.mdx §Specification`: `handlerName`, `handler`, `codeLocation`, `runtime`, `memory` (default 512), `timeout` (default 30), `tracingEnabled` (default false), `environmentVariables` (`map[string]string`). Mark `id` (the output) as `Computed: true` per `celerity-handler.mdx §Outputs`. Per [abstract-resources-links-design.md §1 "Substitution references"](abstract-resources-links-design.md), declaring `id` here makes `${resources.<handler>.spec.id}` validate pre-transform for free — no extra wiring needed.

### [resources/handler/handler_resolve.go](../../resources/handler/handler_resolve.go) (modified) + `resources/handler/handler_resolved.go` (new)

Move `ResolvedHandler` out of `handler_resolve.go` into `handler_resolved.go` per the convention in [resolve-emit-pipeline-design.md §File Structure](resolve-emit-pipeline-design.md). `ResolvedHandler` keeps the existing fields and adds:

- `Runtime`, `Memory`, `Timeout`, `TracingEnabled`, `EnvironmentVariables`, `CodeLocation` — the *resolved* values after inheritance, so emit reads from one struct rather than re-running the inheritance walk.
- `EventSource` discriminator (`http|websocket|consumer|schedule|custom`) derived from inbound contributors plus `celerity.handler.*` annotations on the handler resource. Drives `CELERITY_HANDLER_TYPE`.
- `RoutingTag` (optional) for `CELERITY_HANDLER_TAG` — set when the handler is a consumer-route or schedule-key handler ([../contract/index.md §2.2 "Handler routing"](../contract/index.md#22-sdk-runtime-contract-shared-env-vars)).
- `VPCSubnetType` (`public|private`, default `public`) read from inbound `vpc->handler` link annotation `celerity.handler.vpc.subnetType` (`celerity-handler.mdx §celerity/vpc 🔗 celerity/handler`).

`resolveHandler` walks the link graph:

1. **EdgesFrom** — classify by `edge.TargetType` into `Queues|Topics|Datastores|Databases|Buckets|Caches|Configs`, each as `*types.LinkedResource{Name, Resource, Edge}` (the existing type at [../../types/types.go](../../types/types.go)).
2. **EdgesTo** — classify by `edge.SourceType` into `Consumers|Schedules`, single-valued `VPC` and `HandlerConfig`. Cardinality (max 1 for VPC and HandlerConfig) is *supposed* to be enforced by `SpecTransformer.ValidateLinks`; the resolver trusts it. See gap #2 below — the link defs are empty today, so resolve effectively trusts something nothing checks.
3. **Inheritance resolution** — apply per-field precedence from [../contract/index.md §1.1](../contract/index.md#11-celerityhandler-abstract-resource-spec): handler spec > linked `celerity/handlerConfig` (via `HandlerConfig` field) > blueprint-level `metadata.sharedHandlerConfig` > defaults. Reuse the same field shape `handlerconfig.ResolvedHandlerConfig` will define (so the resolver doesn't need a second parser for the metadata block). A missing `runtime` after inheritance is a fatal `core.Diagnostic` per index.md §1.1.

### [resources/handler/handler_aws_property_map.go](../../resources/handler/handler_aws_property_map.go) (modified)

Today only `spec.handlerName` and `spec.handler` are mapped. The property map's job is **reference rewriting**: when a user writes `${resources.<handler>.spec.<x>}` somewhere in the blueprint (export, value, another resource's spec, datasource, top-level metadata), the rewriter decides where that reference points in the *output* blueprint. It does not transform the value of any field on the emitted Lambda — that's the emit's job (`getTargetRuntime`, `buildTracingConfig`, etc.).

Coverage rule: **every abstract spec field must be referenceable**. The handler emit replaces the abstract resource entirely, so any `${resources.<h>.spec.<x>}` left un-rewritten in the output blueprint would point at a resource that no longer exists. Two patterns cover all cases:

1. The concrete Lambda exposes a faithful equivalent of the abstract value → `Rename` to that field.
2. The concrete Lambda's value differs from the abstract value (different units, different semantics, different type) → `ValueRef` to a transformer-derived blueprint value that the emit populates with the abstract literal. This preserves the abstract value verbatim under a stable name, so user references resolve correctly even when the concrete resource has nothing matching.

With `concreteName(r) = r.Name + "_lambda_func"` (already wired at [handler_resource.go:36](../../resources/handler/handler_resource.go#L36)), `ValueRefSpec.Suffix` produces value names of the form `<handlerName>_lambda_func<suffix>`.

- **Renames** (`PropertyMap.Renames`):
  - `spec.handlerName → spec.functionName` — same string value, renamed key.
  - `spec.memory → spec.memorySize` — integer MB, identical units, renamed key.
  - `spec.timeout → spec.timeout` — identity, listed so the auto-derived capability matrix sees the supported abstract path (`transformutils.RewriterFromPropertyMap` builds capabilities from the same `PropertyMap`).
  - `spec.environmentVariables → spec.environment.variables` — same `map[string]string` shape, nested under Lambda's `environment` block.
  - `spec.id → spec.arn` — `id` is the abstract-spec output (`celerity-handler.mdx §Outputs`); on aws-serverless it lives directly as the `arn` computed field on `aws/lambda/function` (`bluelink-provider-aws: services/lambda/function_resource_schema.go`). Direct rename.
- **ValueRefs** (`PropertyMap.ValueRefs`) — bridge fields whose abstract value has no faithful equivalent on the concrete Lambda. The emit produces the matching `Values` entries (see emit section).
  - `spec.runtime → {Suffix: "_celerity_runtime"}` — Lambda's `spec.runtime` holds the *mapped AWS runtime string* (`nodejs24.x`, `python3.13`); the abstract value is the Celerity runtime ID (`nodejs24.x`, `python3.13.x`). Bridge via derived value `<handlerName>_lambda_func_celerity_runtime` carrying the literal Celerity ID.
  - `spec.handler → {Suffix: "_handler_id"}` — Lambda's `spec.handler` holds the CLI-generated bootstrap entry point (`__celerity_lambda_entry__.handler`, per [../contract/aws-serverless.md §7](../contract/aws-serverless.md#7-lambda-handler-field-read-verbatim-from-the-build-manifest)); the abstract value is the user's logical handler ID (`save_order`, used as `CELERITY_HANDLER_ID`). Bridge via derived value `<handlerName>_lambda_func_handler_id`.
  - `spec.tracingEnabled → {Suffix: "_tracing_enabled"}` — Lambda's `spec.tracingConfig.mode` is a string enum (`Active`/`PassThrough`); the abstract value is a bool. Bridge via derived value `<handlerName>_lambda_func_tracing_enabled` (bool literal).
  - `spec.codeLocation → {Suffix: "_code_location"}` — the CLI consumes the value at build time and the concrete Lambda has no equivalent (`code.s3Bucket`/`code.s3Key` are the build-output coordinates, not the source path). Bridge via derived value `<handlerName>_lambda_func_code_location` (string literal echo of what the user wrote).
- **Custom rules** (`PropertyMap.Custom`):
  - None for handler. Custom is the right tool when the rewrite needs to inspect the *path shape* (e.g. an array index, a wildcard segment) before deciding the substitution; handler's spec is flat and every field maps cleanly via `Renames` or `ValueRefs`.

Net: 5 renames, 4 value-ref bridges, 0 custom rules. Every abstract field is referenceable in the output blueprint. The matching `DerivedValues` are produced by the emit.

### [resources/handler/handler_aws_serverless_emit.go](../../resources/handler/handler_aws_serverless_emit.go) (modified — total rewrite)

Composable subbuilders, one Lambda function emitted per handler plus the trigger nodes the provider links don't own (and standalone event source mappings for external streams/SQS). No role or layer fragments are contributed to SharedParents: both seeds are already complete (see the emit phase above).

```
emit(ctx, r, resPropRewriter, transformCtx) -> EmitResult
  manifest <- deps.BuildManifestLoader.Load(ctx, transformCtx, deps.Env)   // cached per-call
  validation <- transformutils.IsValidationContext(transformCtx)

  funcName  <- "<handlerName>_lambda_func"
  fp        <- iam_fingerprint.ComputeFingerprint(r, AWSServerless)
  layerHash <- shared/aws/layer_planner.SelectLayerForHandler(r.Name, manifest)

  spec := MappingNode{
    functionName:    r.Resource.Spec.handlerName,
    handler:         buildHandlerEntrypoint(manifest, validation),       // verbatim from manifest.lambda.entryPoint
    runtime:         runtimes[AWSServerless][r.Runtime],                 // mapped per §6, fatal-diagnostic if absent
    code:            buildCodeSpec(manifest, validation),                // {s3Bucket, s3Key} from manifest.lambda.appCode
    memorySize:      r.Memory,
    timeout:         r.Timeout,
    environment.variables: buildEnvironmentVariables(r, configStoreInfo), // see ordering below
    layers:          buildLayers(layerHash, configStoreInfo, useExt),    // refs to celerityLambdaLayer_<hash>; +AWS extension
    role:            "${resources.celerityLambdaExec_<fp>.spec.arn}",
    // vpcConfig is intentionally NOT emitted: the aws/flex/vpc::aws/lambda/function
    // link populates it from the VPC's resolved subnets/security group. The
    // transformer only stamps the aws.flexvpc.lambda.subnetType placement annotation.
    tracingConfig:   buildTracingConfig(r.TracingEnabled),               // {mode:"Active"} or omitted
    tags:            shared.AWSSpecTagsFromResourceMetadata(r.Resource.Metadata),
  }

  rewritten := subwalk.WalkMappingNode(spec, transformutils.RewriteResourcePropertyRefs(resPropRewriter))

  out.Resources[funcName] = &schema.Resource{
    Type:     "aws/lambda/function",
    Spec:     rewritten,
    Metadata: schema.Metadata{Annotations: TransformerBaseAnnotations({r.Name, "celerity/handler", "code-hosting"})},
  }

  // Event sources: emit the trigger NODES only; the provider's links create the
  // permissions / event source mappings / API integrations. See §"Event sources"
  // and ../contract/aws-serverless.md §3.5.
  out.Resources += handler_aws_links.EmitTriggerNodes(r, funcName)   // events/rule (targets[].arn -> func), apigatewayv2 api+stage, flex/vpc
  out.Links     += handler_aws_links.DeclareLinks(r, funcName)       // concrete links + their aws.* link annotations

  // NO SharedParentContributions: both the iam-role and layer seeds are already
  // complete. SeedRoleSpec carries the full policy set (links inject per-link IAM
  // at deploy time), and the layer seed built at aggregate time already carries
  // the full compatibleRuntimes union across every handler sharing the layer.

  // Derived values that back the property map's ValueRefs entries. Each holds
  // the abstract literal under a stable name so user references like
  // `${resources.<h>.spec.runtime}` resolve to the right value in the output
  // blueprint after the abstract handler resource itself is gone.
  out.DerivedValues = {
    "<handlerName>_lambda_func_celerity_runtime":  schema.Value(literal string, r.Runtime),
    "<handlerName>_lambda_func_handler_id":        schema.Value(literal string, r.Resource.Spec.handler),
    "<handlerName>_lambda_func_tracing_enabled":   schema.Value(literal bool,   r.TracingEnabled),
    "<handlerName>_lambda_func_code_location":     schema.Value(literal string, r.CodeLocation),
  }
  return out
```

Env-var assembly order (later wins, per [../contract/index.md §2.2](../contract/index.md#22-sdk-runtime-contract-shared-env-vars)):

1. **Bootstrap** — `CELERITY_PLATFORM=aws`, `CELERITY_DEPLOY_TARGET=aws-serverless`. ([../contract/aws-serverless.md §5.1](../contract/aws-serverless.md#51-bootstrap-aws-specific))
2. **Handler routing** — `CELERITY_HANDLER_ID=<handler>`, `CELERITY_HANDLER_TYPE=<eventSource>`, optional `CELERITY_HANDLER_TAG=<routingTag>`.
3. **Resource link discovery** — `CELERITY_CONFIG_RESOURCES_STORE_KIND=parameter-store` (always, per [../contract/aws-serverless.md §10.2](../contract/aws-serverless.md#102-the-internal-resources-namespace); there is no override) and `CELERITY_CONFIG_RESOURCES_STORE_ID=${resources.<storeRes>.spec.<id>}` (an SSM **path prefix**). Set when *any* outbound link is declared (the routing file consumed by the SDK is bundled into `app.zip` by the CLI, but the store identifier must be injected here). Per-`celerity/config` namespace vars (`CELERITY_CONFIG_<NS>_*`). **Never** set the bare `CELERITY_CONFIG_STORE_ID` — it collapses SDK namespace discovery to a single `default` namespace ([../contract/index.md §2.2](../contract/index.md#22-sdk-runtime-contract-shared-env-vars)).
4. **Telemetry** — `CELERITY_TELEMETRY_ENABLED=true` when `r.TracingEnabled`; `CELERITY_LOG_FORMAT=json` only if user hasn't overridden it.
5. **User** — `r.EnvironmentVariables` merged last so user overrides take effect.

The handler emit does *not* set the deprecated `CELERITY_MODULE_PATH` ([../contract/aws-serverless.md §5.1](../contract/aws-serverless.md#51-bootstrap-aws-specific): the CLI ships a generated bootstrap file).

### Event sources: declare links, do not build resources

> **Superseded.** An earlier revision of this design proposed `resources/handler/handler_aws_event_sources.go` with `buildAPIPermissions` / `buildScheduleRules` / `buildEventSourceMappings`. **That file should not exist.** `bluelink-provider-aws` ships implemented links between concrete resources that perform this wiring themselves. See [../contract/aws-serverless.md §3.5](../contract/aws-serverless.md#35-emit-resources-declare-links--the-provider-does-the-wiring).

What the transformer actually does per inbound event source:

- **Schedule** — emit an `aws/events/rule` whose `targets[].arn` is `${resources.<handler>_lambda_func.spec.arn}`. That reference alone *activates* the `aws/events/rule::aws/lambda/function` link (the provider marks `targets[].arn` with `ActivatesLinkOnReference`), and the link deploys the `aws/lambda/permission` as a managed intermediary. Do **not** emit the permission.
- **Consumer (queue / datastore)** — declare the `aws/sqs/queue::aws/lambda/function` or `aws/dynamodb/table::aws/lambda/function` link. The link creates the event source mapping via a direct Lambda SDK call (it is not a blueprint resource at all) and injects the source-read IAM statement into the execution role. Do **not** emit `aws/lambda/eventSourceMapping`. Celerity's consumer config becomes link annotations (`aws.sqs.lambda.batchSize`, `aws.sqs.lambda.reportBatchItemFailures`, `aws.dynamodb.lambda.stream.startingPosition`, …). The DynamoDB link enables table streams itself.
- **API (HTTP / WebSocket)** — emit `aws/apigatewayv2/api` **and** `aws/apigatewayv2/stage`, then declare `aws/apigatewayv2/api::aws/lambda/function`. The link creates the `integration`, `route`, invoke `permission`, and (for two-way WebSocket) `integrationResponse` + `routeResponse`. The **stage is the only piece it does not create**. Route and auth come from its `routeKey` / `authorizerId` / `authorizationType` annotations.
- **VPC** — emit `aws/flex/vpc` (the `celerity/vpc` `preset` passes through verbatim; `mode` maps `managed`→`create` / `referenced`→`reference`, and in `reference` mode `preset` and the `aws.vpc.*` deploy keys are dropped) and declare `aws/flex/vpc::aws/lambda/function` with `aws.flexvpc.lambda.subnetType`. The link sets the function's `vpcConfig`; do **not** emit `vpcConfig`. Reference mode is how a VPC is shared across blueprints without a cross-blueprint link — see [../contract/resource-mapping-aws-serverless.md](../contract/resource-mapping-aws-serverless.md) `celerity/vpc`.

The earlier claim that batch settings are read from `Edge.Annotations` is wrong twice over: `linktypes.ResolvedLink` has no `Annotations` field, and these settings are expressed as *AWS link annotations* on the emitted concrete link, not read from the abstract edge.

### `resources/handler/iam_fingerprint.go` (new)

Pure: `ComputeFingerprint(r *ResolvedHandler, target string) string`. Inputs hashed with SHA-256, truncated to 8 hex chars per [../contract/aws-serverless.md §8](../contract/aws-serverless.md#8-iam-execution-role-shared-by-link-set-fingerprint):

- `tracingEnabled` (Phase 1 input).
- Sorted set of `(linkType, targetType)` pairs covering every outbound link that grants new IAM (queue/topic/datastore/sqlDatabase/bucket/cache, plus `ConfigStoreAccess{kind, resourceName}` per [../contract/aws-serverless.md §11](../contract/aws-serverless.md#11-iam-implications-config-store)).
- VPC presence (drives ENI permissions).
- IAM-auth SQL flag (`rds-db:connect`).

Stable JSON encoding with sorted keys — Phase 1's "single-fingerprint" property follows because `tracingEnabled` is the only varying input until link-derived inputs land; the function just degrades gracefully as inputs accumulate.

### [resources/handler/runtimes.go](../../resources/handler/runtimes.go) (modified)

Already has the right shape. Add a small helper that returns the `core.Diagnostic` for an unsupported runtime so emit stays tight, and extract the diagnostic message format so it lists every supported identifier in the error (per [../contract/index.md §2.3 "Unknown identifiers"](../contract/index.md#23-runtime-identifier-mapping-concept)).

### [resources/handler/handler_resource.go](../../resources/handler/handler_resource.go) (modified)

Wire the new dependencies through `Resource(envMap)`. Pass an `emitDeps` struct (env map, build-manifest loader, runtime mapper) into `newAWSServerlessEmitter` so each handler emit shares one build-manifest fetch. No structural change to the `AbstractResourceDefinition`.

## Cross-cutting wiring

### `transformer/aggregate_aws_serverless.go` (new) — thin coordinator

Implements `transformutils.Aggregator`. Stays small and delegates per-resource SharedParent enumeration to the resource packages:

```
aggregateAWSServerless(deps) Aggregator:
  return func(ctx, resolved):
    primaries := []ResolvedResource{}
    for r in resolved:
      switch r.(type):
        case *handlerconfig.ResolvedHandlerConfig,
             *consumer.ResolvedConsumer,
             *schedule.ResolvedSchedule:   // contributory-only
          continue
        default:
          // VPC resolved structs stay primaries: they emit their own concrete VPC.
          primaries = append(primaries, r)

    parents := []SharedParent{}
    parents = append(parents, handler.AWSServerlessSharedParents(ctx, primaries, deps)...)
    // Future targets / resources slot here without touching this file:
    // parents = append(parents, api.AWSServerlessSharedParents(ctx, primaries, deps)...)

    return &EmitPlan{Primaries: primaries, SharedParents: parents}
```

The aggregator does **not** type-assert handler primaries itself, doesn't know what an IAM fingerprint is, doesn't read the build manifest. It owns the structural decision (filter-vs-fold, in this case filter) and the coordination of per-resource SharedParent helpers.

### `resources/handler/handler_aws_serverless_shared_parents.go` (new)

The handler-package helper that the aggregator calls. This is where type assertion happens, because here the type is known:

```
AWSServerlessSharedParents(ctx, primaries []ResolvedResource, deps) []SharedParent:
  handlers := []*ResolvedHandler{}
  for p in primaries:
    if h, ok := p.(*ResolvedHandler); ok:
      handlers = append(handlers, h)
  if len(handlers) == 0:
    return nil

  manifest := deps.BuildManifestLoader.Load(ctx, deps.TransformCtx, deps.Env)
  parents := []SharedParent{}

  // IAM roles — one per distinct fingerprint
  fingerprintsSeen := map[string]string{}                // fp -> first handler name
  for h in handlers:
    fp := ComputeFingerprint(h, AWSServerless)
    if _, exists := fingerprintsSeen[fp]; !exists:
      fingerprintsSeen[fp] = h.Name
      parents = append(parents, SharedParent{
        Key:          "iam-role:" + fp,
        ResourceName: "celerityLambdaExec_" + fp,
        ResourceType: "aws/iam/role",
        Annotations:  TransformerBaseAnnotations({h.Name, "celerity/handler", "infrastructure"}),
        SeedSpec:     shared/aws.SeedRoleSpec(fp, h /* for partition + base policies */),
      })

  // Lambda layers — one per distinct contentHash. The runtime union is built here,
  // at aggregate time, across every handler sharing the layer, so the seed is
  // complete and the emit contributes nothing.
  runtimes  := map[string]set{}      // hash -> set of mapped runtimes
  artifacts := map[string]Artifact{}
  order     := []string{}            // deterministic hash order
  for h in handlers:
    if hash, artifact := layer_planner.SelectLayerForHandler(h.Name, manifest); hash != "":
      if !seen(hash): artifacts[hash] = artifact; order = append(order, hash)
      runtimes[hash].add(mappedRuntime(h))
  for hash in order:
    parents = append(parents, SharedParent{
      Key:          "layer:" + hash,
      ResourceName: "celerityLambdaLayer_" + hash,
      ResourceType: "aws/lambda/layerVersion",
      Annotations:  TransformerBaseAnnotations({firstHandler(hash).Name, "celerity/handler", "code-hosting"}),
      SeedSpec:     layer_planner.SeedLayerSpec(artifacts[hash], sortedKeys(runtimes[hash])),  // full compatibleRuntimes union
    })

  return parents
```

The `deps` parameter wraps the env-map + build-manifest loader that the handler emit also uses, constructed once in `transformer.NewTransformer` and threaded into both the aggregator factory and the emit constructor. This keeps the build-manifest fetch single-flight across both phases.

Why this split is the right shape:
- The aggregator file stays target-shaped and small. Adding aws-v1 is a new aggregator file plus an `api.AWS_v1_SharedParents` helper — no churn on the handler aggregator.
- Type-asserting `*ResolvedHandler` happens once, in the handler package, alongside the rest of the handler-specific logic.
- The helper seeds each SharedParent complete: the role seed (`SeedRoleSpec`) carries its full policy set and the layer seed carries its full `compatibleRuntimes` union. Handler emit therefore returns no `SharedParentContributions`, and there is nothing for the framework to merge in — provider links inject per-link IAM statements at deploy time.

### [transformer/transformer.go](../../transformer/transformer.go) (modified)

Registers `Aggregators: map[string]transformutils.Aggregator{shared.AWSServerless: createAWSServerlessAggregator()}`. With the aggregator registered, `RunTransformPipeline` runs the resolve → aggregate → emit loop rather than `TransformerPluginDefinition.Transform` short-circuiting to passthrough.

`NewTransformer` also constructs the build-manifest loader at startup and registers the AWS S3 fetcher (see next section) so the same loader serves both the aggregator and the emit.

### `shared/buildmanifest/` (new package) — target-agnostic loader with a fetcher registry

The build manifest is a cross-target concept (CLI writes it for any deploy target; the `lambda` sub-manifest is what aws-serverless specifically reads). The fetch step is the only target-specific thing: aws-serverless reads from `s3://`, gcloud-serverless will read from `gs://`, azure from `azblob://`. Everything else — types, parsing, cache, fallback behaviour — is shared.

The package therefore exposes a target-agnostic `Loader` with a registry of object-storage fetchers keyed by URL scheme. Target-specific packages (`shared/aws/`, future `shared/gcloud/`, `shared/azure/`) ship fetcher implementations that the transformer plugin registers at startup.

- `types.go` — target-agnostic top-level: `Manifest{Version, Runtime, Target, Sub map[string]json.RawMessage, Handlers map[string]map[string]json.RawMessage}`. Per-target sub-manifest types (`LambdaManifest{AppCode *Artifact, EntryPoint string, SharedLayer *Artifact}`, `HandlerLambdaArtifacts{Dependencies *Artifact}`) live in this package too but are decoded lazily via a helper (`manifest.LambdaSubManifest()`) so adding gcloud's `cloudfunctions` sub-manifest later doesn't change this file. `Artifact{Type, LocalPath, RemoteURL, ContentHash, Size}` is generic — `RemoteURL` replaces the AWS-specific `S3Bucket`+`S3Key` pair, and each fetcher knows how to parse its scheme back out (the S3 fetcher splits `s3://bucket/key`). Top-of-file comment pointing at `celerity: apps/cli/internal/build/{types,lambda_types}.go`.
- `fetcher.go` — `Fetcher interface { Scheme() string; Fetch(ctx, url string) ([]byte, error) }`. Pure interface; zero target dependencies.
- `loader.go` — `Loader struct{ fetchers map[string]Fetcher }` plus `RegisterFetcher(Fetcher)`. `Load(ctx, transformCtx, env) (*Manifest, []*core.Diagnostic, error)` reads `celerity.buildManifest` from the context var (`shared.BuildManifestContextVarKey`, [../../shared/config_keys.go](../../shared/config_keys.go)) and dispatches:
  - **Absolute filesystem path** → `os.ReadFile`. Same code path for every target — the local deploy engine uses a path regardless of target.
  - **URL with scheme** → look up the scheme in the fetcher registry. Missing scheme → fatal diagnostic naming the scheme and the set of registered ones (helps catch "configured for gcloud, only AWS fetcher registered"). Found → delegate.
  Returns `(nil, [warning], nil)` when the context var is unset or fetch fails on a non-validation context, per [../contract/index.md §1.4 "Fallback behaviour"](../contract/index.md#14-celerity-cli-build-manifest). Validation context returns the placeholder code-location shape that the current emit already uses ([../../resources/handler/handler_aws_serverless_emit.go:154-162](../../resources/handler/handler_aws_serverless_emit.go#L154-L162)).
- `cache.go` — per-transform-call cache keyed by manifest URL/path so the aggregator and every handler emit share one fetch. Target-agnostic.

### `shared/aws/buildmanifest_fetcher.go` (new) — the S3 fetcher

Owns the `s3://` scheme. Constructed via `NewS3BuildManifestFetcher(envMap)`; under the hood it lazily builds an S3 client per request using the existing `awsshared.NewS3Client` ([../../shared/aws/s3_client.go](../../shared/aws/s3_client.go)) so transformer-config keys (`aws.region`, `aws.s3.endpoint`, `aws.s3.usePathStyle`, credentials, assume-role) flow through unchanged. `Fetch` parses the `s3://bucket/key` URL and issues `GetObject`.

The transformer plugin's `NewTransformer(envMap)` constructs the loader once at startup and registers fetchers for every deploy target it ships emits for. For v0 that's just AWS:

```go
loader := buildmanifest.NewLoader()
loader.RegisterFetcher(awsshared.NewS3BuildManifestFetcher(envMap))
// future: loader.RegisterFetcher(gcloudshared.NewGCSBuildManifestFetcher(envMap))
//        loader.RegisterFetcher(azureshared.NewBlobBuildManifestFetcher(envMap))
```

The same `loader` is then threaded into both the aggregator factory and the handler emit constructor through a small `deps` struct (`{Env, BuildManifestLoader}`), single-flighted via the loader's cache. No per-target client factory leaks into the handler package or the aggregator.

This also means handler resolve/emit never sees AWS-specific dependencies: when the gcloud-serverless emit lands, it calls the same `deps.BuildManifestLoader.Load(...)` and gets back the same `*Manifest`. The handler emit decodes the target-relevant sub-manifest (`manifest.LambdaSubManifest()` for aws-serverless, `manifest.CloudFunctionsSubManifest()` for gcloud-serverless) — only that decoding is target-specific.

### `shared/aws/iam_planner.go` (new)

> **Substantially revised.** The provider's links inject IAM grants themselves (see [../contract/aws-serverless.md §8](../contract/aws-serverless.md#8-iam-execution-role-shared-by-link-set-fingerprint)). `PlanRoleFragment`, the per-handler `SharedParentContributions["iam-role:<fp>"]` fragments, and every per-link inline policy below are **obsolete** — the transformer must not emit them.

- `SeedRoleSpec(fingerprint string) *core.MappingNode` — builds the **complete** role spec (there are no contributions to merge into it). It contains only:
  - `roleName` — **required**. The links resolve the function's role to an `aws/iam/role` resource in the same blueprint by this name; a role without it fails every link at deploy.
  - `assumeRolePolicyDocument` trusting `lambda.${providerDomain(deployTarget)}` (the helper hiding the `amazonaws.com` literal ships in this file). Note the provider's policy-document schema uses **lowercase keys**: `{version, statement: [{effect, action, resource, principal: {service}}]}` — not CFN-style `Version`/`Statement`/`Effect`.
  - `managedPolicyArns: ["arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"]`.
  - `policies` — a **list of `{policyName, policyDocument}` objects**, not a map. Only `celerity-xray` (when `r.TracingEnabled`), since no link grants X-Ray. The allocator's own `bluelink-link-access` policy coexists in this list.
- `ComputeFingerprint(r *ResolvedHandler) string` — hashes the handler's **link set**: the sorted `(linkType, targetResourceName)` pairs, plus `tracingEnabled` and VPC placement. It no longer computes policy content; it only decides which handlers may share a role resource. Since links inject grants into whichever role a function references, sharing a role between handlers with *different* link sets would leak permissions.

Because the seed is complete and identical for every handler sharing a fingerprint, `RoleFragment`/`SharedParentContributions` for roles disappear entirely — and with them the fingerprint↔fragment lockstep hazard. Layer SharedParents remain (dedup by `contentHash` is still the transformer's job).

### `shared/aws/layer_planner.go` (new)

- `SelectLayerForHandler(handlerName string, manifest *Manifest) (contentHash string, artifact *Artifact)` — per [../contract/aws-serverless.md §9](../contract/aws-serverless.md#9-lambda-layers): per-handler `lambda.dependencies` if non-null, else `lambda.sharedLayer`, else `("", nil)`.
- `SeedLayerSpec(artifact *Artifact, runtimes []string) *core.MappingNode` — `{content: {s3Bucket, s3Key}, compatibleRuntimes: [...]}`. The `compatibleRuntimes` union is computed once, at aggregate time, over every handler that shares the layer hash, and passed in complete — the seed is authoritative and the emit contributes no runtime fragments.

### [shared/aws/config_definition.go](../../shared/aws/config_definition.go) (modified)

Add the AWS config-store keys [../contract/aws-serverless.md §2](../contract/aws-serverless.md#2-deploy-configuration-aws-specific-keys) documents:

- `aws.config.replicateRegions` (string, comma-separated regions).
- `aws.config.regionKMSKeys.<region>` (string, KMS key ARN per region).
- `aws.config.useLambdaExtension` (bool, default `false`).

**No `aws.configStore.kind` key.** Backend selection is derived from the `celerity/config` `plaintext` field, and the internal `resources` namespace is always Parameter Store — see [../contract/aws-serverless.md §10](../contract/aws-serverless.md#10-runtime-configuration-store-aws-backends). The earlier `aws.configStore.*` prefix was wrong on both counts: it duplicated a spec-level decision and collided with the real upstream `aws.config.*` namespace.

`useLambdaExtension` feeds `buildLayers` in handler emit, and only for handlers reading a `secrets-manager` namespace — the extension cannot serve Parameter Store. The replication keys feed config-store emit, not handler emit.

### Link definitions in [links/](../../links/) (modified — every file)

Every file currently returns `&AbstractLinkDefinition{}`. At minimum each needs:

- `CardinalityA` / `CardinalityB` per the spec — e.g. `vpc->handler` is `OneToMany` (one VPC, many handlers); `handler->config` is `ManyToOne`; `handler->queue` is `ManyToOne`.
- `AnnotationDefinitions` for the `celerity.handler.*` annotation schema in `celerity-handler.mdx` (`vpc.subnetType` enum `public|private`, `http.method/path`, `websocket.route`, `consumer.route`, `schedule`, `guard.protectedBy`, `guard.custom`, `public`).
- `ValidateFunc` for cross-edge invariants the schema can't express (e.g. a handler can't be both HTTP and WebSocket simultaneously).

Without these, the resolver's annotation reads (`VPCSubnetType`, `RoutingTag`, etc.) silently degrade to defaults when users mistype keys.

## Critical files

New:
- `resources/handler/handler_resolved.go`
- `resources/handler/handler_aws_links.go` (translates Celerity annotations into declared concrete links + their AWS link annotations; **replaces** the obsolete `handler_aws_event_sources.go`)
- `resources/handler/iam_fingerprint.go`
- `resources/handler/handler_aws_serverless_shared_parents.go`
- `transformer/aggregate_aws_serverless.go`
- `shared/buildmanifest/types.go`
- `shared/buildmanifest/fetcher.go`
- `shared/buildmanifest/loader.go`
- `shared/buildmanifest/cache.go`
- `shared/aws/buildmanifest_fetcher.go`
- `shared/aws/iam_planner.go`
- `shared/aws/layer_planner.go`

Modified:
- [resources/handler/handler_resource.go](../../resources/handler/handler_resource.go)
- [resources/handler/handler_resolve.go](../../resources/handler/handler_resolve.go)
- [resources/handler/handler_resource_schema.go](../../resources/handler/handler_resource_schema.go)
- [resources/handler/handler_aws_property_map.go](../../resources/handler/handler_aws_property_map.go)
- [resources/handler/handler_aws_serverless_emit.go](../../resources/handler/handler_aws_serverless_emit.go)
- [resources/handler/runtimes.go](../../resources/handler/runtimes.go)
- [transformer/transformer.go](../../transformer/transformer.go)
- [shared/aws/config_definition.go](../../shared/aws/config_definition.go)
- Every file under [links/](../../links/)

## Reused functions / utilities

- `transformutils.RunTransformPipeline` — already drives resolve→aggregate→emit + SharedParent merging. No need to reinvent.
- `transformutils.RewriterFromPropertyMap` — already in use at [resources/handler/handler_resource.go:33-38](../../resources/handler/handler_resource.go#L33-L38); auto-derives capabilities from the property map.
- `transformutils.TransformerBaseAnnotations` — for the three required annotations on every emitted concrete resource ([../contract/index.md §2.1](../contract/index.md#21-framework-grouping-annotations)).
- `transformutils.IsValidationContext` — already used at [handler_aws_serverless_emit.go:55](../../resources/handler/handler_aws_serverless_emit.go#L55).
- `pluginutils.GetValueByPath` — for spec field reads (already used).
- `subwalk.WalkMappingNode` + `transformutils.RewriteResourcePropertyRefs` — for in-emit user-ref rewriting (already used at [handler_aws_serverless_emit.go:79-82](../../resources/handler/handler_aws_serverless_emit.go#L79-L82)).
- `awsshared.NewS3Client` — for build-manifest fetch ([../../shared/aws/s3_client.go](../../shared/aws/s3_client.go)).
- `shared.AWSSpecTagsFromResourceMetadata` — blueprint labels → AWS tags ([../../shared/metadata.go](../../shared/metadata.go)).
- `shared.BuildManifestContextVarKey` — already defined at [../../shared/config_keys.go](../../shared/config_keys.go).

## Verification

- `go build ./...` and `go vet ./...`.
- Unit tests:
  - `resources/handler/handler_resolve_test.go` — handler + queue + consumer + handlerConfig + vpc → `ResolvedHandler` has correct outbound/inbound classification and inherited spec values; missing runtime after inheritance returns a fatal diagnostic.
  - `resources/handler/handler_aws_serverless_emit_test.go` — golden tests asserting Lambda spec fields per spec/contract: runtime mapping, `handler` field is verbatim from `manifest.lambda.entryPoint`, env-var pipeline order matches [../contract/index.md §2.2](../contract/index.md#22-sdk-runtime-contract-shared-env-vars), vpcConfig presence, role/layer refs are substitutions to the SharedParent resource names.
  - `resources/handler/iam_fingerprint_test.go` — tracing-only fingerprint differs from link-induced; same input → same hash (determinism).
  - `shared/buildmanifest/loader_test.go` — local-path branch, `s3://` branch with mock fetcher, missing context var → nil + diagnostic, validation context → placeholder.
  - `transformer/aggregate_aws_serverless_test.go` — drops correct contributory types from primaries; produces SharedParents for each distinct fingerprint and contentHash; empty blueprint → empty plan.
- Integration:
  - Minimal blueprint (one handler, no links) → output blueprint contains `<handlerName>_lambda_func`, one shared `celerityLambdaExec_<fp>`, one `celerityLambdaLayer_<hash8>` (when manifest provides one), each with the framework annotations.
  - Two handlers with same `tracingEnabled` → one shared role; two with different → two roles.
- End-to-end smoke: feed a real `.celerity/build-manifest.json` (local path) via the `celerity.buildManifest` context var; assert the output blueprint deploys via the AWS provider plugin (manual step).

## Gaps and oversized pieces

### Framework gaps that block end-to-end

1. **RESOLVED — the aws-serverless aggregator is registered** ([../../transformer/transformer.go](../../transformer/transformer.go) registers `shared.AWSServerless: createAWSServerlessAggregator()`), so `RunTransformPipeline` runs the resolve → aggregate → emit loop end-to-end rather than short-circuiting to passthrough.
2. **All link defs are empty** (`&AbstractLinkDefinition{}` in every [../../links/](../../links/) file). `SpecTransformer.ValidateLinks` walks them via `core.LinkType(...)` lookup (`bluelink: libs/plugin-framework/sdk/transformerv1/plugin_definition.go:303-362`) but the empty defs supply no `CardinalityA/B`, no annotation defs, no `ValidateFunc`. The handler resolver trusts cardinality (single VPC, single handlerConfig) and annotation values (`vpc.subnetType` enum, etc.) that nothing actually enforces. Until the link defs are filled in, malformed blueprints reach emit silently.
3. **Build-manifest types live in CLI `internal/`**. The contract's [§1.4 "Types duplication note"](../contract/index.md#14-celerity-cli-build-manifest) endorses copying — long-term fix is a public `pkg/buildmanifest` in the celerity CLI repo. Until then we own the copy + drift risk and need a top-of-file pointer. The fetcher-registry shape (target-specific fetchers registering for URL schemes) is also a hint that the eventual public package should expose the same interface so target packages can register against it without re-importing private types.
4. **RESOLVED — the provider is complete (v0.3.0) and has been audited against this design.** Schemas live at `bluelink-provider-aws: services/cloudcontrol/gen/<service>_<resource>.go` (CloudControl-generated), except `aws/ssm/parameter` and `aws/ssm/parameterPath` (`services/ssm/`) and `aws/flex/vpc` (`flex/`). The synthetic `aws/ssm/parameterPath` resource + its `aws/lambda/function::aws/ssm/parameterPath` link (added in v0.3.0) back the internal `resources` config store: one path-scoped IAM grant and one prefix env var per namespace, rather than one per parameter — see [../contract/aws-serverless.md §11](../contract/aws-serverless.md#11-iam-implications-config-store). The audit confirmed `functionName`, `handler`, `runtime`, `code.s3Bucket/s3Key`, `memorySize`, `timeout`, `environment.variables`, `layers[]`, `role`, `tracingConfig.mode`, `vpcConfig.*`, and `tags` (a `{key,value}` **list**), and `layerVersion`'s `content.s3Bucket/s3Key` + output `layerVersionArn`. It also caught three defects in the emit code: (a) policy documents use **lowercase** keys (`version`/`statement`/`effect`/`principal.service`), not CFN capitals; (b) `aws/iam/role.policies` is a **list of `{policyName, policyDocument}`**, not a map; (c) the role must set **`roleName`** or every link fails at deploy. Also: `aws/secretsmanager/secret` has no `arn` (its `id` is the ARN), `aws/ssm/parameter` SecureString needs `secureValue` not `value`, and there is no `aws/scheduler/*`.
5. **`Aggregators` is per-target on the framework side, but built-in lazy-loading of the manifest at aggregate time is not.** The aggregator needs the manifest to enumerate distinct layer hashes; the emit needs it to reach `lambda.appCode` and `lambda.entryPoint`. The design picks shared lazy load via the cache package above, but it's worth confirming the framework doesn't already plan to inject build-manifest bytes as a context variable in a future revision (so the loader can switch to a pure parser later).

### Bigger / more complex than expected

1. **Env-var assembly** is not a small file. Five injection sources (bootstrap, routing, config-store, telemetry, user) each gated on different conditions, all merged in a defined order with later wins. Expect ~150 LOC for `buildEnvironmentVariables` alone, and a comparably-sized table-driven test that walks every (source, condition) crosspoint per [../contract/index.md §2.2](../contract/index.md#22-sdk-runtime-contract-shared-env-vars) + [../contract/aws-serverless.md §5](../contract/aws-serverless.md#5-sdk-runtime-aws-specific-env-vars).
2. **RESOLVED — the IAM role seed is complete; there are no per-handler contributions.** The current design (`SeedRoleSpec`) emits a full role — `roleName`, assume-role policy, the base managed policy, and the transformer-owned inline policies (X-Ray, and scoped read policies for external event sources that have no provider link). Provider links inject their own per-link IAM statements into the role at deploy time via the bluelink-link-access allocator, so handler emit returns **no** `SharedParentContributions` for the role. The earlier seed-vs-contribution / fingerprint-byte-equality / inline-policy-union model no longer applies: the role fingerprint groups handlers that share an identical seed, and nothing is merged in afterwards.
3. **Layers and IAM at aggregate time depend on the build manifest already being loaded** before any handler emit fires. The handler-package SharedParents helper (called from the aggregator) drives the first load via the same `BuildManifestLoader` the emit uses; the loader's cache makes subsequent emit fetches free. Worth flagging because the dependency direction (aggregator → handler-package helper → loader → target-specific fetcher) inverts the natural per-target ownership: a target-agnostic loader has to exist before any target package, and target packages register fetchers into it at plugin startup. The design threads this via `transformer.NewTransformer`'s constructor, which is the only file that knows the concrete set of registered targets.
4. **Inheritance resolution** runs in resolve, but `metadata.sharedHandlerConfig` is a blueprint-level metadata field with no typed Go struct in the spec. The resolver needs `*schema.Blueprint` (already passed) plus a small parser that reuses `handlerconfig.ResolvedHandlerConfig`'s field set as the canonical shape. Modest amount of code, but easy to forget.
5. **Reference completeness pulls 4 derived values into every handler emit.** Because every abstract spec field must remain referenceable post-transform and `runtime`/`handler`/`tracingEnabled`/`codeLocation` have no faithful equivalent on the concrete Lambda, each handler emits 4 `DerivedValues` entries (literal echoes of the abstract values) for the property map's `ValueRefs` to point at. This is the structural cost of "every abstract field is referenceable" — it scales with handler count, not blueprint complexity, and the names are deterministic so collisions are impossible. Worth flagging because an earlier draft of this design tried to omit these paths from the property map and rely on the framework's reference-validator warning — that approach leaves un-rewritten substitutions in the output blueprint, pointing at a resource that's been replaced. Wrong; the bridge values are not optional.
6. **OBSOLETE — event-source builders do not exist.** This entry previously described `handler_aws_event_sources.go` as "the largest scope expansion in the design". The provider's links create the event source mappings, invoke permissions and API integrations/routes themselves, so the transformer emits only `aws/events/rule` (with `targets[].arn` referencing the function), `aws/apigatewayv2/{api,stage}` and `aws/flex/vpc`, and **declares the links**. The remaining work is translating Celerity's handler/consumer annotations into the AWS link annotations (`aws.apigatewayv2.lambda.routeKey`, `aws.sqs.lambda.batchSize`, `aws.flexvpc.lambda.subnetType`, …). Scope-cutting this is no longer meaningful — it is a small translation layer, not a builder suite.
