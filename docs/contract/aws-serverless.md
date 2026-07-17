# AWS Serverless Deploy Target Contract

**Parent contract:** [index.md](index.md) (shared concerns across all deploy targets)
**Deploy target value:** `aws-serverless`

This document pins down the parts of the Celerity transformer contract that are specific to the `aws-serverless` deploy target — that is, deployments where every `celerity/handler` is emitted as an AWS Lambda function. It is a companion to [index.md](index.md), not a replacement: the shared concerns described there (handler spec, build-manifest top-level shape, framework annotations, routing-file layout, and so on) still apply and are not repeated here.

**How to read this alongside index.md**: index.md covers what the transformer accepts from upstream and what every target emits in common; this document adds the AWS-specific emission rules, Lambda-specific sub-manifest, Lambda-specific env vars, and IAM access model. When something here contradicts index.md (there shouldn't be a case, but if there is), the shared contract wins — open an issue.

---

## 1. Build manifest: `lambda` sub-manifest

**Source of truth:** `celerity: apps/cli/internal/build/lambda_types.go` (`LambdaManifest` / `LambdaHandlerArtifacts`).

The `aws-serverless` target reads its sub-manifest from the top-level `lambda` key of `.celerity/build-manifest.json` (see [index.md §1.4](index.md#14-celerity-cli-build-manifest) for the top-level shape the CLI and transformer agree on).

**Schema (v1)**

```jsonc
{
  "version": "1",
  "runtime": "nodejs24.x",
  "target": "aws-serverless",
  "lambda": {
    "appCode": {
      "type": "lambda-archive",
      "localPath": ".celerity/build/app.zip",
      "s3Bucket": "celerity-build-123456789012-us-east-1",
      "s3Key": "default/app/<hash>.zip",
      "contentHash": "sha256:...",
      "size": 98765
    },
    "entryPoint": "__celerity_lambda_entry__.handler",
    "sharedLayer": {
      "type": "lambda-layer",
      "localPath": ".celerity/build/shared-layer.zip",
      "s3Bucket": "celerity-build-123456789012-us-east-1",
      "s3Key": "default/shared-layer/<hash>.zip",
      "contentHash": "sha256:...",
      "size": 12345
    }
  },
  "handlers": {
    "ordersHandler": {
      "lambda": {
        "dependencies": null
      }
    }
  }
}
```

- `lambda.appCode` is a **single shared code asset** containing the whole app source tree, the generated Celerity Lambda entry point file (`__celerity_lambda_entry__.{py,mjs,js}`), and the CLI-generated resource-links routing file (currently `resource-links.json` — see the name-discrepancy note in [index.md §3.2](index.md#32-how-the-sdk-finds-values)). Every Lambda function the transformer emits references the same `appCode` artifact; there is no per-handler code zip.
- `lambda.entryPoint` is the Lambda `Handler` field value, identical for every function in the project (for example `"__celerity_lambda_entry__.handler"`). The transformer reads this verbatim and writes it to every emitted Lambda function (see §7).
- `lambda.sharedLayer` is the production dependency layer every handler uses by default.
- `handlers[name].lambda.dependencies` is nullable. `null` means the handler falls back to `lambda.sharedLayer`. A non-null value is a per-handler custom layer; the transformer dedupes layers across handlers by `contentHash` (see §9).
- Artifact types used by the transformer in Phase 1 are `lambda-archive` for `appCode` and `lambda-layer` for dependency layers. `container-image` is reserved for future code-hosting modes.

**Division of responsibilities — AWS-specific rows**

These rows extend the shared table in [index.md §1.4](index.md#14-celerity-cli-build-manifest) with the Lambda-specific pieces:

| Responsibility | Owner | Notes |
|---|---|---|
| Generating the Lambda entry point file (`__celerity_lambda_entry__.{py,mjs,js}`) | CLI | Written into the shared `app.zip` at build time. |
| Generating the resource-links routing file (CLI-owned; currently `resource-links.json`) | CLI | Written into the shared `app.zip` at build time; read by the SDK from `/var/task/<routing-file>` at cold start. **Filename is CLI-owned and currently `resource-links.json`** — see the name-discrepancy note in [index.md §3.2](index.md#32-how-the-sdk-finds-values). |
| Deciding the entry-point string value | CLI | Recorded on `lambda.entryPoint`. The transformer reads it verbatim; it never computes or guesses this value. |
| Packaging the shared app code zip (`app.zip`) | CLI | Single zip shared by all handlers, recorded on `lambda.appCode`. |
| Building the shared Lambda layer | CLI | Recorded on `lambda.sharedLayer`. |
| Building per-handler custom dependency layers (when `handlerDependencies` is declared) | CLI | Recorded on `handlers[name].lambda.dependencies`. |
| Producing N distinct `aws/lambda/function` resources, one per `celerity/handler` | Transformer | All reference the same `lambda.appCode` asset (see §3). |
| Setting the Lambda `Handler` field on every function | Transformer | Always the constant from `lambda.entryPoint` (see §7). |
| Attaching the right dependency layer to each function | Transformer | Per-handler `lambda.dependencies` if set, otherwise `lambda.sharedLayer` (see §9). |

**Remote manifest URL (remote deploy engine)**

On a remote deploy engine, the CLI uploads the build manifest to the same S3 bucket it uses for artifact uploads and writes an `s3://bucket/key` URL into the `celerity.buildManifest` context variable. See [index.md §1.4](index.md#14-celerity-cli-build-manifest) for the shared context-variable contract and the transformer-side resolution responsibility.

- **URL format**: `s3://<bucket>/<instanceName>/build-manifest.json`. For example: `s3://celerity-build-123456789012-us-east-1/default/build-manifest.json`.
- **Upload path**: `celerity: apps/cli/internal/build/upload_s3.go` (`UploadManifest`). The key format is assembled by the same helper that names artifact keys, so the manifest lives as a sibling of the `app.zip` and layer zips in the same bucket.
- **Read credentials**: the transformer uses the same AWS credential chain as the deploy engine and the AWS provider plugin. The CLI does not embed credentials in the URL.
- **IAM requirement**: the operator identity running the transform needs `s3:GetObject` on the manifest key. In practice it is the same identity that already has `s3:GetObject` on the sibling artifact keys in the same bucket, so no new policy is usually needed — but if you are tightening the operator role, include the manifest key alongside the artifact keys in the allow-list.

On a local deploy engine the CLI does not upload the manifest; the context variable holds the local absolute path of `.celerity/build-manifest.json` and the transformer opens it directly.

**Module bootstrap: handled by the CLI-generated entry point**

Earlier drafts of the contract described a `CELERITY_MODULE_PATH` env var the SDK read at Lambda cold start to locate the user's root `@Module` / `@module` class. That indirection is no longer needed. The CLI now writes a generated `__celerity_lambda_entry__.{py,mjs,js}` file into the shared app zip that imports and bootstraps the user app directly, and records the entry point on `lambda.entryPoint`. Handler dispatch inside the bootstrapped app happens via the routing env vars described in [index.md §2.2](index.md#22-sdk-runtime-contract-shared-env-vars), not via a module-path lookup.

---

## 2. Deploy configuration: AWS-specific keys

**Source of truth:** `celerity: apps/cli/internal/deployconfig/convert.go`.

Beyond the shared keys in [index.md §1.5](index.md#15-celerity-cli-deploy-configuration), the `aws-serverless` target reads:

| Source | Key | Read via | Value, default |
|---|---|---|---|
| `deployTarget.config` keys matching `aws.lambda.*`, `aws.sqs.*`, `aws.sns.*`, `aws.dynamodb.*`, `aws.s3.*`, `aws.apigateway.*`, `aws.ecs.*`, `aws.eks.*` | unchanged dotted key | `TransformerConfigVariable("<dotted-key>")` | provider- and transformer-scoped config |
| `deployTarget.config["aws.config.replicateRegions"]` | `aws.config.replicateRegions` | same | comma-separated region list. Required when a `celerity/config` sets `replicate = true`; ignored otherwise. See §10. |
| `deployTarget.config["aws.config.regionKMSKeys.<region>"]` | `aws.config.regionKMSKeys.<region>` | same | KMS key ARN per region in `replicateRegions`. Required when `replicate = true`; supersedes the resource's `encryptionKeyId`. See §10. |
| `deployTarget.config["aws.config.useLambdaExtension"]` *(new, proposed)* | `aws.config.useLambdaExtension` | same | bool, default `false`. Only affects Secrets Manager namespaces. See §12. |

**There is no deploy-config key that selects a config-store backend, and there must not be one.** Backend choice derives from the `celerity/config` `plaintext` field ([index.md §1.3](index.md#13-celerityconfig-abstract-resource)), and the internal `resources` namespace always uses Parameter Store (§10.2). An earlier revision of this document proposed an `aws.configStore.*` prefix with `kind` and `encrypted` keys; both duplicated a decision the resource spec already makes, and the prefix itself collided with the real upstream `aws.config.*` namespace. They have been removed.

**Proposed CLI-side follow-up**: `aws.config.*` is already the documented deploy-config prefix for config stores (`celerity-docs: content/docs/framework/applications/resources/celerity-config.mdx`). Confirm it is routed to `Transformers["celerity"]` by the AWS transformer-config prefix list in `celerity: apps/cli/internal/deployconfig/convert.go` rather than dropped or misrouted as provider config, and add `aws.config.useLambdaExtension` to that list if it lands.

### 2.1 Backing-target deploy-config: the global + per-resource-override rule

Several backing targets take AWS-specific settings that are **not on the resource spec** — they exist only in the deploy configuration (message retention, DynamoDB billing mode, an S3 replication role, …). Every such setting shares one resolution shape, defined here once; the precise per-resource key → concrete-field maps live alongside each resource's spec-field map in [resource-mapping-aws-serverless.md](resource-mapping-aws-serverless.md).

Each setting has up to two key forms:

- **Global** — `aws.<svc>.<key>` — the default applied to every resource of that service.
- **Per-resource** — `aws.<svc>.<resourceName>.<key>` — overrides the global for one resource.

**Resolution: per-resource wins over global wins over the field's documented default.** Both forms are read via `TransformerConfigVariable("<dotted-key>")` (§2). `<resourceName>` is the resource's `spec.name`, or — when absent — its logical blueprint name, i.e. the same identifier the store/queue naming uses (§10.3). It is **not** the concrete emitted resource name (`<name>_sqs_queue`).

Not every service exposes both forms: SQS and S3 expose global **and** per-resource; DynamoDB is **per-resource only** (`aws.dynamodb.<datastore>.*`, no global). Each resource's deploy-config table notes which. A shared resolver (`aws.<svc>[.<name>].<key>` → value, with the override precedence above) is the single implementation point for all of these — the deploy-config pass builds it once and every backing target calls it.

**Settings are deploy-config-only, not spec fields.** When a value has *both* a spec field and a deploy-config key, that is called out explicitly; otherwise a value listed here has **no** spec-field source and deploy config is its only input (for example the S3 replication role).

**Not every key is the flat `aws.<svc>[.<name>].<key>` shape.** Two irregularities exist today, both on `celerity/topic` (see its deploy-config table): a fixed **infix segment** before the optional `<name>` (`aws.sns.fifo[.<topic>].messageRetentionPeriod`), and an **indexed array** form (`aws.sns[.<topic>].statusLogging.<i>.<field>`) where the per-resource form overrides the global by index rather than wholesale. The shared resolver must handle a configurable infix and an indexed-collection variant, not only the flat scalar key.

---

## 3. Concrete provider resource types

**Provider**: `bluelink-provider-aws` (v0.4.0 — *pending release*; the `aws/ssm/parameterTree` resource and its `function::parameterTree` link are committed post-0.3.1 but not yet tagged — confirm the version on release), which covers everything Celerity v0 needs. Most resources are CloudControl-generated under `services/cloudcontrol/gen/<service>_<resource>.go`; `aws/ssm/parameter`, `aws/ssm/parameterPath`, and `aws/ssm/parameterTree` are hand-written at `services/ssm/`. The names below are the **exact spec attribute keys** on those schemas.

### 3.1 Handler core

- `aws/lambda/function` — one per `celerity/handler` in the merged blueprint. Required: `code`, `role`. Every function references the **same shared `lambda.appCode` asset**; there is no per-handler code zip. Fields used: `functionName`, `handler` (the constant from `lambda.entryPoint`, see §7), `runtime` (§6), `code.s3Bucket` / `code.s3Key`, `memorySize`, `timeout`, `environment.variables` (§5), `layers[]` (layer ARN strings), `role` (role ARN), `tracingConfig.mode` (`Active` | `PassThrough`), `vpcConfig.subnetIds` / `vpcConfig.securityGroupIds`, and `tags` — which is **a list of `{key, value}` objects, not a map**. Output: `arn`.
- `aws/iam/role` — the Lambda execution role, shared across handlers by policy fingerprint (§8). Required: `assumeRolePolicyDocument`. Fields: `assumeRolePolicyDocument`, `managedPolicyArns[]` (strings), and `policies[]` — **a list of `{policyName, policyDocument}` objects, not a map keyed by name**. Output: `arn`.
- `aws/lambda/layerVersion` — one per distinct `contentHash` across `lambda.sharedLayer` and per-handler `lambda.dependencies` (§9). Required: `content` (whose `content.s3Bucket` and `content.s3Key` are both required). Fields: `compatibleRuntimes[]`. Output: **`layerVersionArn`** — this resource has no `arn` attribute.

### 3.2 Event sources

- `aws/events/rule` — the EventBridge rule backing a `celerity/schedule` handler. Fields: `scheduleExpression`, `targets[]` (**inline on the rule**; each target requires `id` and `arn`, with optional `input`, `roleArn`), `state` (`ENABLED` | `DISABLED` | …), `eventBusName`, `eventPattern`. Output: `arn`. There is **no separate target resource**, and **no `aws/scheduler/*`** — scheduling is available only via `aws/events/rule`'s `scheduleExpression`.
- `aws/lambda/permission` — grants a **push** source (EventBridge, API Gateway) permission to invoke a function. Required: `action`, `functionName`, `principal`. Fields: `sourceArn`. `functionName` accepts either a bare function name or an ARN. Output: `id`.
- `aws/lambda/eventSourceMapping` — **poll** sources (SQS, DynamoDB Streams). Poll sources need **no** `aws/lambda/permission`; the source-read grants live on the execution role instead. Required: `functionName`. Fields: `eventSourceArn`, `batchSize`, `functionResponseTypes[]` (set to `ReportBatchItemFailures` for partial-batch failures), `startingPosition` (`LATEST` | `TRIM_HORIZON` | `AT_TIMESTAMP`), `enabled`, `maximumBatchingWindowInSeconds`, `destinationConfig.onFailure.destination`. Outputs: `id`, `eventSourceMappingArn`.
- `aws/apigatewayv2/{api,integration,route,stage}` — HTTP and WebSocket APIs. `api`: `name`, `protocolType` (`HTTP` | `WEBSOCKET`), `routeSelectionExpression`; outputs `apiId`, `apiEndpoint` — **no `executionArn`** (see the caveat below). `integration`: required `apiId`, `integrationType` (e.g. `AWS_PROXY`); plus `integrationUri`, `payloadFormatVersion`; output `integrationId`. `route`: required `apiId`, `routeKey`; plus `target`; output `routeId`. `stage`: required `apiId`, `stageName`; plus `autoDeploy`; **no computed outputs**.

> **API Gateway caveat**: `aws/apigatewayv2/api` exposes only `apiId` and `apiEndpoint`. A Lambda permission for API Gateway conventionally scopes `sourceArn` to the API's *execution* ARN (`arn:aws:execute-api:<region>:<account>:<apiId>/*`), which the provider does not expose as an output. The transformer must therefore compose that ARN from `apiId`, or scope the permission more loosely. Resolve this before implementing the API event source.

### 3.3 Configuration store (§10)

- `aws/ssm/parameter` — required `name`, `type` (`String` | `StringList` | `SecureString`). A `SecureString` **must** set `secureValue` (sensitive), **not** `value`; `String`/`StringList` use `value`. Also `tier`, `keyId`. Outputs: `arn`, `version`.
- `aws/ssm/parameterPath` — a synthetic resource representing a **parameter hierarchy prefix**, not an AWS object. Required `path` (starts with `/`, no trailing slash, ≤14 hierarchy levels, must not begin `/aws` or `/ssm`; `MustRecreate` on change). Its `IDField` is `path`. A pure link handle for externally-managed prefixes; **not emitted for config stores** (superseded by `aws/ssm/parameterTree` — see below).
- `aws/ssm/parameterTree` — a synthetic resource that **owns a prefix-scoped tree of parameters as a single store**. Required `path` (`MustRecreate`, same rules as `parameterPath`). Entries split across `values` (map string→string → `String` parameters) and `secureValues` (map string→string → `SecureString`, `Sensitive`); one real SSM parameter is created per entry at `<path>/<key>`. Also `keyId` (KMS for `secureValues`), `tier`, `description`, `tags` (map), optional `region`. Stored values carry **blob-like drift semantics** (`values`/`secureValues` are `IgnoreDrift` and never read back), so runtime CLI overrides survive redeploys. Computed output `parameters` (map key→`{arn, type, valueHash}`) — never the values themselves. This is the store a config-store namespace's parameters nest beneath, and the resource a handler links to; see §10 and §11.
- `aws/secretsmanager/secret` — `name`, `secretString` (sensitive), `kmsKeyId`. Output: **`id`**, which holds the secret ARN — there is no `arn` attribute on this resource.

### 3.4 Backing resources for linked sources

`aws/sqs/queue` (outputs `queueUrl`, `arn`), `aws/dynamodb/table` (input `streamSpecification.streamViewType`; outputs `arn` and `streamArn`), `aws/sns/topic`, `aws/sns/subscription`, `aws/sqs/queueInlinePolicy`, `aws/ec2/{vpc,subnet,securityGroup}`, `aws/elasticache/*`, `aws/rds/*`, `aws/s3/bucket`.

Cross-resource references use bluelink substitution strings: the Lambda `role` field is `${resources.<roleResourceName>.spec.arn}`, `layers[]` entries are `${resources.<layerResourceName>.spec.layerVersionArn}`, and an EventBridge target `arn` is `${resources.<lambdaResourceName>.spec.arn}`.

### 3.5 Emit resources, declare links — the provider does the wiring

`bluelink-provider-aws` ships **implemented links between concrete resources**. Those links perform the wiring that a naive transformer would hand-emit. The transformer's job is to emit the *nodes* and declare the *edges*; the provider supplies the plumbing.

**The transformer emits:**

| Resource | Notes |
|---|---|
| `aws/lambda/function` | **without** `vpcConfig` — the VPC link sets it |
| `aws/iam/role` | base role only, **with `roleName`** (§8) |
| `aws/lambda/layerVersion` | deduped by `contentHash` (§9) |
| `aws/events/rule` | **with `targets[].arn` = `${<lambda>.spec.arn}`** — that reference *activates* the rule→function link (`ActivatesLinkOnReference`), so no `linkSelector` is needed |
| `aws/apigatewayv2/api` **and** `aws/apigatewayv2/stage` | the API link creates integration/route/permission but **not** the stage |
| `aws/flex/vpc` | from `celerity/vpc`; `preset` passes through verbatim |
| source resources | `aws/sqs/queue`, `aws/dynamodb/table`, `aws/sns/topic`, `aws/s3/bucket`, `aws/ssm/parameterTree`, `aws/secretsmanager/secret`, `aws/elasticache/replicationGroup`, `aws/rds/dbCluster`, … |

**The transformer must NOT emit** — the links own these, and hand-emitting them causes double-management:

- `aws/lambda/permission` — created by `events/rule::function`, `apigatewayv2/api::function`, and `apigatewayv2/authorizer::function`.
- `aws/lambda/eventSourceMapping` **for in-blueprint poll sources** — when the source (`aws/sqs/queue`, `aws/dynamodb/table`, `aws/kinesis/stream`) is defined in the blueprint, its poll-source link (`sqs/queue::function`, `dynamodb/table::function`, `kinesis/stream::function`) owns the mapping via a direct Lambda SDK call, so the transformer must not emit one. **Exception:** an external event source with no in-blueprint resource — an `externalEvents` `dbStream`/`dataStream`, or a raw external SQS `sourceId` (URL or ARN) — has no link to own the mapping, so the transformer *does* emit a standalone `aws/lambda/eventSourceMapping` for it (with the queue URL normalised to its ARN).
- `aws/apigatewayv2/{integration,route,integrationResponse,routeResponse}` — created by `apigatewayv2/api::function`.
- **Per-link IAM statements** — injected into the execution role by the links (§8).
- **Per-link environment variables** — the outbound links inject them live (`SQS_QUEUE_<name>`, `SNS_TOPIC_<name>`, `DYNAMODB_TABLE_<name>`, `SSM_PARAMETER_<name>`, `SECRET_<name>`), renameable via each link's `envVarName` annotation.
- `vpcConfig` on the function — set by `flex/vpc::function` from the flex VPC's computed subnets (selected by tier) and its managed security group.
- `streamSpecification` on a DynamoDB table used as an event source — the `dynamodb/table::function` link enables streams itself via `UpdateTable`.

**Celerity annotations map onto AWS link annotations.** The transformer's remaining work for links is translation:

| Celerity | AWS link annotation |
|---|---|
| `celerity.handler.http.method` + `celerity.handler.http.path` | `aws.apigatewayv2.lambda.routeKey` (e.g. `"GET /orders"`) |
| `celerity.handler.websocket.route` | `aws.apigatewayv2.lambda.routeKey` (`$connect`, `$disconnect`, `$default`, …) |
| `celerity.handler.guard.protectedBy` / `celerity.handler.guard.custom` | `aws.apigatewayv2.lambda.authorizerId` + `aws.apigatewayv2.lambda.authorizationType` |
| consumer `batchSize` | `aws.sqs.lambda.batchSize` / `aws.dynamodb.lambda.stream.batchSize` |
| consumer `partialFailures` | `aws.sqs.lambda.reportBatchItemFailures` **and** `aws.dynamodb.lambda.stream.reportBatchItemFailures` (both supported since provider P1) |
| consumer `startFromBeginning` | `aws.dynamodb.lambda.stream.startingPosition` (`TRIM_HORIZON` \| `LATEST`) |
| `celerity.handler.vpc.subnetType` | `aws.flexvpc.lambda.subnetType` (`public` \| `private`, default `private`) |
| config-store access (§10) | `aws.lambda.ssm.<NS>.envVarName = CELERITY_CONFIG_<NS>_STORE_ID`, `…accessLevel = read` — declared on the `aws/ssm/parameterTree` link (§11) |
| outbound resource access level | `aws.lambda.sqs.<queue>.accessLevel` (`send`\|`receive`\|`sendReceive`), `aws.lambda.dynamodb.<table>.accessLevel` (`read`\|`write`\|`readwrite`), etc. |

> **Caveats.** (1) ~~The DynamoDB event-source link exposes no `reportBatchItemFailures` annotation~~ — **resolved (provider P1):** `aws.dynamodb.lambda.stream.reportBatchItemFailures` → `FunctionResponseTypes=[ReportBatchItemFailures]`, so `partialFailures` is now honoured for datastore streams. (2) SNS outbound has no `accessLevel` — it always grants `sns:Publish`. (3) `aws/apigatewayv2/api` has no `executionArn` output, but this is a non-issue: the API link composes the `execute-api` ARN itself when creating its permission. (4) The route/auth annotations (`routeKey`, `authorizerId`, `authorizationType`) and the consumer/VPC annotations are stamped on the **function** (resourceB of each link), not the source resource.

---

## 4. Framework annotation category assignment

Per [index.md §2.1](index.md#21-framework-grouping-annotations), every emitted resource must carry `resourceCategory` as either `code-hosting` or `infrastructure`. Phase 1 assignment for `aws-serverless`:

| Emitted resource | Category constant | Category value | Rationale |
|---|---|---|---|
| `aws/lambda/function` | `ResourceCategoryCodeHosting` | `code-hosting` | Code changes may qualify for auto-approval. |
| `aws/lambda/layerVersion` | `ResourceCategoryCodeHosting` | `code-hosting` | Dependencies are part of the shipped code package. |
| `aws/iam/role` | `ResourceCategoryInfrastructure` | `infrastructure` | Policy changes escalate to manual review. |
| `aws/events/rule` | `ResourceCategoryInfrastructure` | `infrastructure` | A schedule trigger gates code execution; it is the gate, not the gated code. |
| `aws/apigatewayv2/api` | `ResourceCategoryInfrastructure` | `infrastructure` | Network-facing surface; changes escalate to manual review. |
| `aws/apigatewayv2/stage` | `ResourceCategoryInfrastructure` | `infrastructure` | Deployment surface for the API. |
| `aws/flex/vpc` | `ResourceCategoryInfrastructure` | `infrastructure` | Networking topology. |
| source resources (`aws/sqs/queue`, `aws/dynamodb/table`, `aws/sns/topic`, `aws/ssm/parameterTree`, `aws/secretsmanager/secret`, …) | `ResourceCategoryInfrastructure` | `infrastructure` | Stateful/infrastructure dependencies. |

Resources created by **provider links** (`aws/lambda/permission`, `aws/apigatewayv2/integration` / `route` / `integrationResponse` / `routeResponse`) are not emitted by the transformer and therefore carry no framework annotations from it — the link owns their lifecycle (see §3.5).

---

## 5. SDK runtime: AWS-specific env vars

Beyond the shared cross-target env vars documented in [index.md §2.2](index.md#22-sdk-runtime-contract-shared-env-vars), the transformer injects the following on `aws-serverless` deployments. The legend (✅ / 🔗 / 🧑 / ⛔) is the same as in the parent document.

### 5.1 Bootstrap (AWS-specific)

| Env var | Injected by transformer | Source, value | Purpose |
|---|---|---|---|
| `CELERITY_PLATFORM` | ✅ always | literal `"aws"` when `deployTarget` is `aws` or `aws-serverless` | Selects the AWS provider wiring in the SDK bootstrap. |
| `CELERITY_MODULE_PATH` | ⛔ never | (unset) | Earlier drafts derived this from `spec.codeLocation` so the SDK could locate the user's root module at cold start. That indirection has been removed: the CLI now ships a generated `__celerity_lambda_entry__.{py,mjs,js}` bootstrap file that imports the user app directly (see §1, §7). |

### 5.2 AWS SDK configuration (read by resource clients)

These are the standard AWS SDK vars plus Celerity endpoint overrides for local testing. **The transformer never sets any of them on production Lambda**. The Lambda execution environment already provides `AWS_REGION`, the IAM role provides credentials, and endpoint overrides are dev-only.

| Env var | Injected by transformer | Purpose |
|---|---|---|
| `AWS_REGION`, `AWS_DEFAULT_REGION` | ⛔ never | Provided by the Lambda runtime environment. |
| `AWS_ENDPOINT_URL`, `CELERITY_AWS_SQS_ENDPOINT`, `CELERITY_AWS_SNS_ENDPOINT`, `CELERITY_AWS_DYNAMODB_ENDPOINT`, `CELERITY_AWS_S3_ENDPOINT`, `CELERITY_AWS_S3_PATH_STYLE` | ⛔ never | Local-testing overrides only. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | ⛔ never | Provided by the IAM role's credential chain. |
| `PARAMETERS_SECRETS_EXTENSION_HTTP_PORT` | 🔗 on link (future) | Only when `aws.config.useLambdaExtension = true` **and** the handler reads a `secrets-manager` namespace, see §12. |

---

## 6. Runtime identifier mapping (`aws-serverless`)

See [index.md §2.3](index.md#23-runtime-identifier-mapping-concept) for the shared concept and unknown/unmapped-identifier error handling. The mapping is an **exact lookup** (`resources/handler/runtimes.go`, `getTargetRuntime`) — there is no nearest-version fallback; an identifier absent from the table is an error and no function is emitted. The mapping table below writes its result into the `runtime` field on every emitted `aws/lambda/function` resource.

| Celerity runtime identifier | AWS Lambda `runtime` value | Notes |
|---|---|---|
| `nodejs24.x` | `nodejs24.x` | Identity mapping: the Celerity identifier and the AWS Lambda runtime string happen to match for Node.js today. |
| `python3.13.x` | `python3.13` | The trailing `.x` suffix is dropped. |

The list grows as Celerity adds support for new runtimes. Adding a row here is the single maintenance point when a new Celerity runtime is released and its AWS Lambda target value is known.

**Forward reference: per-handler override**

The upstream spec describes a future `aws.lambda.<handlerName>.runtime` override in the app deploy configuration that lets authors pin a specific AWS Lambda runtime value for a single handler, bypassing the mapping. This is planned for v1 and not available today. When it lands, the transformer reads the override via `TransformerConfigVariable` and uses it in place of the mapping result for the named handler only; the exact-lookup rule and unknown/unmapped-identifier error handling in [index.md §2.3](index.md#23-runtime-identifier-mapping-concept) still apply to everything else.

**Not part of Phase 1**

Expanding the mapping table to cover additional language tracks (Go, Java, C#, OS-only `os.*` identifiers) tracks the upstream spec. Each addition lands in its own PR that simultaneously updates the table above and the supporting code, per the maintenance rule in [index.md §5](index.md#5-versioning-and-maintenance).

---

## 7. Lambda `Handler` field: read verbatim from the build manifest

A subtlety easy to miss: the Lambda runtime entry point is **the CLI-generated bootstrap file**, not the user's `spec.handler`. `celerity build` writes a `__celerity_lambda_entry__.{py,mjs,js}` file into the shared `app.zip`; that file imports and wraps the user app in the Celerity SDK serverless adapter. At cold start, Lambda invokes the bootstrap, the bootstrap hands the event to the adapter, and the adapter reads `CELERITY_HANDLER_ID`, `CELERITY_HANDLER_TYPE`, and optional `CELERITY_HANDLER_TAG` (see [index.md §2.2](index.md#22-sdk-runtime-contract-shared-env-vars)) to route the event to the correct decorated user handler.

**Contract**: the transformer sets the Lambda `Handler` field on every emitted function to the constant string read from `manifest.lambda.entryPoint`. It does **not** compute, guess, or transform this value. It is identical across every handler in a project because `__celerity_lambda_entry__` is identical across every handler — the differentiator between functions is the routing env vars, not the `Handler` field.

A missing or unavailable build manifest (and hence a missing `lambda.entryPoint`) is **not fatal**: per the build-manifest fallback contract ([index.md §1.4](index.md#14-celerity-cli-build-manifest)), the transformer emits the function **without** its `code` asset and with an empty `Handler`, attaches a per-handler warning diagnostic (`resources/handler/handler_aws_serverless_emit.go`, `loadCodeLocationInfo`), and relies on downstream validation to reject the output unless the deploy is a dry run/plan. This preserves lint/plan before `celerity build` has run.

| Celerity runtime identifier | `lambda.entryPoint` value observed today | Bootstrap file location in deployment package |
|---|---|---|
| `nodejs*` | `__celerity_lambda_entry__.handler` | `/var/task/__celerity_lambda_entry__.mjs` (or `.js`), generated by the CLI |
| `python*` | `__celerity_lambda_entry__.handler` | `/var/task/__celerity_lambda_entry__.py`, generated by the CLI |
| unmapped runtime | error diagnostic — unsupported runtime; no function emitted (see §6) | (none) |

The column values above are the CLI's current output, not contract guarantees the transformer enforces. The transformer reads whatever string the CLI wrote and trusts it; changes to bootstrap filename or handler symbol are CLI-side refactors that do not require transformer updates, provided `lambda.entryPoint` stays accurate.

---

## 8. IAM execution role: shared by link-set fingerprint

**The provider's links grant IAM, not the transformer.** Every `aws/lambda/function::<target>` link and every poll-based event-source link injects its own resource-scoped statement into the referenced execution role at deploy time. `linkutils.ReconcileRoleAccessPolicy` packs a `Sid`-keyed statement (e.g. `SQSAccess<queueName>`, `DynamoDBStreamRead<tableName>`) into a Bluelink-managed inline policy named `bluelink-link-access` (~9 KB budget), overflowing into at most five managed policies `bluelink-link-access-<n>` (~5.5 KB each). The read-modify-write is serialised by a **per-role lock** and attributed back through `ResourceDataMappings`, so the engine does not see it as drift. **The transformer therefore never emits per-link inline policies.**

**Hard requirement**: the function's `role` must resolve to an `aws/iam/role` resource **in the same blueprint**, and that resource **must set `roleName`**. The links derive the role name from the function's live role ARN and look the resource up in blueprint state by it. A role without `roleName` fails *every* link at deploy.

Because links inject grants into whichever role a function references, two handlers sharing a role accumulate each other's grants. The transformer therefore shares a role only between handlers whose **link sets are identical**, deduplicating by a **link-set fingerprint**.

**Fingerprint inputs** — stable JSON encoding, SHA-256, truncated to the first 8 hex characters:

- The **sorted set of `(linkType, targetResourceName)` pairs** for every link the handler declares — outbound `function::*` links and inbound event-source links alike.
- `tracingEnabled` (bool).
- VPC placement: whether a `aws/flex/vpc::aws/lambda/function` link is declared, and its `subnetType`.

Handlers with identical link sets share one `aws/iam/role`, and the union of injected grants is exactly what each of them needs — so least privilege holds. Handlers touching different resources get different roles rather than silently inheriting each other's access. Role resource naming follows `celerityLambdaExec_<fingerprint>`.

Note the fingerprint no longer computes or deduplicates policy *content* (the links own that). It exists purely to decide **which handlers may share a role resource**.

**The emitted role spec is the base role only** — links supply everything else:

- `roleName` — required, see above.
- `assumeRolePolicyDocument` trusting `lambda.${providerDomain(deployTarget)}` (the helper hides the literal `amazonaws.com` so future partitions `aws-us-gov` / `aws-cn` slot in). The provider's policy-document schema is a **structured object with lowercase keys**: `{version, statement: [{sid?, effect, action, resource, principal}]}`, where `principal` is a string or `{service, aws, federated, canonicalUser}`. It is **not** a JSON string, and **not** CFN-style `Version`/`Statement`/`Effect`.
- `managedPolicyArns: ["arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"]` for CloudWatch Logs.
- `policies` — a **list of `{policyName, policyDocument}` objects**, not a map keyed by name. Used only for grants no link provides: the `celerity-xray` policy (`xray:PutTraceSegments`, `xray:PutTelemetryRecords` on `"*"`) when `tracingEnabled`. The allocator's `bluelink-link-access` policy coexists in this list without conflict.

**Framework annotations**: `AbstractResourceType = celerity/handler`; `AbstractResourceName` is the resource name of the first handler that requested the role; a supplementary annotation `celerity.handler.sharedBy` (comma-separated list) records every handler using this role for traceability; `resourceCategory = infrastructure` so role changes escalate to manual review in code-only approval.

---

## 9. Lambda layers

The build manifest's `lambda.sharedLayer` and per-handler `handlers[name].lambda.dependencies` artifacts are deduplicated by `contentHash` and emitted as `aws/lambda/layerVersion` resources. Each distinct hash becomes one resource shared by every handler that resolves to it.

**Per-handler layer-selection rule:**

1. If `handlers[name].lambda.dependencies` is non-null for the handler, the transformer attaches the `layerVersion` derived from that custom layer to the handler's Lambda function.
2. Otherwise, it falls back to the `layerVersion` derived from `lambda.sharedLayer`.
3. If neither exists (project with zero production dependencies), the Lambda is emitted with no `layers[]` entries and runs with only the shared app code asset.

Because deduplication is by `contentHash`, two handlers that happen to produce byte-identical custom layers share the same emitted `aws/lambda/layerVersion` resource, and a handler whose custom layer happens to match the shared layer byte-for-byte collapses onto the shared layer instead of producing a separate resource.

- Resource name: `celerityLambdaLayer_<contentHash>` — the **full** layer content hash (`resources/handler/handler_aws_serverless_shared_parents.go`, `lambdaLayerResourceName`), not a truncation. (The 8-hex-character truncation is used only for the IAM role fingerprint, `celerityLambdaExec_<fingerprint>`, in §8.)
- Fields populated: `content.s3Bucket`, `content.s3Key`, and `compatibleRuntimes` (the union of the runtimes of handlers that reference the layer).
- Framework annotations: `AbstractResourceType = celerity/handler`; `AbstractResourceName` recorded similarly to shared roles; `resourceCategory = code-hosting` because dependencies are part of the shipped code package and should flow through the same auto-approval path as code changes.

---

## 10. Runtime configuration store: AWS backends

See [index.md §3.1](index.md#31-interface-abstraction) for the shared interface abstraction, store purposes, and the two backend *shapes*; [index.md §3.2](index.md#32-how-the-sdk-finds-values) for how the SDK finds values; and [index.md §1.3](index.md#13-celerityconfig-abstract-resource) for the `celerity/config` spec that drives backend selection. This section documents the AWS-specific mapping.

### 10.1 Backend choices

Two managed-store backends on `aws-serverless`:

| Backend | `..._STORE_KIND` value | Shape | Storage | When chosen |
|---|---|---|---|---|
| AWS Secrets Manager | `"secrets-manager"` | single-object encrypted blob | one `aws/secretsmanager/secret` holding a JSON-encoded key/value map | A `celerity/config` whose `plaintext` is empty or absent — every value is a secret. |
| AWS Systems Manager Parameter Store | `"parameter-store"` | prefix-scoped parameter tree | one `aws/ssm/parameterTree` owning one parameter **per key** beneath its `path` prefix | A `celerity/config` with at least one `plaintext` key, and always for the internal `resources` namespace (§10.2). |

`"secrets-manager"` is the SDK's default when a namespace's `_STORE_KIND` is unset, so the transformer sets it explicitly on every namespace rather than relying on the default.

**Parameter Store is a single `aws/ssm/parameterTree`, not one resource per key.** The store is emitted as one `aws/ssm/parameterTree` whose `path` is the prefix; the provider fans that out into one real SSM parameter per key at `<path>/<key>` on deploy, and the SDK enumerates them at runtime with `GetParametersByPath` (`Recursive: true`, `WithDecryption: true`). A handler links to the **tree**, once. The earlier model — an `aws/ssm/parameterPath` handle plus one `aws/ssm/parameter` per key — is superseded (`aws/ssm/parameterPath` remains available for externally-managed prefixes but is no longer emitted for config stores). Every consequence below follows from the tree being a single store resource.

**Blob-like drift is the point.** The tree treats its stored values as an opaque blob: values are applied on create and on explicit blueprint change, but out-of-band writes are **never reported or reverted as drift**. This is what lets the Celerity CLI (`celerity config set`) write config/secret values directly to the store at runtime without the next deploy clobbering them — bringing Parameter Store in line with the Secrets Manager backend, whose `secretString` already behaves this way. Provider-side this is achieved by marking the tree's `values`/`secureValues` `IgnoreDrift` and never reading values back in `GetExternalState`; explicit-change detection uses a per-key `valueHash` on the computed `parameters` output rather than the value itself.

**Per-key encryption.** Keys split across two maps on the tree, which selects the backing SSM parameter type:

| Key sensitivity | Tree field | SSM parameter `type` |
|---|---|---|
| Listed in `plaintext` (or a non-sensitive auto-populated identifier) | `values` | `String` |
| Everything else | `secureValues` (`Sensitive`, redacted from diffs/logs) | `SecureString` |

`WithDecryption: true` is a no-op on `String` parameters, so the SDK reads both without branching. `SecureString` values use the tree's `keyId` (from the config's `encryptionKeyId`) when set and the AWS-managed `alias/aws/ssm` key otherwise — see §11 for the IAM consequence of choosing a customer-managed key. `keyId` applies only when at least one `secureValues` entry exists.

There is no env-var backend on `aws-serverless` (or on any other supported target). Lambda's 4 KB env-var cap makes it unworkable as a general-purpose config store, and the routing metadata that used to justify an env-var-based path now ships as a build-bundled file inside `app.zip` (see §1 and [index.md §3.2](index.md#32-how-the-sdk-finds-values)).

### 10.2 The internal `resources` namespace

The transformer emits a **dedicated store** for the internal resource-links namespace, separate from any `celerity/config` the author declares ([index.md §3.1](index.md#31-interface-abstraction)). It is always **Parameter Store**, and this is derived rather than configured.

**Why Parameter Store, always.** The namespace holds a mix. Most entries are physical identifiers — a queue URL, a table name, a bucket name — that are already visible in the blueprint, in bluelink state, and in the deploy plan; encrypting them buys nothing and costs plan legibility, because `aws/ssm/parameter.secureValue` is a `Sensitive` field and is redacted from diffs and logs. But some links auto-populate credentials, which is precisely what `celerity/config`'s `rotation` field exists to rotate. Only the per-key backend can hold both honestly. Applying the same `plaintext`-derived rule the author's stores obey (§10.1), a namespace with at least one non-sensitive key takes Parameter Store — and the `resources` namespace always has one.

The transformer decides each key's sensitivity, since it populates them: a physical identifier goes in the tree's `values` map (`String`), an auto-populated credential in `secureValues` (`SecureString`). There is no author-facing switch and no deploy-config override.

**Emitted resources.** One `aws/ssm/parameterTree` (concrete name `celerityResourcesConfigStore`, `transformer/resources_store.go`) for the namespace; its `path` is the store prefix `/celerity/<appName>/resources`. It carries a `values` map with one entry per backing resource a handler links to, keyed by **configKey** and valued by a substitution over the concrete resource's physical-id output: `queue → queueUrl`, `topic → topicArn`, `datastore → tableName`, `bucket → bucketName`. **`cache` and `sqlDatabase` are excluded** — their connection details reach handlers via per-link env vars (see the cache/sqlDatabase mappings in [resource-mapping-aws-serverless.md](resource-mapping-aws-serverless.md)), not the store. The `configKey` is the linked resource's `spec.name`, falling back to its blueprint logical name (matching the CLI's routing-file derivation). The store is emitted only when at least one handler links a store-backed resource; otherwise none is produced. The tree's blob-drift posture suits this namespace: a changed physical identifier (a recreated queue's URL) flows through as an explicit blueprint change while out-of-band writes are not reverted.

**Env vars and IAM — set directly on the handler, not via a link.** The internal store is a **shared-parent resource**, which cannot carry a link-selector label, so — unlike a user `celerity/config` store (§11) — no handler declares an `aws/lambda/function::aws/ssm/parameterTree` link to it. Instead the transformer wires each qualifying handler (one that links at least one store-backed resource) directly:

- it sets `CELERITY_CONFIG_RESOURCES_STORE_ID` to the store's path-prefix **literal** and `CELERITY_CONFIG_RESOURCES_STORE_KIND = "parameter-store"` on the function (`shared/awslambda/env.go`);
- it grants the handler's execution role a direct, scoped SSM-read policy `celerity-resource-links-store` (`shared/awslambda/iam_planner.go`): `ssm:GetParametersByPath`, `ssm:GetParameters`, `ssm:GetParameter` on `arn:aws:ssm:*:*:parameter<path>` **and** `arn:aws:ssm:*:*:parameter<path>/*`.

It must **not** set `CELERITY_CONFIG_STORE_ID` — see the warning in [index.md §2.2](index.md#22-sdk-runtime-contract-shared-env-vars), which would silently collapse namespace discovery to a single `default` namespace and strip resource-link resolution from every handler. At runtime the SDK still reads the `STORE_ID` path via `GetParametersByPath`, instantiates the backend named by `STORE_KIND`, and resolves each routing-file entry's `configKey` against the returned map.

This is unconditional: no author action is required for projects of any size, and an application needs no `celerity/config` resource to have working resource links.

### 10.3 User-defined `celerity/config` stores

**Store naming / SSM path.** The Parameter Store backend emits its `aws/ssm/parameterTree` at the prefix `/celerity/<appName>/<configName>`, where `<appName>` is the `celerity.appName` context variable ([index.md §1.5](index.md#15-celerity-cli-deploy-configuration)) and `<configName>` is the config resource's `spec.name`, or — when that is absent — the config resource's logical name in the blueprint (unique within the blueprint, so multiple unnamed config stores don't collide). The Secrets Manager backend uses the same `<appName>`/`<configName>` derivation for the secret name. In a validation context where `appName` is not yet available, the transformer uses a placeholder segment.

The backend follows `plaintext` per §10.1. Two AWS-specific rules come from the upstream resource spec:

- **`replicate = true`** requires `aws.config.replicateRegions` in the deploy configuration (§2). Secrets Manager replicates natively; Parameter Store has no built-in cross-region replication, so replication is realised as per-region parameter copies. This is **not yet emitted** — the transformer reports an error diagnostic for `replicate: true` on `aws-serverless` (single-region stores only), pending the tree's per-region fan-out (the `aws/ssm/parameterTree` carries a `region` field for exactly this future use).
- **`encryptionKeyId`** must be a KMS key ARN in the same region as the store. When `replicate = true` it is **ignored**, and per-region keys must be supplied via `aws.config.regionKMSKeys.<region>` instead.

Secrets Manager's maximum secret size is 64 KB. A store that exceeds it fails at deploy; the upstream guidance is to split across multiple `celerity/config` resources.

### 10.4 Routing-file schema on `aws-serverless`

Per [index.md §3.2](index.md#32-how-the-sdk-finds-values), the routing map for the internal `resources` namespace ships in the CLI-owned routing file (currently `resource-links.json` — see the name-discrepancy note in [index.md §3.2](index.md#32-how-the-sdk-finds-values)) inside the shared `app.zip` (see §1). The schema is backend-agnostic, because the store-lookup step happens at value time, not routing time:

```json
{
  "orders-queue":  { "type": "queue",     "configKey": "orders-queue" },
  "events-topic":  { "type": "topic",     "configKey": "events-topic" },
  "orders-db":     { "type": "datastore", "configKey": "orders-db" }
}
```

At cold start the SDK reads the file, instantiates the `parameter-store` backend named by `CELERITY_CONFIG_RESOURCES_STORE_KIND`, enumerates every parameter beneath the path prefix in `CELERITY_CONFIG_RESOURCES_STORE_ID` via `GetParametersByPath`, and looks up each entry's `configKey` in the resulting `key → value` map. A key's `configKey` is its parameter name relative to the prefix.

---

## 11. IAM implications (config store)

**The transformer emits no config-store IAM policy for user `celerity/config` stores.** Access is granted by declaring the outbound link from the handler's function to the store resource; the link injects the statement into the execution role (§8) and injects the env var pointing at the store (§3.5).

> **Exception — the internal `resources` store.** The internal resource-links store (§10.2) is a shared-parent `aws/ssm/parameterTree` with no link-selector label, so no link can grant its access. For that store alone the transformer **does** emit a direct scoped SSM-read policy (`celerity-resource-links-store`) onto each qualifying handler's execution role and sets the `CELERITY_CONFIG_RESOURCES_STORE_ID`/`_KIND` env vars as literals — see §10.2. Everything below concerns user `celerity/config` stores, which use the link mechanism.

| Backend | Link to declare | Link annotations (`<NS>` = the store's resource name) |
|---|---|---|
| `parameter-store` | `aws/lambda/function::aws/ssm/parameterTree` | `aws.lambda.ssm.<NS>.accessLevel = read` (grants `ssm:GetParameter`, `ssm:GetParameters`, `ssm:GetParametersByPath` over the whole path), `aws.lambda.ssm.<NS>.envVarName = CELERITY_CONFIG_<NS>_STORE_ID` |
| `secrets-manager` | `aws/lambda/function::aws/secretsmanager/secret` | `aws.lambda.secretsmanager.<NS>.accessLevel = read` (grants `secretsmanager:GetSecretValue`, `secretsmanager:DescribeSecret`), `aws.lambda.secretsmanager.<NS>.envVarName = CELERITY_CONFIG_<NS>_STORE_ID` |

The env var the link injects carries the store identifier. For Parameter Store that is the **path prefix** (the `parameterTree.path`); for Secrets Manager it is the secret **ARN** (`$.id` — this resource has no `arn` attribute). Either way **one link is one store**: the tree represents the whole namespace, so a handler declares a single link regardless of how many parameters sit beneath the path. The tree link's `envVarName` defaults to `SSM_PARAMETER_PATH_<NS>` (matching the parameter-path link's convention), but the transformer sets it explicitly to `CELERITY_CONFIG_<NS>_STORE_ID` — the name the SDK runtime contract requires (§5). `CELERITY_CONFIG_<NS>_STORE_KIND` remains a transformer-injected literal (§5), since no link supplies it.

**How the Parameter Store grant is scoped** (`bluelink-provider-aws: inter-service-links/lambda_ssm/function__parameter_tree_link_update.go`). The link builds one statement (SID `SSMTreeAccess<NS>`) whose `Resource` is `[<pathARN>, <pathARN>/*]` — the bare path ARN authorises `ssm:GetParametersByPath` over the hierarchy, and the `/*` wildcard authorises per-parameter reads beneath it. The path ARN is derived from the function's ARN (same partition and account) plus the region and `path`, since the tree is a synthetic prefix owner with no ARN of its own. One statement covers the entire namespace no matter how many keys it holds, so the role's statement count is O(namespaces), not O(parameters) — which matters against the shared role's inline-policy budget (§8).

Because the config-store link is part of a handler's link set, it participates in the role fingerprint (§8) automatically: handlers reading the same store share a role, and handlers with no config store keep the lean base role.

**Customer-managed KMS keys — the transformer's obligation, unenforced by the provider.** `SecureString` parameters and Secrets Manager secrets encrypted with the AWS-managed key (`alias/aws/ssm`, `alias/aws/secretsmanager`) need no extra grant — the managed key's policy authorises the account through the calling service. The moment a `celerity/config` sets `encryptionKeyId`, or `aws.config.regionKMSKeys.<region>` supplies a key, every reader additionally needs `kms:Decrypt` on that key, and the transformer must also declare `aws/lambda/function::aws/kms/key` on each handler that reads the store.

Neither config-store link checks for this. The `parameterTree` link cannot see the encryption setting of parameters beneath the path, and the provider's own link docs (`bluelink-provider-aws: inter-service-links/lambda_ssm/descriptions/function__parameter_tree.md`) state the requirement as a note for the practitioner to satisfy, not a validation. So there is **no diagnostic** — a missing KMS link surfaces as an `AccessDenied` at first read (runtime), not at transform or deploy. The transformer must therefore declare the `aws/kms/key` link itself whenever it emits a store key encrypted with a non-default key; it is the only component positioned to know a customer-managed key is in play.

> All these links also call `ActivateLinkNetworking` to ensure an interface VPC endpoint exists for `ssm` / `secretsmanager` / `kms` when the function is VPC-attached — another reason not to hand-roll this wiring.

---

## 12. Lambda Parameters and Secrets Extension

The SDK can read through the AWS-provided Lambda Parameters and Secrets Extension (`PARAMETERS_SECRETS_EXTENSION_HTTP_PORT`, default `2773`) instead of calling the AWS SDK directly, amortising fetches across the extension's built-in cache.

**It applies to Secrets Manager namespaces only.** The extension has no `GetParametersByPath` equivalent, so it cannot serve a per-key Parameter Store namespace; the SDK's backend resolver hard-codes this, selecting the direct SDK client for `parameter-store` regardless of whether the extension is present (`celerity-node-sdk: packages/config/src/backends/resolve.ts`). The extension is chosen only when the namespace is `secrets-manager`, the function is on Lambda, and the port env var is set.

- Controlled via the transformer config key `aws.config.useLambdaExtension` (default `false`).
- When enabled, the transformer attaches the AWS-managed layer ARN `arn:aws:lambda:<region>:<account>:layer:AWS-Parameters-and-Secrets-Lambda-Extension:<version>` to every Lambda that reads a **Secrets Manager** namespace. Attaching it to a function whose only store is Parameter Store adds cold-start weight for no benefit.
- The IAM requirements in §11 still apply; the extension proxies requests but does not supply its own credentials.

Note this means the internal `resources` namespace never benefits from the extension, since it is always Parameter Store (§10.2). The optimisation is therefore scoped to applications that declare all-secret `celerity/config` resources — not, as an earlier revision of this document claimed, to every handler with outbound links.

Documented here so the Phase 2 onward PR that lands config-store population has a fixed target, and reviewers know where this slots in.

