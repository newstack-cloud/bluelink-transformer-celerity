# Resource Mapping — `aws-serverless`

**Parent contract:** [index.md](index.md) (shared concerns) · **Companion:** [aws-serverless.md](aws-serverless.md) (handler + shared emission rules)
**Deploy target:** `aws-serverless`
**Provider:** `bluelink-provider-aws` v0.4.0 (adds the DynamoDB-stream `reportBatchItemFailures` annotation → `FunctionResponseTypes` [P1] and the `aws/elasticache/replicationGroup::aws/secretsmanager/secret` AUTH-token link [P2]; both required by the `consumer` and `cache` mappings below)

This document places all thirteen Celerity abstract resources on one map: for each, the concrete AWS resource(s) the transformer emits, the provider link(s) that wire them, the Celerity→`aws.*` annotation translation, and the gaps. [aws-serverless.md](aws-serverless.md) specifies the handler and the shared emission rules in depth; this document is the system-wide view, so the handler's link-declaration layer can be built once against the full set of backing targets rather than discovered piecemeal.

Where a mapping is specified in depth elsewhere (handler, config store, VPC), this document states it compactly and links out rather than duplicating.

> **Implementation status (2026-07):** emits exist for **all thirteen** abstract resources. `celerity/handler`, `celerity/config`, `celerity/queue`, `celerity/topic`, `celerity/datastore`, `celerity/bucket` (partial — `lifecycle`/`replication` deferred), `celerity/vpc`, and now `celerity/api`, `celerity/consumer`, `celerity/schedule`, `celerity/cache` and `celerity/sqlDatabase` all emit; `celerity/handlerConfig` is complete (contributory-only: inheritance + strip, §C). The handler→backing-target links are established generically by preserving the handler's `linkSelector` onto the emitted Lambda, and the handler now carries the **union of its own labels and each absorbed consumer's labels** so an inbound source/api/subscription link that selected the abstract consumer/handler by label resolves to the concrete function (label-on-source; [`handler_aws_triggers.go`](../../resources/handler/handler_aws_triggers.go), [`handler_api_annotations.go`](../../resources/handler/handler_api_annotations.go)). **Deploy-target config (`aws.<svc>.*`) is wired for `queue`, `datastore`, and `sqlDatabase`/Aurora (`aws.aurora.<db>.*`)** via the shared resolver `shared/aws.ResolveDeployConfig` (per-resource-over-global precedence, §2.1); **`topic` and `bucket` deploy-config are deferred** (topic pending the `archivePolicy`/indexed-`statusLogging` shape; bucket's `replication.role` lands with the deferred `replication` block). **Still deferred within otherwise-implemented slices:** API stage **X-Ray tracing** (`tracingEnabled`, an API Gateway v2 platform limitation — handler-level Lambda tracing is wired instead), consumer **externalEvents on out-of-blueprint object storage**, and per-target **access-level annotations** on backing-target handler links. This document is therefore both a *contract* (what the mapping must be) and a *gap register* (what is not yet built). Per-resource "Status" notes and the [Gaps & build sequencing](#gaps--build-sequencing) appendix track the delta.

---

## How to read this

Every abstract resource has an **emit classification** — its structural role in the emitted blueprint, which determines whether it produces a standalone concrete resource, is folded into another, or only modifies one:

| Classification | Meaning | Emits standalone? |
|---|---|---|
| `primary-compute` | The unit of execution. | Yes — `aws/lambda/function` + shared role/layer. |
| `trigger-api` | Front door that invokes compute. | Yes — API Gateway resources. |
| `absorbed-event-source` | An event source folded into the handler it triggers. | No — the handler (or a provider link) realises it. |
| `backing-target` | A stateful resource handlers link to. | Yes — the store/queue/table/etc. |
| `infra-networking` | Placement/networking substrate. | Yes — `aws/flex/vpc` (synthetic). |
| `contributory-config` | Modifies a handler; no resource of its own. | No. |

**"Declaring a link"** throughout means the mechanism in [aws-serverless.md §3.5](aws-serverless.md#35-emit-resources-declare-links--the-provider-does-the-wiring): the transformer emits resources with matching `metadata.labels` and stamps the source resource with `aws.*` annotations; the provider's inter-service link does the actual wiring (IAM statements, env vars, event-source mappings, integrations, `vpcConfig`) at deploy time. `EmitResult` has no "links" field — labels + annotations *are* the declaration.

---

## Master matrix

| Abstract resource | Classification | Concrete resource(s) emitted on `aws-serverless` | Primary provider link (direction) | Upstream spec |
|---|---|---|---|---|
| `celerity/handler` | `primary-compute` | `aws/lambda/function` + shared `aws/iam/role`, `aws/lambda/layerVersion` | *(target of most links)* | `celerity-handler.mdx` |
| `celerity/api` | `trigger-api` | `aws/apigatewayv2/api` (one **per protocol**) + `…/stage` (+ `…/authorizer`, guard Lambda) | `apigatewayv2/api::lambda/function` | `celerity-api.mdx` |
| `celerity/consumer` | `absorbed-event-source` | none for poll/S3 push; **`aws/sns/subscription`** emitted for a Celerity-topic source | `sqs/queue`·`dynamodb/table`·`s3/bucket`·`sns/subscription` `::lambda/function` | `celerity-consumer.mdx` |
| `celerity/schedule` | `absorbed-event-source` | `aws/events/rule` — emitted by the **absorbing handler** | `events/rule::lambda/function` | `celerity-schedule.mdx` |
| `celerity/queue` | `backing-target` | `aws/sqs/queue` | `lambda/function::sqs/queue` | `celerity-queue.mdx` |
| `celerity/topic` | `backing-target` | `aws/sns/topic` (+ `aws/sns/subscription` per consumer) | `lambda/function::sns/topic` | `celerity-topic.mdx` |
| `celerity/datastore` | `backing-target` | `aws/dynamodb/table` | `lambda/function::dynamodb/table` | `celerity-datastore.mdx` |
| `celerity/bucket` | `backing-target` | `aws/s3/bucket` | `lambda/function::s3/bucket` | `celerity-bucket.mdx` |
| `celerity/cache` | `backing-target` | `aws/elasticache/replicationGroup` (+ `subnetGroup`, security group, `secretsmanager/secret` for password auth) | `lambda/function::elasticache/replicationGroup` | `celerity-cache.mdx` |
| `celerity/sqlDatabase` | `backing-target` | `aws/rds/{dbCluster\|dbInstance}` + `dbProxy` + `dbSubnetGroup` | `lambda/function::rds/dbProxy` (or `::rds/dbCluster`) | `celerity-sql-database.mdx` |
| `celerity/config` | `backing-target` | `aws/secretsmanager/secret` **or** one `aws/ssm/parameterTree` (backend derived from `plaintext`) | `lambda/function::ssm/parameterTree` · `::secretsmanager/secret` | `celerity-config.mdx` |
| `celerity/handlerConfig` | `contributory-config` | none — resolved into handler defaults, then **stripped** | none (transformer-internal edge) | `celerity-handler-config.mdx` |
| `celerity/vpc` | `infra-networking` | `aws/flex/vpc` (synthetic) | `flex/vpc::lambda/function` | `celerity-vpc.mdx` |

`celerity/channel` and `celerity/workflow` exist upstream but are v1 and unregistered in the transformer — out of scope here.

**Provider links with no current Celerity mapping.** The provider (per `provider/provider.go`) registers a few links that no v0 Celerity abstract resource drives yet: `lambda/function::events/eventBus` (publish to a custom event bus), `lambda/function::lambda/function` (direct/async invoke), `events/rule::events/apiDestination`, and `lambda/function::lambda/codeSigningConfig`. They are provider capabilities available ahead of the Celerity resources (e.g. `channel`) that would use them; noted so they aren't mistaken for missing wiring.

---

## Cross-cutting patterns

These recur across resources and are the load-bearing ideas the per-resource sections lean on.

### 1. Poll sources vs push sources

Event delivery to a Lambda splits two ways, and the split decides what gets emitted ([aws-serverless.md §3.2](aws-serverless.md#32-event-sources)):

- **Poll sources** — SQS, DynamoDB Streams, Kinesis. The provider link creates an `aws/lambda/eventSourceMapping` via a **direct Lambda SDK call** (not a blueprint resource) and grants source-read IAM on the execution role. **No `aws/lambda/permission`.**
- **Push sources** — EventBridge schedules and S3 event notifications. The provider link creates an `aws/lambda/permission` (invoke grant). **No event-source mapping.**

Consumers are poll (except S3-sourced consumers, which are push); schedules are push.

### 2. VPC wiring is two complementary mechanisms

- **Attach** — `flex/vpc::lambda/function` fills the function's `vpcConfig` (subnet IDs by tier + the VPC's managed security group). Subnet tier is chosen by `aws.flexvpc.lambda.subnetType` (default `private`). The transformer emits the function **without** `vpcConfig`; the link supplies it.
- **Reach** — once a function is VPC-attached, `linkutils.ActivateLinkNetworking` on its *other* access links reads that same `vpcConfig` and adds what the target needs: an **interface/gateway VPC endpoint** for managed services (SSM, Secrets Manager, KMS, S3, DynamoDB) or **security-group pairing** for in-VPC targets (ElastiCache, RDS). It is a no-op for non-VPC functions.

### 3. Backing-target has two sub-shapes

- **Link-wired access** (`queue`, `topic`, `datastore`, `bucket`, `config`): emit the concrete resource standalone; the handler link injects IAM + env var. Nothing create-only, no placement.
- **VPC-placed with create-only networking** (`cache`, `sqlDatabase`): subnet group + security group must be set **on the resource at creation** and cannot be changed by a link. So the transformer emits `aws/elasticache/subnetGroup` / `aws/rds/dbSubnetGroup` + a security group and wires them by reference; the handler link only adds the SG ingress + credential/env-var wiring. Connection details flow as **per-link Lambda env vars + a Secrets Manager secret**, *not* through the config store.

### 4. Secret handling asymmetry

Password auth needs a secret, but who owns it differs: **ElastiCache** has no AWS-managed secret, so the transformer emits an `aws/secretsmanager/secret` for the AUTH token; **RDS/Aurora** use `manageMasterUserPassword` → an **AWS-managed** `masterUserSecret`, which the transformer references rather than creates. Either way the handler is separately linked to the secret, and a customer-managed KMS key adds the `kms:Decrypt` obligation from [aws-serverless.md §11](aws-serverless.md#11-iam-implications-config-store).

### 5. `spec.id` is not always an ARN

Upstream models `spec.id` as an ARN for most resources, but two concrete resources don't expose one: `aws/apigatewayv2/api` (only `apiId` + `apiEndpoint`) and `aws/elasticache/replicationGroup` (only `replicationGroupId`). The transformer must **synthesise** the ARN. For `api` this is the API Gateway control-plane ARN `arn:aws:apigateway:<region>::/apis/${apiId}` (region + id only — **no account id**; implemented). For `cache` the account-scoped `arn:aws:elasticache:<region>:<account>:replicationgroup:<id>` form is still deferred with the cache outputs.

---

## Sections by classification

### primary-compute

#### `celerity/handler` → `aws/lambda/function`

The reference mapping; specified in full in [aws-serverless.md §3.1, §7, §8, §9](aws-serverless.md#31-handler-core). In brief: one `aws/lambda/function` per handler (spec fields `functionName`, `handler`, `runtime`, `code`, `memorySize`, `timeout`, `environment.variables`, `tags`, `tracingConfig`, `role`, `layers`), a shared `aws/iam/role` deduped by link-set fingerprint (§8), and a shared `aws/lambda/layerVersion` deduped by content hash (§9). The handler is the **target** of nearly every backing-target and event-source link, and the **owner** of the link-declaration layer (`handler_aws_links.go` for backing-target `linkSelector` propagation; `handler_aws_triggers.go` + `handler_api_annotations.go` for event-source/api/VPC annotation stamping) that stamps the `aws.*` annotations described in every section below.

---

### trigger-api

#### `celerity/api` → `aws/apigatewayv2/*`

**Status:** implemented (`resources/api/api_aws_serverless_emit.go` + `api_transform_test.go`). Emits one `aws/apigatewayv2/api` **per protocol** + its `aws/apigatewayv2/stage`, JWT/REQUEST authorizers, and (when `spec.domain` is set) the custom-domain `domainName` + `apiMapping` resources; the `api::function` link owns the integration/route/permission plumbing. The **concrete API preserves the abstract API's `linkSelector`** so the provider `apigatewayv2/api::lambda/function` link resolves to each handler by label (label-on-source); route/auth config is stamped on the **function** (resourceB), not the API.

| celerity `spec` | concrete | notes |
|---|---|---|
| `protocols[]` (`http`/`websocket`) | `aws/apigatewayv2/api.protocolType` (`HTTP`\|`WEBSOCKET`) | **one API per protocol** — `protocolType` is create-only, single-valued. Hybrid `["http","websocket"]` ⇒ **two** `api` resources (`<name>_http_api`, `<name>_websocket_api`). |
| `protocols[].websocketConfig.routeKey` | `routeSelectionExpression` (`$request.body.<routeKey>`) | WS only |
| `cors.*` (or `"*"` shorthand) | `corsConfiguration.{allowCredentials,allowOrigins,allowMethods,allowHeaders,exposeHeaders,maxAge}` | HTTP only; `maxAge` string→int coercion |
| `auth.guards[] type:jwt` | `aws/apigatewayv2/authorizer` (`authorizerType=JWT`, `jwtConfiguration.{issuer,audience}`, `identitySource`) | `tokenSource` → `identitySource` shape transform (`$.headers.` → `$request.header.` etc.) |
| `auth.guards[] type:custom` | `authorizer` (`authorizerType=REQUEST`, `authorizerUri` = `arn:aws:apigateway:<region>:lambda:path/…/${guardFn.arn}/invocations`, `authorizerPayloadFormatVersion=2.0`, `enableSimpleResponses=true`) + the guard's own `aws/lambda/function` | custom guard is a handler (found via `celerity.handler.guard.custom`); needs `aws.region` |
| `spec.domain.*` | `aws/apigatewayv2/domainName` (`domainName`, `domainNameConfigurations[].{certificateArn←certificateId, securityPolicy}`) + `aws/apigatewayv2/apiMapping` per protocol/basePath (`apiId`, `domainName`, `stage`, `apiMappingKey`) | one `domainName`, N `apiMapping`s |
| *(implicit)* | `aws/apigatewayv2/stage` (`apiId`, `stageName=$default`, `autoDeploy=true`) | **no link creates this** — transformer emits it |

**Outputs:** `spec.id` → **synthesised** as `arn:aws:apigateway:<region>::/apis/${resources.<primaryApi>.spec.apiId}` (API Gateway control-plane ARN — the `api` resource exposes no ARN attribute; account id is omitted; empty region segment + warning when `aws.region` is unset); `spec.baseUrl` → `apiEndpoint`. The primary API is the HTTP API when present, else the WebSocket API.

**Links.** Links to `celerity/handler` (`api_handler_link.go`, implemented, `CardinalityB {0,1}`) and — per upstream — `celerity/config` (`api_config_link.go`). Provider link `apigatewayv2/api::lambda/function` injects the `AWS_PROXY` integration (`integrationUri` = function ARN), the route, and the invoke permission; for WebSocket it adds an opt-in `execute-api:ManageConnections` statement.

**Annotation map** (stamped on the **function**, resourceB of the `apigatewayv2/api::lambda/function` link, from the handler's own route/guard annotations + the linked API's `auth` config; `api_transform_test.go`):

| celerity (handler / api) | `aws.*` annotation on the function |
|---|---|
| `celerity.handler.http.method` + `.http.path` | `aws.apigatewayv2.lambda.routeKey` (e.g. `"GET /orders"`) |
| `celerity.handler.websocket.route` | `aws.apigatewayv2.lambda.routeKey` (`$connect`/`$disconnect`/`$default`/custom) |
| guard resolved from `celerity.handler.guard.protectedBy` (or the API's `auth.defaultGuard`), `type:jwt` | `aws.apigatewayv2.lambda.authorizerId` = `${<api>_<guard>_authorizer.spec.authorizerId}` + `aws.apigatewayv2.lambda.authorizationType=JWT` |
| same, `type:custom` | `authorizerId` + `authorizationType=CUSTOM` |
| `celerity.handler.public=true` | *(none — the route is left open; the handler opts out of the API's default guard)* |

---

### absorbed-event-source

#### `celerity/consumer` → `aws/lambda/eventSourceMapping` (poll) / S3 notification / SNS subscription (push)

**Status:** implemented (`resources/handler/handler_aws_triggers.go` + `consumer_transform_test.go`). A consumer is **absorbed into the handler** it triggers: it produces no standalone compute, and the handler's emitted Lambda carries the **union of the handler's + every absorbed consumer's labels**, so each concrete source's preserved `linkSelector` (which selected the abstract consumer by label) now resolves to the function (label-on-source). The source→function wiring is created by the **provider poll-source link** via a direct SDK call, by the S3 push link, or — for a Celerity-topic source — by an **emitted `aws/sns/subscription`** whose `endpoint` references the function (reference-activated `sns/subscription::lambda/function` link, which grants the invoke permission; no permission emitted here).

| celerity/consumer source | concrete | notes |
|---|---|---|
| linked queue | SQS event-source mapping (link-owned SDK call) | poll |
| linked datastore (DynamoDB) | DynamoDB-stream event-source mapping (link enables the stream) | poll |
| linked bucket | S3 event notification | **push** — `aws.s3.lambda.event.<index>` on the function |
| Celerity topic (literal `sourceId` `celerity::topic::<arn>`) | **emitted `aws/sns/subscription`** (`protocol=lambda`, `topicArn`=the literal ARN, `endpoint`=`${func.arn}`) | **push** — reference-activated, no function-side annotation. A topic `sourceId` expressed as a **substitution** is not yet classified as a topic and falls to the unclassifiable path (warning) |
| `externalEvents` / out-of-blueprint ARN | *(none — warning diagnostic)* | **deferred**, see §A |
| unclassifiable source (no linked queue/datastore/bucket, unrecognised `sourceId`) | *(none — warning diagnostic)* | skipped |

**Annotation map** (stamped on the **function**, resourceB of each poll/push link):

| celerity | `aws.*` annotation on the function | source |
|---|---|---|
| `spec.batchSize` | `aws.sqs.lambda.batchSize` (queue) · `aws.dynamodb.lambda.stream.batchSize` (datastore) | queue / datastore |
| `spec.partialFailures=true` | `aws.sqs.lambda.reportBatchItemFailures` **and** `aws.dynamodb.lambda.stream.reportBatchItemFailures` | queue **and** datastore — **DynamoDB is now supported (provider P1)**, no longer dropped |
| `celerity.consumer.datastore.startFromBeginning` | `aws.dynamodb.lambda.stream.startingPosition` (`TRIM_HORIZON` when `true`, else `LATEST`) | datastore — **required** by the provider link, always stamped |
| `celerity.consumer.bucket.events` (`created`/`deleted`; `metadataUpdated` has no S3 equivalent → skipped) | `aws.s3.lambda.event.<index>` (`s3:ObjectCreated:*` / `s3:ObjectRemoved:*`) | bucket |
| consumer→handler marker | `celerity.handler.consumer`, `celerity.handler.consumer.route` (no `aws.*` — handler routing metadata) | all |

#### `celerity/schedule` → `aws/events/rule`

**Status:** implemented. `schedule_emit.go` is a deliberate **no-op** (a schedule is contributory); the **absorbing handler** emits one `aws/events/rule` per absorbed schedule (`emitScheduleRules` in `handler_aws_triggers.go` + `schedule_transform_test.go`).

The absorbing handler emits the rule; its `targets[].arn` reference to the handler's function (`${resources.<func>.spec.arn}`) activates the `events/rule::lambda/function` link, which creates the invoke permission (push source). **No `aws/lambda/permission` is emitted** — the link owns it.

| celerity/schedule | `aws/events/rule` |
|---|---|
| `schedule` (cron/rate) | `scheduleExpression` |
| *(handler ARN)* | `targets[].arn` (+ per-target `id`, both required) |
| `input` (static JSON) | `targets[].input` |

**Output** `arn` (computed). There is **no `aws/scheduler/*`** — scheduling is only the rule's `scheduleExpression`. The `events/rule::lambda/function` link has **no annotations** (wiring is inline via `targets[]` + reference activation); `celerity.handler.schedule` marks the handler but has no `aws.*` counterpart.

---

### backing-target

#### `celerity/queue` → `aws/sqs/queue`

**Status:** implemented (emit + tests). Emits `aws/sqs/queue` (`queueName` with `.fifo` suffix when `fifo`, `fifoQueue`, `visibilityTimeout`, `kmsMasterKeyId`, list `tags`), preserves the abstract queue's `metadata.labels` and `linkSelector` onto the concrete resource, and re-keys `celerity.queue.deadLetterMaxAttempts` → `aws.sqs.redrive.maxReceiveCount`. The handler→queue edge is established generically: the handler emit copies the abstract handler's `linkSelector` onto the emitted Lambda (`handler_aws_links.go`), so the provider's `function::sqs/queue` link resolves by label with default `send` access. Per-target access-level annotations (`aws.lambda.sqs.<queue>.accessLevel`) are the override extension point, not yet stamped.

| celerity | `aws/sqs/queue` |
|---|---|
| `name` (`.fifo` suffix when `fifo`) | `queueName` |
| `fifo` | `fifoQueue` |
| `visibilityTimeout` | `visibilityTimeout` |
| `encryptionKeyId` | `kmsMasterKeyId` |
| DLQ target + `deadLetterMaxAttempts` | `redrivePolicy` `{deadLetterTargetArn, maxReceiveCount}` on the source queue — **set by the `sqs/queue::sqs/queue` link**, not written inline |

**Output:** `spec.id` → `arn` (but the handler env var uses the queue **URL**, `queueUrl`, not the ARN).

**Links.** From: handler (`lambda/function::sqs/queue` — IAM + `SQS_QUEUE_<name>` env var), bucket (`s3/bucket::sqs/queue`), topic subscription (`sns/subscription::sqs/queue`), event rule (`events/rule::sqs/queue`). To: consumer (`sqs/queue::lambda/function` — see consumer), **queue-as-DLQ (`sqs/queue::sqs/queue`** — sets the source queue's `redrivePolicy` to the DLQ's ARN).

**Annotation map:** handler outbound `aws.lambda.sqs.<queue>.accessLevel` (`send`\|`receive`\|`sendReceive`, default `send`), `.envVarName`, `.populateEnvVars`; consumer `aws.sqs.lambda.batchSize`, `aws.sqs.lambda.reportBatchItemFailures`; `deadLetterMaxAttempts` → **`aws.sqs.redrive.maxReceiveCount`** on the queue→queue link (→ `redrivePolicy.maxReceiveCount`).

**Deploy config** (global + per-queue override, [aws-serverless.md §2.1](aws-serverless.md#21-backing-target-deploy-config-the-global--per-resource-override-rule); **wired** via `shared/aws.ResolveDeployConfigNode`):

| Key — global / per-queue | `aws/sqs/queue` field | Default | Notes |
|---|---|---|---|
| `aws.sqs.messageRetentionPeriod` / `aws.sqs.<queue>.messageRetentionPeriod` | `messageRetentionPeriod` | `345600` (4 days) | integer seconds, 60–1,209,600 |
| `aws.sqs.maxMessageSize` / `aws.sqs.<queue>.maxMessageSize` | `maximumMessageSize` | `262144` (256 KiB) | integer bytes |

#### `celerity/topic` → `aws/sns/topic`

**Status:** implemented (emit + tests). Emits `aws/sns/topic` (`topicName` with `.fifo` suffix when `fifo`, `fifoTopic`, `kmsMasterKeyId`, list `tags`), preserves `metadata.labels` for handler/bucket links, `spec.id` → `topicArn`. `contentBasedDeduplication` is intentionally never set (forbidden for FIFO topics). The `aws/sns/subscription` per topic→consumer edge is now **emitted by the absorbing handler** (see `celerity/consumer`), not the topic emit.

| celerity | `aws/sns/topic` |
|---|---|
| `name` (`.fifo` when `fifo`) | `topicName` |
| `fifo` | `fifoTopic` |
| `encryptionKeyId` | `kmsMasterKeyId` |

**Output:** `spec.id` → `topicArn`. Topics **cannot link to** other resources; consumers subscribe via an emitted `aws/sns/subscription`.

**Links.** From: handler (`lambda/function::sns/topic` — **always grants `sns:Publish`, no `accessLevel`** + `SNS_TOPIC_<name>` env var), bucket (`s3/bucket::sns/topic`). Topic→consumer path: emit `aws/sns/subscription` (protocol `sqs`/`lambda`) wired by `sns/subscription::sqs/queue` or `::lambda/function`.

**Annotation map:** `aws.lambda.sns.<topic>.envVarName`, `.populateEnvVars` (no `accessLevel`); bucket `aws.s3.sns.{event.<i>,filterPrefix,filterSuffix}`. Topics have no `celerity.topic.*` annotations. **Do not** map any field to `contentBasedDeduplication` — the spec forbids it for FIFO topics; leave unset.

**Deploy config** (global + per-topic override, [aws-serverless.md §2.1](aws-serverless.md#21-backing-target-deploy-config-the-global--per-resource-override-rule); **not yet wired**). Two shape irregularities vs. the plain `aws.<svc>[.<name>].<key>` rule — see the notes:

| Key — global / per-topic | `aws/sns/topic` field | Notes |
|---|---|---|
| `aws.sns.fifo.messageRetentionPeriod` / `aws.sns.fifo.<topic>.messageRetentionPeriod` | `archivePolicy` (FIFO message archive retention) | **`fifo` infix**: the segment sits between `sns` and the optional `<topic>` (`aws.sns.fifo[.<topic>].messageRetentionPeriod`), not the plain `aws.sns[.<topic>].key`. **FIFO-only** — ignored for standard topics. |
| `aws.sns.statusLogging.<i>.{failureFeedbackRoleArn,protocol,successFeedbackRoleArn,successFeedbackSampleRate}` / `aws.sns.<topic>.statusLogging.<i>.{…}` | `deliveryStatusLogging[<i>].{failureFeedbackRoleArn,protocol,successFeedbackRoleArn,successFeedbackSampleRate}` | **Indexed array** (`.<i>.`) — a list of per-protocol logging configs, not a scalar. The resolver must enumerate indices, and merge per-topic entries over global by index. |

#### `celerity/datastore` → `aws/dynamodb/table`

**Status:** implemented (emit + tests). Emits `aws/dynamodb/table` (`tableName`; `keySchema` HASH/RANGE from `keys`; `attributeDefinitions` collected from all key + index attributes, defaulting to type `S` since abstract keys carry no type and schema management is out of scope for the concrete table; `globalSecondaryIndexes` from `indexes[]` with `ALL` projection; `timeToLiveSpecification` from `timeToLive`; list `tags`), preserves `metadata.labels`, `spec.id` → `arn`. Does **not** emit `streamSpecification` (the consumer link enables streams). `schema`/`schemaPath`/`scriptsPath` are accepted but dropped (schema-management tooling). Deploy-config-sourced settings (`aws.dynamodb.<datastore>.billingMode`, capacity/on-demand ceilings) are **wired** (see the deploy-config table below).

| celerity | `aws/dynamodb/table` |
|---|---|
| `name` | `tableName` (create-only) |
| `keys.partitionKey` / `.sortKey` | `keySchema[HASH]`/`[RANGE]` + `attributeDefinitions` |
| `indexes[]` | `globalSecondaryIndexes[]` |
| `timeToLive.{fieldName,enabled}` | `timeToLiveSpecification.{attributeName,enabled}` |
| `schema`/`schemaPath`/`scriptsPath` | *(none — schema-management tooling; drop)* |

**Output:** `spec.id` → `arn` (a `streamArn` also exists, not surfaced). **Do not emit `streamSpecification`** — the `dynamodb/table::function` link enables streams via `UpdateTable` and creates the ESM.

**Links.** From: handler (`lambda/function::dynamodb/table` — IAM + `DYNAMODB_TABLE_<t>` env var). To: consumer (`dynamodb/table::lambda/function` — streams).

**Annotation map:** handler `aws.lambda.dynamodb.<table>.accessLevel` (`read`\|`write`\|`readwrite`, default `readwrite`), `.envVarName`, `.populateEnvVars`; stream `aws.dynamodb.stream.viewType` (on the table); consumer `aws.dynamodb.lambda.stream.{batchSize,startingPosition,…}`.

**Deploy config** (per-datastore only — **no global form**; `<ds>` = datastore name; [aws-serverless.md §2.1](aws-serverless.md#21-backing-target-deploy-config-the-global--per-resource-override-rule); **wired** via `shared/aws.ResolveDeployConfig`):

| Key | `aws/dynamodb/table` field | Default | Notes |
|---|---|---|---|
| `aws.dynamodb.<ds>.billingMode` | `billingMode` | `PAY_PER_REQUEST` | `PAY_PER_REQUEST` \| `PROVISIONED` |
| `aws.dynamodb.<ds>.readCapacityUnits` | `provisionedThroughput.readCapacityUnits` | — | only when `PROVISIONED` |
| `aws.dynamodb.<ds>.writeCapacityUnits` | `provisionedThroughput.writeCapacityUnits` | — | only when `PROVISIONED` |
| `aws.dynamodb.<ds>.maxReadRequestUnits` | `onDemandThroughput.maxReadRequestUnits` | — | only when `PAY_PER_REQUEST` |
| `aws.dynamodb.<ds>.maxWriteRequestUnits` | `onDemandThroughput.maxWriteRequestUnits` | — | only when `PAY_PER_REQUEST` |
| `aws.dynamodb.<ds>.replicaRegions` | global-tables replica specifications | — | **deferred** beyond the first deploy-config pass (global tables restructure the table; own slice) |

#### `celerity/bucket` → `aws/s3/bucket`

**Status:** implemented, partial (emit + tests). Emits `aws/s3/bucket` (`bucketName`; `encryption` → `bucketEncryption.serverSideEncryptionConfiguration` with SSE algorithm defaulting to `aws:kms` when a key is set else `AES256`; `cors` → `corsConfiguration.corsRules` (1:1); `versioning.status` → `versioningConfiguration.status`; `website` → `websiteConfiguration.{indexDocument,errorDocument}`; `logging` → `loggingConfiguration.{destinationBucketName,logFilePrefix}`; list `tags`), preserves `metadata.labels`, `spec.id` → `arn`. **`lifecycle` and `replication` are deferred** — accepted by the schema but raise a warning diagnostic when set (never silently dropped); `replication`'s role is sourced from `aws.s3.<bucket>.replication.role` deploy config, so it lands with the deploy-config pass.

| celerity | `aws/s3/bucket` |
|---|---|
| `name` | `bucketName` (create-only) |
| `encryption.*` | `bucketEncryption.serverSideEncryptionConfiguration[]` |
| `cors.corsRules[]` | `corsConfiguration.corsRules[]` |
| `lifecycle.rules[]` | `lifecycleConfiguration.rules[]` (write-only) |
| `versioning.status` | `versioningConfiguration.status` |
| `replication.*` | `replicationConfiguration` (write-only; role from `aws.s3[.<bucket>].replication.role`) |
| `website.*` | `websiteConfiguration.{indexDocument,errorDocument}` |

**Output:** `spec.id` → `arn`. Event notifications: emit the bucket, declare the edge; the `s3/bucket::{lambda,sns,sqs}` links own `notificationConfiguration` (and the permission for lambda).

**Links.** From: handler (`lambda/function::s3/bucket` — IAM + `S3_BUCKET_<b>` env var). To: consumer/queue/topic (S3 event notifications).

**Annotation map:** handler `aws.lambda.s3.<bucket>.accessLevel` (`read`\|`write`\|`readwrite`, default `readwrite`), `.envVarName`, `.populateEnvVars`; event filters `aws.s3.{lambda,sns,sqs}.{event.<i>,filterPrefix,filterSuffix}`. S3-source links carry **no `accessLevel`** (the bucket is the producer).

**Deploy config** (global + per-bucket override, [aws-serverless.md §2.1](aws-serverless.md#21-backing-target-deploy-config-the-global--per-resource-override-rule); **not yet wired**):

| Key — global / per-bucket | `aws/s3/bucket` field | Default | Notes |
|---|---|---|---|
| `aws.s3.replication.role` / `aws.s3.<bucket>.replication.role` | `replicationConfiguration.role` | auto-created role | **deploy-config is the only source** — no spec field. `replicationConfiguration` requires both `role` (here) and `rules` (from the deferred `replication` spec block), so it lands with `replication`. |

#### `celerity/cache` → `aws/elasticache/replicationGroup`

**Status:** implemented (emit + `cache_transform_test.go`). Emits `aws/elasticache/replicationGroup` (`replicationGroupId`←`name`, `engine`=`redis`, `engineVersion`, `clusterMode`→`numNodeGroups` 1-vs-2, `replicasPerNodeGroup`=1, `port` 6379, `transitEncryptionEnabled=true`) + `aws/elasticache/subnetGroup` when placed in a VPC (`subnetIds`=`${resources.<vpc>_flex_vpc.spec.privateSubnetIds}`, `securityGroupIds`=`${…spec.securityGroups}`). The placement VPC is resolved from the inbound `vpc→cache` link; **preset-suitability validation** rejects `public`/`light-public` (no private subnets) via an error diagnostic; an unlinked cache raises a VPC-placement warning. Preserves `metadata.labels`. **Password auth is fully wired (provider P2):** the emit adds an `aws/secretsmanager/secret` (`generateSecretString.{passwordLength:32, excludeCharacters:"@/\\"+space}`) carrying the label `celerity.cache.auth:<name>`, and merges that label into the RG's `linkSelector`, activating the `aws/elasticache/replicationGroup::aws/secretsmanager/secret` link — which reads the secret value and applies it as the RG's write-only `authToken` via `ModifyReplicationGroup` (requires `transitEncryptionEnabled`, set above). The default `authTokenUpdateStrategy` (SET-on-create / ROTATE-on-update) is **not stamped** — the provider derives it. **iam auth is also wired:** the emit adds `aws/elasticache/user` (`authenticationMode.type=iam`) + `aws/elasticache/userGroup` (containing the managed `default` user + the IAM user) and sets the RG's `userGroupIds`; the absorbing handler stamps `aws.lambda.elasticache.<rg>.authMode=iam` so the `function::replicationGroup` link grants `elasticache:Connect`. Outputs (`host`/`port`/`id`) are wired via the property map + emit-time derived values. Uses `vpc.ConcreteResourceName` for the cross-package subnet reference.

| celerity | `aws/elasticache/replicationGroup` |
|---|---|
| `name` | `replicationGroupId` |
| `clusterMode` | `numNodeGroups` / `replicasPerNodeGroup` |
| `engineVersion` | `engineVersion` (write-only) |
| `authMode="password"` (default) | `transitEncryptionEnabled=true` + **emit `aws/secretsmanager/secret`** (label `celerity.cache.auth:<name>`); the `replicationGroup::secret` link SETs `authToken` (write-only, never in state) |
| `authMode="iam"` | `userGroupIds` → `aws/elasticache/user` (IAM-enabled) — **deferred** |
| *(placement)* | `cacheSubnetGroupName` (emit `aws/elasticache/subnetGroup`) + `securityGroupIds` — **create-only** |

**Outputs:** `spec.id` → **synthesise ARN** (no ARN attribute); `host` → `configurationEndPoint`/`primaryEndPoint.address`; `port` → default 6379. Connection details reach the handler as **per-link env vars** (`<PREFIX>_HOST`/`_PORT`), not the config store; only the AUTH token is a secret.

**Links.** From: handler (`lambda/function::elasticache/replicationGroup` — SG ingress via `ActivateLinkNetworking`, `elasticache:Connect` IAM for iam-mode, `<PREFIX>_HOST/_PORT` env vars) + the password secret (`lambda/function::secretsmanager/secret`). `vpc→cache` has **no provider link** (create-only placement, see gaps).

**Annotation map:** `aws.lambda.elasticache.<cache>.{authMode,userId,port,envVarPrefix,populateEnvVars}`; handler VPC tier via `aws.flexvpc.lambda.subnetType` on the handler↔VPC link. The `replicationGroup::secret` link exposes `aws.elasticache.secretsmanager.authTokenUpdateStrategy` on the **replication group** (resourceA) — the transformer leaves it unset (provider default). Cache spec has no other linking annotations (spec-field-driven).

#### `celerity/sqlDatabase` → `aws/rds/{dbCluster|dbInstance}` + `dbProxy`

**Status:** implemented (emit + `sqldatabase_transform_test.go`). Emits standalone `aws/rds/dbInstance` (`dbInstanceIdentifier`/`dbName`←`name`, `engine`, defaults for `dbInstanceClass`/`allocatedStorage`/`masterUsername`, `manageMasterUserPassword` for password mode / `enableIAMDatabaseAuthentication` for iam) + `aws/rds/dbSubnetGroup` when VPC-placed (`subnetIds`=`${…privateSubnetIds}`, `vpcSecurityGroups`=`${…securityGroups}`). VPC resolved from the inbound `vpc→sqlDatabase` link; **preset-suitability validation** rejects `public`/`light-public`/**`light`** (RDS needs ≥2 AZs) via an error diagnostic. Preserves `metadata.labels`. **RDS Proxy is now emitted** for the standalone path (VPC-placed only): `aws/rds/dbProxy` (`dbProxyName`, `engineFamily` MYSQL/POSTGRESQL from `engine`, `roleArn`=`${role.arn}`, `vpcSubnetIds`, `vpcSecurityGroupIds`, password-mode `auth[]={authScheme:SECRETS, secretArn:${instance.spec.masterUserSecret.secretArn}}` / iam-mode `auth[]={iamAuth:REQUIRED}`) + its `aws/iam/role` (trusts `rds.amazonaws.com`, password mode grants `secretsmanager:GetSecretValue` on the managed secret) + `aws/rds/dbProxyTargetGroup` (`targetGroupName:default`, `dbInstanceIdentifiers`). The proxy carries the db's labels so the handler's `function::rds/dbProxy` link resolves by label (label-on-source). **Aurora Serverless v2 is now emitted** behind `aws.aurora.<db>.enabled`: `aws/rds/dbCluster` + a `db.serverless` writer `dbInstance` (no proxy — Aurora has built-in pooling). **`readReplicas`** emits a reader `dbInstance` (Aurora reader in the cluster / RDS read replica of the primary). **Still deferred:** outputs (`spec.id`/`host`/`readHost`/`port`). `schemaPath`/`migrationsPath` accepted but dropped (schema-management).

Two provisioning shapes:

- **Standalone RDS** (default): `aws/rds/dbInstance` fronted by `aws/rds/dbProxy` (Lambda pooling) + `aws/iam/role` + `aws/rds/dbProxyTargetGroup`, on `aws/rds/dbSubnetGroup`.
- **Aurora Serverless v2** (`aws.aurora.<db>.enabled`): `aws/rds/dbCluster` (`engine=aurora-postgresql`/`aurora-mysql`, `serverlessV2ScalingConfiguration.{minCapacity,maxCapacity}` from `aws.aurora.<db>.{minCapacity,maxCapacity}` defaulting 0.5/4.0, `manageMasterUserPassword`→`masterUserSecret`) + `db.serverless` writer instance. **No proxy.**

| celerity | concrete |
|---|---|
| `name` | `databaseName` + `dbClusterIdentifier`/instance id |
| `authMode="password"` | `manageMasterUserPassword` → computed `masterUserSecret` (AWS-managed); proxy `auth[].secretArn` references it |
| `authMode="iam"` | IAM DB auth (`enableIAMDatabaseAuthentication`); proxy `auth[].iamAuth=REQUIRED`; `rds-db:connect` via link |
| `readReplicas` | reader `aws/rds/dbInstance` (Aurora reader / RDS read replica) |
| *(placement)* | `dbSubnetGroupName` + `vpcSecurityGroups`(instance)/`vpcSecurityGroupIds`(cluster/proxy) — **create-only** |

**Outputs:** `spec.id` → `dbClusterArn`/`dbInstanceArn`; `host` → the **proxy** `endpoint` on serverless; `readHost` → cluster `readEndpoint` (cluster path only); `port` default 5432. Connection details via per-link env vars + AWS-managed secret, not the config store.

**Links.** From: handler (`lambda/function::rds/dbProxy` default, or `::rds/dbCluster` direct — SG ingress, `rds-db:connect` for iam, `<PREFIX>_HOST/_PORT/_DATABASE`/`_READER_HOST` env vars) + managed password secret (`lambda/function::secretsmanager/secret`). `vpc→sqlDatabase` has **no provider link**.

**Annotation map:** `aws.lambda.rds.<target>.{authMode,dbUser,port,databaseName,envVarPrefix,populateEnvVars}`, plus `aws.lambda.rds.<cluster>.readerEndpoint` (**cluster only**). `celerity.sqlDatabase.allowDestructive` is a schema-migration control with **no `aws.*` counterpart**.

#### `celerity/config` → `aws/secretsmanager/secret` | `aws/ssm/parameterTree`

**Status:** implemented (emit + tests). Handler-side link declaration (the `aws.lambda.ssm.*` / `aws.lambda.secretsmanager.*` annotations) is part of the not-yet-built `handler_aws_links.go` layer. Specified in depth in [aws-serverless.md §10–§12](aws-serverless.md#10-runtime-configuration-store-aws-backends) and [index.md §1.3, §3.1](index.md#13-celerityconfig-abstract-resource).

Backend is **derived from `plaintext`**: empty ⇒ one `aws/secretsmanager/secret` (JSON blob); non-empty ⇒ one `aws/ssm/parameterTree` (`plaintext` keys → `values`/`String`, the rest → `secureValues`/`SecureString`). The tree owns one real SSM parameter per key beneath its `path` and gives stored values **blob-like drift semantics** (runtime CLI overrides survive redeploys — [§10.1](aws-serverless.md#101-backend-choices)). `encryptionKeyId` → the tree's `keyId`; `replicate = true` is reported as an error diagnostic (single-region only for now); the `aws.config.*` deploy keys govern the deferred replication/KMS ([§10.3](aws-serverless.md#103-user-defined-celerityconfig-stores)).

**Output:** `spec.id` → secret ARN (`$.id`) or SSM path prefix (the tree's `path`, a literal). **Links from** handler/api/consumer/schedule; **links to** nothing. Provider links: `lambda/function::ssm/parameterTree` (env var `CELERITY_CONFIG_<NS>_STORE_ID` = path prefix, path-scoped SSM IAM) or `::secretsmanager/secret` (ARN + `secretsmanager:GetSecretValue`), plus `::kms/key` for customer-managed keys (transformer-declared, unenforced — §11). `CELERITY_CONFIG_<NS>_STORE_KIND` is a transformer literal, never link-injected. The internal `resources` namespace is a **separate** always-Parameter-Store store, likewise on `aws/ssm/parameterTree` ([§10.2](aws-serverless.md#102-the-internal-resources-namespace)).

---

### infra-networking

#### `celerity/vpc` → `aws/flex/vpc`

**Status:** implemented (emit + tests). Emits `aws/flex/vpc`: `name`, `mode` (`managed`→`create` / `referenced`→`reference`), and in create mode `preset` (default `standard`), `cidrBlock` (from `aws.vpc[.<name>].cidrBlock`, default `10.0.0.0/16`), `enableDNSSupport`/`enableDNSHostnames` (deploy config, when set), and `region` (from `aws.region`; error diagnostic if absent outside a validation context). Reference mode carries **only** `name` (preset/cidrBlock/region are `ConflictsWith` on the provider). Preserves `metadata.labels`/`linkSelector`; `spec.id` → `vpcId`. **Handler VPC placement is now implemented:** the emitted Lambda carries the handler's labels (so the VPC's `flex/vpc::function` link resolves by label) and the transformer stamps `aws.flexvpc.lambda.subnetType` on the **function** (resourceB); the function is emitted **without** `vpcConfig` — the `flex/vpc::lambda/function` link fills it (subnet IDs by tier + managed SG) at deploy time. Specified alongside handler VPC placement in [aws-serverless.md §3.5](aws-serverless.md#35-emit-resources-declare-links--the-provider-does-the-wiring).

`aws/flex/vpc` is a **synthetic** provider resource (same pattern as `parameterPath`): one node stands in for the whole VPC/subnet/route/gateway/SG hierarchy, exposed as computed outputs.

| celerity/vpc | `aws/flex/vpc` |
|---|---|
| `name` | `name` (create-only) |
| `mode` (`managed`\|`referenced`, default `managed`) | `mode` (`create`\|`reference`, default `create`) |
| `preset` (`standard`\|`public`\|`isolated`\|`light`\|`light-public`, default `standard`) | `preset` (**verbatim** — value sets match); omitted in `reference` mode |
| *(deploy `aws.vpc.cidrBlock`, default `10.0.0.0/16`)* | `cidrBlock` (**required** in `create`; omitted in `reference`) |
| *(deploy `aws.vpc.enableDNSSupport`/`enableDNSHostnames`)* | `enableDNSSupport`/`enableDNSHostnames` (create only) |
| *(deployment region)* | `region` (**required** in `create`; omitted in `reference`) |

**Output:** `spec.id` → `vpcId` (subnets/SGs/gateways are computed but provider-internal, not celerity outputs). **Links to** handler (`flex/vpc::lambda/function` → `vpcConfig`), cache, sqlDatabase; VPC cannot be linked *from*.

**Annotation map:** `celerity.handler.vpc.subnetType` → `aws.flexvpc.lambda.subnetType` (`public`\|`private`, default `private`) — a 1:1 rename on the handler↔VPC link.

**Shared VPCs across blueprints (`referenced` mode).** `aws/flex/vpc` supports `mode: reference`, which resolves an existing flex VPC by its `bluelink:flex-vpc:name={name}` tag via `GetExternalState` rather than provisioning one ([`flex/vpc_resource_create.go:51`](../../../projects2025/bluelink-provider-aws/flex/vpc_resource_create.go#L51)). This is how a VPC is shared across applications **without native cross-blueprint links**: one blueprint declares `celerity/vpc` in `managed` mode (the owner), others declare a `celerity/vpc` with the **same `name`** in `referenced` mode. Each blueprint's own `flex/vpc::function` link places its functions into the shared VPC; nothing crosses a blueprint boundary. The transformer lowering is: `referenced` → `aws/flex/vpc` `mode: reference` with only `name`; `preset` and the `aws.vpc.*` deploy keys are dropped (they `ConflictsWith` reference on the provider). Two constraints follow from the tag-lookup design: the referenced VPC must itself be a Celerity/flex-managed VPC (not an arbitrary imported one), and — because AWS VPCs are regional — it must be in the same region as the resources placed into it. The abstraction generalises across providers: each provider plugin resolves the Celerity `name` via its native tag/label convention (GCP VPCs are global, so no region match is needed). See [celerity-vpc.mdx §mode](https://celerityframework.io/docs/framework/applications/resources/celerity-vpc#mode).

---

## Gaps & build sequencing

### A. Missing provider links (abstract edge with no concrete counterpart)

> **Link inventory reconciled against the full `provider.go` registry.** An earlier draft under-counted provider links because it searched only `inter-service-links/`; **service-scoped** links also live under `services/<svc>/links/` (`sqs`, `lambda`, `events`). The rows below are checked against the complete registration list in `bluelink-provider-aws: provider/provider.go`. Notably `aws/sqs/queue::aws/sqs/queue` (DLQ redrive) **does exist** and is *not* a gap — it is the `queue`-section mapping above.

| Abstract edge | Why it has no link | Transformer must instead |
|---|---|---|
| `queue → topic` (forward) | no `sqs::sns` link | emit an intermediary `aws/lambda/function` + `sqs::function` + `function::sns` (spec: "intermediary functions provisioned") |
| `vpc → cache` | ElastiCache placement is create-only; not a link | **Resolved (provider v0.3.1).** Emit `aws/elasticache/subnetGroup` with `subnetIds` = `${vpc.spec.privateSubnetIds}` + reference `securityGroups`. The subnet-group resource validates ≥1 subnet at plan time. |
| `vpc → sqlDatabase` | RDS placement is create-only; not a link | **Resolved (provider v0.3.1).** Emit `aws/rds/dbSubnetGroup` with `subnetIds` = `${vpc.spec.privateSubnetIds}` + reference `securityGroups`. The subnet-group resource validates ≥2 subnets across distinct AZs at plan time (also enforced for RDS Proxy). |
| `api → config` | no app-level concrete link | fan out to a `function::parameterTree`/`::secret` link **per handler** in the API |
| `api custom domain` | no `domainname::api` link, no ACM cert link | **Resolved.** The `api` emit builds `aws/apigatewayv2/domainName` (`domainNameConfigurations[].certificateArn` from `spec.domain.certificateId`) + one `aws/apigatewayv2/apiMapping` per protocol/basePath, wired by reference. The ACM certificate is referenced by ARN, not created. |
| `topic → consumer` subscription node | `sns::sqs`/`sns::lambda` links only attach policy | **Resolved.** The absorbing handler emits the `aws/sns/subscription` node (`protocol=lambda`, `topicArn`, `endpoint`=`${func.arn}`); the reference-activated `sns/subscription::lambda/function` link grants the invoke permission. |
| `api/apigatewayv2 stage` | no `stage::api` link | **Resolved.** The `api` emit emits `aws/apigatewayv2/stage` (`apiId`, `stageName=$default`, `autoDeploy=true`) directly. |

**Cross-blueprint VPC sharing is intentionally link-free.** Sharing a VPC across applications is the one cross-blueprint relationship handled *without* a link, by design — `celerity/vpc` `mode: referenced` → `aws/flex/vpc` `mode: reference`, resolved by name tag (see the `celerity/vpc` section). This sidesteps native cross-blueprint links entirely. It depends on an upstream `celerity-vpc.mdx` spec addition (the `mode` field), which has been drafted; the transformer lowering (add `mode`, drop `preset`/`aws.vpc.*` in reference mode) is a small extension of the `celerity/vpc` emit in build step 4.

### B. Shape mismatches to confirm with the provider

- ~~**`consumer.partialFailures` on DynamoDB streams**~~ — **Resolved (provider P1).** The `dynamodb/table::lambda/function` link now honours `aws.dynamodb.lambda.stream.reportBatchItemFailures` → `FunctionResponseTypes=[ReportBatchItemFailures]`; the transformer stamps it from `spec.partialFailures` for datastore sources.
- **`bucket → consumer` FaaS path** — the spec describes an EventBridge rule; the provider offers only direct S3→Lambda notification (`s3::lambda`). The transformer uses the **direct S3 push path** (`aws.s3.lambda.event.<index>`); confirm this is acceptable long-term or whether an S3→EventBridge link is needed.
- **`spec.id` ARN synthesis** — **`api` resolved:** synthesised as `arn:aws:apigateway:<region>::/apis/${apiId}` (account id omitted; empty region + warning when `aws.region` unset). `cache` (`replicationGroupId`) ARN synthesis is still **deferred** (part of the cache outputs follow-up).
- ~~**Standalone RDS has no direct `function::dbInstance` link**~~ — **Confirmed & handled.** Lambda access is only via `dbProxy`; the transformer therefore emits the proxy as **mandatory** for VPC-placed standalone RDS (a non-VPC standalone DB emits no proxy and warns that handlers cannot pool to it).
- **`readerEndpoint` only on the cluster link** — `readReplicas` now emits a reader instance, but `readHost` can't surface through the standalone `dbProxy` path; the output is deferred with the rest of the sqlDatabase outputs.

### C. Correctness bug in an existing stub

- ~~**`celerity/handlerConfig` is not stripped.**~~ **Resolved.** `celerity/handlerConfig` is contributory-only and correctly handled: its emitter is a no-op, so the framework (which builds the output `Resources` solely from emitted resources — `assembleBlueprint`) leaves the abstract resource out of the concrete blueprint automatically; and the handler's `resolveInheritedSpec` performs the inheritance merge (linked `handlerConfig` → `metadata.sharedHandlerConfig` → schema defaults, per-field). Covered by `resources/handler/handler_resolve_test.go` (inheritance) and `transformer/handlerconfig_transform_test.go` (strip + inherited fields reaching the emitted lambda). The dead `TransformResource` free function that copied it into output — the source of the original claim — has been removed.

### D. Recommended build order

Rationale: build the link *targets* before the link *declarers*, and networking substrate before the resources that need placement.

1. ~~**`handlerConfig` strip + inheritance**~~ — **Done.** Was already implemented; hardened with a strip regression test and dead-code removal (§C).
2. ~~**Simple backing targets: `queue`, `topic`, `datastore`, `bucket`**~~ — **Done** (bucket partial: `lifecycle`/`replication` deferred). Emit concrete resource from schema; the handler→target link is established generically by preserving the handler's `linkSelector` onto the Lambda (`handler_aws_links.go`), so no per-target handler change is needed. **Two deferred cross-cutting follow-ups:** (a) per-target access-level annotations (`aws.lambda.<svc>.<target>.accessLevel`), sourced from the abstract `celerity/handler`→target annotations; (b) the **deploy-config pass** — wire `aws.<svc>[.<name>].*` deploy config (queue retention/max-size, datastore billing/capacity, bucket replication + lifecycle) via a shared resolver, contract-mapped first.
3. **`config`** — backend-derived emission + the internal `resources` namespace store (the freshest, fully-specified mapping); unblocks runtime resource resolution.
4. ~~**`vpc`**~~ — **Done** (emit). `aws/flex/vpc` with mode mapping, preset passthrough, deploy-config `cidrBlock`/DNS + `region`, reference-mode drops. Handler VPC *placement* (`vpcConfig` via `flex/vpc::function`, `subnetType` annotation) deferred to the handler-placement follow-up.
5. ~~**`cache`, `sqlDatabase`**~~ — **Done.** Emit subnet-group + primary resource referencing `${resources.<vpc>_flex_vpc.spec.{privateSubnetIds,securityGroups}}` via `vpc.ConcreteResourceName`, resolving the VPC from the inbound `vpc→resource` link. **Cache password auth** is wired via the `aws/secretsmanager/secret` + `replicationGroup::secret` link (provider P2); **sqlDatabase** emits `dbProxy` (+IAM role + target group), the Aurora Serverless v2 cluster path (`aws.aurora.<db>.enabled`), and `readReplicas`. Cache iam/RBAC auth and the cache + sqlDatabase endpoint outputs are wired. **Preset-suitability validation is implemented:** a `managed`-mode VPC linked to a `cache`/`sqlDatabase` must have enough private subnets — `public`/`light-public` have none (reject both), and `light` is single-AZ (reject `sqlDatabase`, needs ≥2 AZs; a single-node `cache` is fine). Not checked for `referenced`-mode VPCs (topology unknown at transform time) — relies on the provider's plan-time subnet-group AZ validation. Facts derived from `celerity-vpc.mdx`, not a hardcoded table.
6. ~~**`consumer`, `schedule` + the event-source half of `handler_aws_triggers.go`**~~ — **Done.** Absorbed sources: the handler emits `aws/events/rule` (schedule) + `aws/sns/subscription` (topic consumer), and stamps poll/push link annotations on the function (label-on-source). Handler VPC placement (`aws.flexvpc.lambda.subnetType`, no `vpcConfig`) also landed here.
7. ~~**`api`**~~ — **Done.** Per-protocol fan-out, stage, JWT/REQUEST authorizers, custom domain (`domainName`+`apiMapping`), and route/auth annotations stamped on the function; `spec.id` ARN synthesised.

`handler_aws_triggers.go` / `handler_api_annotations.go` are the connective tissue built incrementally across steps 2–7: each backing target or event source contributes its slice of "emit node + stamp annotations + set labels."
