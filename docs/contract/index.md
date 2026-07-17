# Transformer Contract

**Implements spec version:** `celerity-2026-02-27-draft`
**Build manifest version:** `v1`

This document is the canonical upstream reference for every transformation the Celerity transformer plugin performs. It covers concerns that are **shared across all supported deploy targets**. Concerns that are specific to a particular target live in per-target companion documents linked from section 7; these separate documents include concrete provider resource types, runtime-ID mapping tables, IAM or equivalent access rules, target-specific env vars and target-specific build-manifest sub-sections.

The transformer sits at the intersection of five independent contracts (the Celerity handler resource spec, the Celerity CLI build manifest, the Celerity CLI deploy configuration, the Celerity runtime SDKs, and the bluelink framework grouping annotations), and captures in one place what the plugin accepts from upstream, what it emits downstream, and the invariants that connect the two.

It is the reference that implementation PRs are checked against, and the document that SDK, CLI, and provider contributors can review without needing to read any Go source in this repo. See the [repo README](../../README.md) for the plugin's purpose and registry details.

## How to use this document

- **Contributors implementing a transformation**: start here for the shared shape, then open the relevant deploy-target contract in section 7 for the concrete provider emission rules. Section 1 (sources) tells you what you can rely on from upstream, section 2 (shared transformer outputs) covers what every target emits, and section 3 (runtime configuration store) is the piece most likely to surprise you.
- **SDK, CLI, or provider developers** reviewing the transformer's assumptions about other components: every claim that depends on other component code is tagged with a `repo-name: path/within/repo` reference (see the note at the top of section 5). If it drifts, open an issue and mention this document so the correction lands in both places in the same PR.
- **Anyone proposing a change**: updates to this document land in the same PR that adopts the upstream change in the transformer. Drift between doc and implementation is a bug.

---

## 1. Sources the transformer consumes

### 1.1 `celerity/handler` abstract resource spec

**Source of truth:** `celerity-docs: content/docs/framework/applications/resources/celerity-handler.mdx`.

Fields accepted on `resource.spec`:

| Field | Type | Default | Inheritable | Required | Notes |
|---|---|---|---|---|---|
| `handlerName` | string | (none) | No | Yes | Identifies the handler; used as the target provider's function name. |
| `handler` | string | (none) | No | Yes | Handler id: a `moduleName.exportName` code-entry reference the SDK runtime imports to locate the user's handler. Injected verbatim as env var `CELERITY_HANDLER_ID` (see 2.2). This is *not* the target runtime entry point (that is the CLI-generated bootstrap); see the per-target contract. |
| `codeLocation` | string | (none) | Yes | Yes | Directory, or file path without extension, containing user code. Consumed by `celerity build` when assembling the target's code asset. |
| `runtime` | string | (none) | Yes | Yes, after inheritance | Must be a valid **Celerity runtime identifier** (not a cloud-native runtime string). The transformer maps this to the target cloud provider's concrete runtime at transform time; see 2.3 for the concept and the per-target contract for the actual table. No default applies; a handler that resolves to an empty runtime after inheritance, or to a runtime identifier the transformer does not recognise, is a fatal transform error. |
| `memory` | int (MB) | `512` | Yes | No | Maps to the target provider's function memory allocation. |
| `timeout` | int (s) | `30` | Yes | No | Maps to the target provider's function execution timeout. Individual targets enforce additional caps (for example AWS Lambda's 900-second hard cap); see the per-target contract. |
| `tracingEnabled` | bool | `false` | Yes | No | When true, enables the target provider's tracing integration and sets `CELERITY_TELEMETRY_ENABLED=true` (see 2.2 and the per-target contract). |
| `environmentVariables` | `map[string]string` | `{}` | Yes, merged; handler wins on key collision | No | Merged last in the per-handler env-var assembly pipeline so user vars can override SDK-injected vars (see 2.2). |

**Inheritance precedence**, applied per field rather than per resource, runs highest to lowest:

1. Handler spec field set directly on the resource.
2. Linked `celerity/handlerConfig` resource (via bluelink `linkSelector` label match). See 1.2.
3. `metadata.sharedHandlerConfig` at the blueprint level.
4. Defaults from the table above.

`handlerName` and `handler` are not inheritable and must be set on the handler resource itself. A missing `runtime` after inheritance is a fatal error.

### 1.2 `celerity/handlerConfig` abstract resource

A metadata-only carrier for shared handler configuration. It has no provider-side concrete form: the transformer consumes it during inheritance resolution (see 1.1) and strips it from the output blueprint. It is registered as an abstract resource so blueprint validation accepts it, though its transform function emits nothing.

Shape: any subset of the inheritable handler fields from 1.1 (`codeLocation`, `runtime`, `memory`, `timeout`, `tracingEnabled`, `environmentVariables`), plus standard bluelink `linkSelector` labels.

A blueprint may contain zero or more `celerity/handlerConfig` resources. Each handler can be linked to at most one via `linkSelector`. When a handler matches multiple, the transform fails fatally with no tiebreak.

### 1.3 `celerity/config` abstract resource

**Source of truth:** `celerity-docs: content/docs/framework/applications/resources/celerity-config.mdx`.

Blueprint authors declare `celerity/config` resources to request a dedicated secrets and configuration store. Fields accepted on `resource.spec`:

| Field | Type | Default | Required | Notes |
|---|---|---|---|---|
| `name` | string | generated from the blueprint | No | Name of the store in the target environment. |
| `values` | `mapping[string, string \| number \| bool]` | `{}` | No | Key/value pairs held in the store. Every value is stored **encrypted** unless its key is listed in `plaintext`. |
| `plaintext` | `array[string]` | `[]` | No | Keys in `values` that hold non-sensitive configuration and may be stored unencrypted. **This is the sensitivity switch** — there is no deploy-config key for it. |
| `replicate` | bool | `false` | No | Replicate the store across regions. Regions come from the deploy configuration (see 1.5 and the per-target contract). |
| `encryptionKeyId` | string | platform-managed key | No | Customer-managed encryption key for the store. Ignored when `replicate` is `true`; region-specific keys are supplied via deploy configuration instead. |
| `rotation` | object | (none) | No | 🚀 v1. Rotation for the **Celerity-managed** secrets auto-populated from links between handlers and infrastructure resources. |

**Output**: `spec.id` — the store's identifier in the target environment. Note this is a *store* identifier, not necessarily a single object's identifier: on a per-key backend such as SSM Parameter Store it is a **path prefix** (e.g. `arn:aws:ssm:us-west-2:123456789012:parameter/app/config/*`), because each key is its own parameter.

**Backend selection is derived from `plaintext`, not configured.** A store whose `plaintext` is empty or absent holds only secrets, and maps to the target's single-object encrypted-blob backend (AWS Secrets Manager). A store with at least one `plaintext` key maps to the target's per-key backend (AWS SSM Parameter Store), which can hold encrypted and unencrypted keys side by side. There is no deploy-config override for this choice: authors express intent by marking keys sensitive or not, and the transformer picks the backend that can represent that intent. See [aws-serverless.md §10](aws-serverless.md#10-runtime-configuration-store-aws-backends) for the AWS mapping.

**`celerity/config` declares user namespaces only.** The internal resource-links namespace (`resources`) is always a **separate, transformer-emitted store**; it is never merged into a user's `celerity/config` resource. See 3.1 for why, and for the backend it uses. Applications therefore do not need a `celerity/config` resource at all.

### 1.4 Celerity CLI build manifest

**Source of truth:** `celerity: apps/cli/internal/build/types.go` (top-level `Manifest` / `HandlerArtifacts`). Per-target sub-manifest types live alongside their respective targets (for example `celerity: apps/cli/internal/build/lambda_types.go` for `aws-serverless`).

Written by `celerity build` to `.celerity/build-manifest.json`. The top-level shape is **strategy-agnostic**: every deploy target owns a typed sub-manifest under its own key, and adding a future target (GCP Cloud Functions, Azure Functions, container) does not change the top-level shape.

**Top-level schema (v1)**

```jsonc
{
  "version": "1",
  "runtime": "<celerity runtime identifier>",
  "target": "<deploy target name>",
  "<targetKey>": { /* target-specific sub-manifest */ },
  "handlers": {
    "<handlerName>": {
      "<targetKey>": { /* per-handler, target-specific fields */ }
    }
  }
}
```

- `version` is `"1"` today. Changes require a version bump and a coordinated transformer update.
- `runtime` at the top of the manifest is the **Celerity runtime identifier** taken verbatim from the blueprint, not a cloud-native runtime value. The transformer, not the CLI, is responsible for mapping it to the concrete target runtime at transform time (see 2.3).
- `target` names the deploy target (one of the values listed in 1.5). It drives which sub-manifest the transformer reads and which companion target contract applies.
- `<targetKey>` is the target-specific top-level sub-manifest. For `aws-serverless` that key is `lambda`; see [aws-serverless.md](aws-serverless.md#1-build-manifest-lambda-sub-manifest) for its full schema.
- `handlers[name].<targetKey>` carries per-handler, target-specific fields. Each deploy target documents these in its own contract file.

**Division of responsibilities — CLI owns, transformer owns**

Recorded here because it is the single clearest expression of why the manifest is shaped the way it is. The CLI and transformer are loosely coupled through this schema; as long as the transformer reads the agreed shape from the sub-manifest and per-handler block, neither side can break the other by internal refactors. This table covers the shared slice; each per-target contract extends it with its own target-specific rows.

| Responsibility | Owner | Notes |
|---|---|---|
| Merging the extracted Celerity SDK handler manifest into the blueprint | CLI | The transformer sees the already-merged blueprint and never runs the SDK extractor. |
| Packaging code assets (shared or per-handler, as the target demands) | CLI | Recorded on the target sub-manifest. |
| Generating any target-specific entry point or bootstrap file | CLI | Recorded on the target sub-manifest; the transformer reads its value verbatim, never computes it. |
| Generating the internal resource-links routing file (`__celerity_resource_links__.json` on the Lambda path — see the deploy-mode name note in 3.2) into the code asset | CLI | Bundled next to the user app by the CLI build step. The SDK reads it from disk at cold start; it is never carried in an env var. See 3.2. |
| Building dependency artifacts (shared or per-handler) | CLI | Recorded on the target sub-manifest. |
| Resolving `celerity.buildManifest` (context variable) to manifest bytes | Transformer | Variable holds either an absolute filesystem path or an `s3://` URL depending on the deploy engine. The transformer handles both forms; the CLI does not pre-resolve the URL. |
| Emitting concrete provider resources, one per `celerity/handler` | Transformer | See the per-target contract for the concrete resource types. |
| Setting routing env vars on each handler (`CELERITY_HANDLER_ID`, `CELERITY_HANDLER_TYPE`, `CELERITY_HANDLER_TAG`) | Transformer | So the SDK adapter dispatches to the correct decorated handler at runtime (see 2.2). |
| Setting framework annotations (abstract grouping, resource category) on every emitted resource | Transformer | For deploy-cli-sdk TUI grouping and `--auto-approve-code-only` classification (see 2.1). |

**Location and delivery**

The manifest pointer is surfaced to the transformer via the transformer context variable `celerity.buildManifest`, read through `TransformerConfigVariable("celerity.buildManifest")`. The CLI sets this variable in `celerity: apps/cli/internal/deployconfig/pre_command_step.go` (see `injectBuildManifestPointer`) during the pre-command build step, and its value depends on the deploy engine:

- **Local deploy engine**: an absolute filesystem path to `.celerity/build-manifest.json`. The transformer opens the file directly.
- **Remote deploy engine**: a **target-specific remote URL** pointing to the uploaded manifest. The URL scheme is picked by the deploy target — for example `s3://bucket/key` on `aws-serverless` (see [aws-serverless.md §1](aws-serverless.md#1-build-manifest-lambda-sub-manifest)). Future targets (GCP, Azure, and the planned `aws` container target) will use whichever storage backend the target already uses for artifact upload. The CLI uploads the manifest as part of the pre-command step, reusing that backend; the transformer must fetch the URL itself using the available target credentials, since the CLI does not resolve the URL back to a local path before handing off.

In both cases the CLI also persists a local copy of the manifest at `.celerity/build-manifest.json`. That copy carries a `RemoteURL` field (see `celerity: apps/cli/internal/build/types.go`, comment `Populated only in the locally-persisted copy`) recording the remote URL when an upload happened. The transformer reads from the context variable, not from the local copy, so this field is for debugging and human inspection rather than the transformer's own consumption.

**Fallback behaviour**

When the context variable is unset, or the manifest cannot be obtained (filesystem path missing or unreadable; S3 fetch fails with no cached copy), the transformer produces syntactically valid target resources but omits code-asset references and entry-point values, logs a warning, and relies on downstream validation to reject the output if the deploy is not a dry run. This preserves the ability to lint and plan a blueprint before `celerity build` has run.

**Types duplication note** (engineering detail, not a contract item): because the CLI package is under `internal/`, Go's internal-package rule forbids external modules from importing it. When the transformer implementation lands it will copy the shared `Manifest` and `HandlerArtifacts` types — plus the relevant target sub-manifest types — into a local `internal/buildmanifest` package with a stability-note comment pointing back at the CLI source. The long-term fix is to expose a public package that can be used by both the CLI module and the transformer, and eventually remove the copy.

### 1.5 Celerity CLI deploy configuration

**Source of truth:** `celerity: apps/cli/internal/deployconfig/convert.go` and `celerity: apps/cli/internal/compose/consts.go`.

The CLI reads `app.deploy.jsonc`, splits its keys by prefix, and passes a `BluelinkDeployConfig` to the deploy engine. Every configuration value the Celerity transformer receives arrives under `Transformers["celerity"]` or as a context variable on the `TransformerContext`.

**Valid `deployTarget.name` values** (from `celerity: apps/cli/internal/compose/consts.go`, L18-26) are `aws`, `aws-serverless`, `gcloud`, `gcloud-serverless`, `azure`, and `azure-serverless`. Every transformer branch that needs to dispatch by target reads this via `TransformerConfigVariable("deployTarget")`.

**Shared transformer-relevant keys:**

| Source | Key | Read via | Value, default |
|---|---|---|---|
| `deployTarget.name` | `deployTarget` | `TransformerConfigVariable("deployTarget")` | one of the values above |
| `appName` | `celerity.appName` | context variable | **required**; the project name — a first-class top-level field in `app.deploy.jsonc`, not a `deployTarget.config` key. Used for resource naming and configuration-store paths (see [aws-serverless.md §10.3](aws-serverless.md#103-user-defined-celerityconfig-stores)). Constrained to `^[a-zA-Z0-9][a-zA-Z0-9-_]*$`. The CLI validates presence and surfaces it via `ConvertToBluelinkFormat` (`celerity: apps/cli/internal/deployconfig/convert.go`). |
| `deployTarget.appEnv` | `appEnv` | context variable | for example `staging`, `production` |
| CLI `PreCommandStep` | `celerity.buildManifest` | context variable | absolute path or S3 URL (see 1.4) |

`appName` is a **first-class authoring item**: it is a named field at the top level of `app.deploy.jsonc`, deliberately not buried in the generic `contextVariables` map, so the project name is an explicit part of the authoring experience. As with the build manifest, it may be absent in a validation context — the transformer must tolerate that and only require it when emitting resources that depend on it.

**Target-specific keys** (`aws.*`, `gcloud.*`, `azure.*` and their equivalents, including provider-scoped and transformer-scoped config) are documented in the target environment section of the Celerity resource type [specifications](https://celerityframework.io/docs/framework/applications). For example, `aws-serverless` uses `aws.lambda.*`, `aws.config.*`, and related prefixes; see [aws-serverless.md §2](aws-serverless.md#2-deploy-configuration-aws-specific-keys).

---

## 2. Shared transformer outputs

Concerns documented here apply to every deploy target. Target-specific concrete resource emission, env var injection, IAM or equivalent access policies, and runtime mapping tables live in the per-target contract documents linked from section 7.

### 2.1 Framework grouping annotations

**Source of truth:** `bluelink: libs/plugin-framework/sdk/pluginutils/transformer_annotations.go`.

Every emitted concrete resource MUST carry these three metadata annotations. They are produced via the plugin-framework helper `pluginutils.TransformerBaseAnnotations`, and the transformer uses that helper uniformly rather than writing keys directly.

| Annotation key | Constant | Value | Consumer |
|---|---|---|---|
| `bluelink.transform.source.abstractName` | `pluginutils.AnnotationSourceAbstractName` | Name of the abstract resource (e.g. `celerity/handler`) in the input blueprint | deploy-cli-sdk TUI grouping, see `deploy-cli-sdk: tui/shared/abstract_resource_grouping.go` |
| `bluelink.transform.source.abstractType` | `pluginutils.AnnotationSourceAbstractType` | Literal string (e.g. `celerity/handler`) | same |
| `bluelink.transform.resourceCategory` | `pluginutils.AnnotationResourceCategory` | `code-hosting` or `infrastructure` | code-only approval gating, see `deploy-cli-sdk: tui/shared/code_only_approval.go` |

**The category values are exhaustive**: only `code-hosting` and `infrastructure` are defined by the framework. Whether a given emitted resource is classified as `code-hosting` or `infrastructure` is a per-target decision, documented in each target's contract.

**Missing annotations silently break TUI grouping and code-only approval**. Resources without them are rendered ungrouped and never auto-approve. This is the canonical reason the contract says *must*: a function emitted without the annotations will still deploy, so there is no hard failure; the regression only shows up at review time.

### 2.2 SDK runtime contract: shared env vars

**Source of truth:**
- `celerity-node-sdk: packages/serverless-aws/src/adapter.ts`
- `celerity-node-sdk: packages/config/src/config-layer.ts`
- `celerity-python-sdk: src/celerity/serverless/aws/adapter.py`
- `celerity-python-sdk: src/celerity/config/layer.py`

This section lists the environment variables the runtime SDKs read that are **shared across deploy targets**. Target-specific variables — including `CELERITY_PLATFORM`, cloud-provider SDK credentials, and backend-specific store IDs — are documented in each per-target contract.

**Legend for "Injected by transformer"**:
- ✅ always: set on every handler
- ❓ optional: set only when certain conditions are met
- 🔗 on link: set when the handler is linked to relevant upstream or downstream resources, or when the blueprint declares relevant resources
- 🧑 user: originates from `spec.environmentVariables` and passes through untouched
- ⛔ never: the SDK reads it, but the transformer never sets it; the runtime environment or another component does

#### Bootstrap

| Env var | Injected by transformer | Source, value | Purpose |
|---|---|---|---|
| `CELERITY_PLATFORM` | ✅ always | target-specific literal (see the per-target contract) | Selects the target provider wiring in the SDK bootstrap. |
| `CELERITY_RUNTIME` | ⛔ never | (unset) | Presence indicates long-lived runtime mode; serverless mode omits it. The transformer leaves it unset. |
| `CELERITY_DEPLOY_TARGET` | ✅ always | the deploy-target value from `EnvInput.DeployTarget` (e.g. `aws-serverless` on the AWS handler path) | Injected by the transformer; indicates the deploy target environment to help in resource backend selection (e.g. AWS DynamoDB to back a `celerity/datastore` in an AWS deployment) |

#### Handler routing

These three vars are the contract between the transformer and the SDK adapter that runs inside the target's generated bootstrap. The adapter reads these vars plus the incoming event shape to pick which user-decorated handler to call. The handler's `handler` field from the blueprint is used as the **handler id** — the SDK runtime treats it as a `moduleName.exportName` code-entry reference, splitting on the last dot to dynamically import and resolve the user's handler (see `celerity-node-sdk: packages/core/src/handlers/module-resolver.ts`; the Python SDK dispatches identically by `handler.id`). `handlerName` is **not** used for dispatch — it is runtime observability metadata only (and the emitted function name; see 1.1).

| Env var | Injected by transformer | Source, value | Purpose |
|---|---|---|---|
| `CELERITY_HANDLER_ID` | ✅ always | `spec.handler` from the `celerity/handler` resource | Primary routing key. A `moduleName.exportName` code-entry reference the SDK adapter imports and resolves to dispatch the incoming event to the correct user handler. Unique per emitted function. |
| `CELERITY_HANDLER_TYPE` | ✅ always | one of `http`, `websocket`, `consumer`, `schedule`, `custom`, derived from the handler's event-source link (or the handler kind in the spec) | Forces the Node.js SDK adapter's event-type detection so it does not have to infer type from the event shape. Python auto-detects and tolerates this being set; the transformer sets it uniformly for both runtimes so the contract is symmetric. |
| `CELERITY_HANDLER_TAG` | ✅ when the handler declares a routing tag | handler spec tag field (e.g. the queue name for a consumer, the schedule key for a scheduled handler) | Optional secondary lookup key used by the SDK's consumer and schedule dispatchers when multiple handlers of the same type share one function. This is not set for `http`, `websocket`, and `custom` handlers. |

#### Resource link discovery (runtime configuration store)

All entries below depend on section 3. The **user-namespace** entries (`celerity/config` stores) are populated from resolved outbound links; the internal `resources` store's identifiers are set **directly by the transformer** as literals, not via a link (see the per-target contract for why). The table documents the final contract for the resource-link configuration store.

The SDK discovers namespaces from the env-var names themselves (`celerity-node-sdk: packages/config/src/config-layer.ts`, `discoverNamespaces`). The transformer always uses the **per-namespace** `CELERITY_CONFIG_<NS>_*` form, including for the internal `resources` namespace, where `<NS>` is `RESOURCES`.

| Env var | Injected by transformer | Source, value | Purpose |
|---|---|---|---|
| `CELERITY_CONFIG_RESOURCES_STORE_ID` | ❓ when the handler links a store-backed resource | **transformer-set literal**: the internal store's path prefix, set **directly** on the qualifying function — **not** via a link. The internal store is a shared-parent resource that cannot carry a link-selector label, so the transformer writes this env var itself (see the per-target contract). | Points the SDK at the internal resource-links store. Namespace name resolves to `resources`, matching the SDK's `RESOURCE_CONFIG_NAMESPACE` (`packages/config/src/resource-links.ts`). |
| `CELERITY_CONFIG_RESOURCES_STORE_KIND` | ❓ when the handler links a store-backed resource | **transformer-emitted literal** naming the backend kind (see 3.1), `"parameter-store"` on `aws-serverless`; set directly on the function, not link-resolved | Selects which backend the SDK's `ConfigService` instantiates for the `resources` namespace. **Must be set explicitly**: when unset the SDK defaults a namespace to `secrets-manager`, not to the target's default. |
| `CELERITY_CONFIG_<NS>_STORE_ID` | 🔗 on link, per `celerity/config` | **link-provided** store identifier for namespace `<NS>` | User namespaces declared via `celerity/config` resources. |
| `CELERITY_CONFIG_<NS>_STORE_KIND` | 🔗 on link, per `celerity/config` | **transformer-emitted literal** naming the backend kind for namespace `<NS>` | Per-namespace backend choice. There is **no inheritance** from any global value; unset means `secrets-manager`. |
| `CELERITY_CONFIG_<NS>_NAMESPACE` | 🔗 on link, per `celerity/config` | namespace name | Namespace name override, defaulting to the lowercase env-key suffix. |
| `CELERITY_CONFIG_<NS>_STORE_PREFIX` | 🔗 on link, per `celerity/config` | key prefix | Filters fetched keys to those under `<prefix>/`, stripping the prefix. Distinct from the store id: the id locates the store, the prefix selects keys **within** it. |

> **The transformer must never set `CELERITY_CONFIG_STORE_ID`.** It is the SDK's single-namespace shortcut: when present, `discoverNamespaces` returns exactly one namespace called `default` and **ignores every `CELERITY_CONFIG_<NS>_STORE_ID` in the environment**. Setting it alongside the `resources` namespace would silently strip resource-link resolution from every handler. `CELERITY_CONFIG_STORE_KIND` is only read in combination with it, so it is likewise unused.

#### Telemetry and logging

| Env var | Injected by transformer | Source, value | Purpose |
|---|---|---|---|
| `CELERITY_TELEMETRY_ENABLED` | ✅ when `resolved.tracingEnabled` | `"true"` | Enables OpenTelemetry bootstrap in the SDK. |
| `CELERITY_LOG_FORMAT` | ✅ when unspecified | `"json"` | Structured logs by default. User may override via `spec.environmentVariables`. |
| `CELERITY_LOG_LEVEL` | 🧑 user | from `spec.environmentVariables` | SDK default is `info`; user override. |
| `CELERITY_LOG_FILE_PATH` | ⛔ never | (unset) | Not meaningful in serverless environments, where the filesystem is generally ephemeral. |
| `CELERITY_LOG_REDACT_KEYS` | 🧑 user | from `spec.environmentVariables` | Comma-separated list of log keys to redact. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | ✅ when `resolved.tracingEnabled` is `"true"` | OTLP collector endpoint from telemetry configuration | Future phase. Not set in Phase 1. |
| `OTEL_SERVICE_NAME` | ✅ when `resolved.tracingEnabled` is `"true"` | handler name or app name | Future phase. |
| `OTEL_SERVICE_VERSION` | 🔗 on link (future) | blueprint version metadata | Future phase. |

#### User-supplied

`spec.environmentVariables` from 1.1 is merged **last** into each handler's environment variable set. User-set keys override transformer-injected keys so authors can deliberately pin, for example, `CELERITY_LOG_LEVEL=debug` for a single handler.

### 2.3 Runtime identifier mapping (concept)

**Source of truth:** `celerity-docs: content/docs/framework/applications/resources/celerity-handler.mdx` (the "Runtimes" section) and `celerity-docs: content/docs/framework/runtime/lifecycle.mdx`.

Blueprint authors write a **Celerity runtime identifier** in `spec.runtime`, not a cloud-native runtime string. The transformer is responsible for mapping that identifier to the value the target cloud provider expects in its concrete runtime field. The actual mapping table is per-target and lives in each target's contract (for `aws-serverless`, see [aws-serverless.md §6](aws-serverless.md#6-runtime-identifier-mapping-aws-serverless)).

**Resolution policy** (shared across targets):

- The mapping is an **exact lookup** in the per-target table. There is **no** nearest-version or same-language-track fallback: the transformer maps only identifiers it has an explicit table entry for.
- An identifier with no matching table entry — unrecognised, or a runtime the current transformer version does not map for this target — is an **error**. The transformer emits an error diagnostic and does not emit a function for that handler.

**Unknown or unmapped identifiers** are an error, not a silent substitution. The diagnostic names the identifier and the deploy target so operators can see immediately which runtime is unsupported.

---

## 3. Runtime configuration store

This section is the backbone of how the Celerity SDK resolves physical resource identifiers at runtime. Every real application (anything beyond hello-world) needs it. The transformer may emit nothing for it in early phases since a blueprint with zero outbound links does not require a store, but the architecture reserves space for it, and this document pins down the shared contract so per-target implementation work is content-only rather than structural.

### 3.1 Interface abstraction

The Celerity SDK's `ConfigService` (see the config-layer source-of-truth files listed in 2.2) abstracts over interchangeable backends behind one interface. Handlers never branch on backend choice. The transformer and the SDK bootstrap both know which backend is active; user code does not.

**Two store purposes**:

1. **Internal resource-links store** (namespace `resources`), auto-populated by the transformer. Contains the map from resource name to physical identifier for every resource a handler in the blueprint links to. This is what the SDK's DI layer resolves when a handler writes `@Inject(resourceToken("queue", "orders"))`.
2. **User-defined config stores** (namespaces named by the author), declared in the blueprint as `celerity/config` resources (see 1.3). Hold arbitrary user configuration such as feature flags, third-party API keys, and tuning constants.

Both use the same `ConfigService` interface and the same routing-file-plus-store-identifier wiring convention, and both are addressed through the `CELERITY_CONFIG_<NS>_*` env-var family in 2.2. The difference is who populates the content and which backend stores it.

**The `resources` namespace is a separate store, not a reserved key prefix inside a user store.** The alternative — writing auto-populated link values into whichever `celerity/config` the author declared, under a reserved key prefix — was rejected. A separate store keeps the two lifecycles independent (the transformer owns one, the author owns the other), avoids a key-collision surface between generated and authored keys, keeps `plaintext` a purely authorial decision, and lets an application have resource links with no `celerity/config` resource at all. It is also what the SDK implements: `resources` is a well-known namespace constant, and namespaces are discovered independently from the environment.

**The `resources` namespace is not uniformly non-sensitive.** Most of what it holds is a physical identifier — a queue URL, a table name, a bucket name — that is already visible in the blueprint, in bluelink state, and in the deploy plan. But some links auto-populate credentials (this is exactly what `celerity/config`'s `rotation` field exists to rotate). The store therefore holds a **mix**, and the backend chosen for it must be able to represent both: unencrypted physical identifiers alongside encrypted credentials. This drives the per-target default in 3.1's backend table.

**Backend kinds**

The `ConfigService` abstracts over one or more **managed-store backends** provided by the deploy target — for example Parameter Store and Secrets Manager on AWS, and the equivalent managed stores on GCP and Azure. Backends fall into two shapes, and the distinction matters because it determines what a store can express:

- **Single-object encrypted blob** (AWS Secrets Manager): the whole store is one encrypted object holding a JSON map. Every key is encrypted, or none is. The store id identifies that one object.
- **Per-key store** (AWS SSM Parameter Store): each key is its own object with its own encryption setting, so encrypted and unencrypted keys coexist. The store id is a **path prefix**, and the SDK enumerates keys beneath it.

The complete list of backends supported on each target, which one is used for the internal `resources` namespace, and their access-model requirements are documented in the per-target contract (for `aws-serverless`, see [aws-serverless.md §10](aws-serverless.md#10-runtime-configuration-store-aws-backends)).

There is **no env-var backend**. Lambda's 4 KB env-var cap (and analogous caps on other serverless targets) makes env vars unworkable as a general-purpose config store, so every backend fronts a real store that the SDK fetches from at cold start. The transformer's env-var injection is deliberately limited to the small set of store-identifier scalars listed in 2.2; the routing metadata that maps DI tokens to store lookup keys lives in a build-bundled file instead (see 3.2).

**Backend choice is derived, never configured.** For a user-defined `celerity/config`, the `plaintext` field decides it (1.3): all-secret stores take the blob backend, mixed stores take the per-key backend. For the internal `resources` namespace, the mix described above means the per-key backend is always required. Neither case reads a deploy-config key, and the transformer will not second-guess an author who marks a key sensitive.

### 3.2 How the SDK finds values

At cold start the SDK reads the routing map from the CLI-generated resource-links routing file, bundled next to the user app in the target's code asset by the CLI (see 1.4). For each DI token the map records `{type, configKey}` — `type` selects the resource layer the SDK instantiates, and `configKey` is the handle the SDK uses to fetch the actual value from the active backend.

> **Routing-file name — two deploy-mode variants (CLI-owned).** The CLI, not the transformer, writes this file, and the name differs by deploy mode:
> - **FaaS / Lambda deployment package** (the `app.zip` bundled next to the user app, read from `/var/task` at cold start): **`__celerity_resource_links__.json`** (`celerity: apps/cli/internal/build/types.go`, written by `internal/build/lambda.go`). This matches the SDK's default `RESOURCE_LINKS_FILENAME` (`celerity-node-sdk: packages/config/src/resource-links.ts`), so it is the name that must land in the Lambda code asset.
> - **Local dev container**: **`resource-links.json`** (`celerity: apps/cli/internal/seed/resource_links.go`), mounted at `/opt/celerity/resource-links.json` and located via the `CELERITY_RESOURCE_LINKS_PATH` env var.
>
> This document describes the aws-serverless (Lambda) path, so it refers to `__celerity_resource_links__.json`. The transformer never emits the routing file; it only guarantees its store parameter names equal the CLI's `configKey` derivation.

**Backend selection**: `CELERITY_CONFIG_RESOURCES_STORE_KIND` tells the SDK which backend to instantiate for the `resources` namespace (see 3.1), and `CELERITY_CONFIG_RESOURCES_STORE_ID` points it at the concrete store. The SDK wires up exactly one backend implementation per namespace; the handler code above it is unchanged.

**Value lookup**: the SDK fetches the namespace's whole key set once per cold start, caches it across invocations, and looks up `configKey` in the resulting map. *How* those keys are fetched depends on the backend shape from 3.1 — a blob backend does one get-and-parse, a per-key backend enumerates the path prefix — but both produce the same flat `key → value` map, so `configKey` resolution is identical either way. The exact fetch mechanism is per-target and documented in each target's contract.

Because both shapes collapse to the same map, the routing file never needs to know which backend is active.

Example routing-file contents:

```json
{
  "orders-queue":  { "type": "queue",     "configKey": "orders-queue" },
  "events-topic":  { "type": "topic",     "configKey": "events-topic" },
  "orders-db":     { "type": "datastore", "configKey": "orders-db" }
}
```

**Why a bundled file rather than an env var**: the routing map is 100% build-time-knowable — resource names, types, and config keys all derive from the blueprint with no deploy-time values. Carrying it in an env var would consume space in every handler's env-var budget for information that never changes at deploy time. On AWS Lambda, where the total env-var payload is capped at 4 KB, this matters in practice for any project with more than a handful of resources. Shipping the map inside the code asset instead keeps the env-var budget free for the handful of truly runtime-dynamic scalars — the store identifiers the SDK needs to reach the config store.

### 3.3 Comparison, diffing, and drift detection

The routing file's byte contents are the canonical form for drift detection of resource-link topology. The CLI writes the file with stable (lexicographically sorted) key ordering, so two builds with identical logical content produce byte-identical file contents and therefore byte-identical contentHashes on the code asset. Drift is a single content-hash comparison rather than a flurry of per-env-var diffs.

Concrete consequences:

- **`configKey` uniqueness**: the component that writes the routing file (the CLI, on the transformer's behalf per 1.4) MUST verify that every `configKey` within a namespace is unique, and fail fatally if two resources would collapse to the same key. There is no tiebreak. The check is enforceable without reading the SDK because it depends only on the blueprint.
- **Between deploys**: the engine's reconciliation diff between the deployed code asset and the blueprint's desired state is a contentHash comparison on the shared code asset. Resource topology changes — rename, type change, new link, removed link — surface in the deploy plan as a code-asset update, not as a flurry of per-env-var diffs.
- **Between backends**: a namespace's backend can change without any deploy-config edit, because it is derived (3.1) — adding the first `plaintext` key to a `celerity/config` migrates it from the blob backend to the per-key backend. Such a switch is modelled as a full replacement of that namespace's config-store plumbing regardless of whether the underlying logical values changed: the concrete store resource, access grants, and store-identifier env vars all change, and the diff engine treats it as a wholesale replacement. This keeps the diff engine simple and makes backend changes visible in the deploy plan. Authors should expect a store replacement, not an in-place edit, the first time they mark a key as `plaintext`.
- **Routing-file stability across runs**: because the CLI writes with sorted keys, the file never produces false diffs from non-deterministic map iteration order or JSON whitespace variation.

---

## 4. End-to-end flow

```text
   ┌──────────────┐                  ┌─────────────────┐
   │  celerity    │                  │ app.deploy.jsonc│
   │    build     │                  │                 │
   └──┬───────────┘                  └────────┬────────┘
      │                                        │
      │ writes                                 │ read + split
      ▼                                        ▼
 ┌────────────────────────┐        ┌─────────────────────────┐
 │ .celerity/             │        │ ConvertToBluelinkFormat │
 │   build-manifest.json  │        │  → Transformers[celerity]│
 │ (v1 schema, 1.4)       │        │  + ContextVariables     │
 └────────────┬───────────┘        └──────────────┬──────────┘
              │                                    │
              │ path via                           │ deployTarget,
              │ celerity.buildManifest             │ appEnv,
              │ context variable                   │ target-specific keys,
              │                                    │ celerity.buildManifest
              │                                    │
              └──────────────┬─────────────────────┘
                             ▼
            ┌──────────────────────────────────┐
            │   Celerity Transformer Plugin    │  ← reads plugin-framework
            │        (THIS REPO)               │    annotation constants (2.1)
            │                                  │  ← reads spec contract (1.1)
            │  transformBlueprint()            │  ← reads deploy config (1.5)
            │    1. Build context +            │  ← reads build manifest (1.4)
            │       planners                   │
            │    2. Per-resource dispatch      │
            │    3. Finalize(role, layer,      │
            │                configStore)      │
            └──────────────────┬───────────────┘
                               │ emits
                               ▼
            ┌──────────────────────────────────────┐
            │  Target-specific concrete resources  │
            │  (see per-target contract for list)  │
            │  (all carrying framework annotations)│
            └──────────────────┬───────────────────┘
                               ▼
               ┌────────────────────────────┐
               │   Bluelink Deploy Engine   │
               └──────────────┬─────────────┘
                              ▼
               ┌────────────────────────────┐
               │   deploy-cli-sdk TUI       │
               │   groups by                │
               │   bluelink.transform.      │
               │     source.* annotations   │
               └────────────────────────────┘
```

---

## 5. Versioning and maintenance

**Spec version implemented by this plugin**: `celerity-2026-02-27-draft`, the value returned as `TransformName` from [`transformer.NewTransformer()`](../../transformer/transformer.go). **Build manifest version consumed**: `v1`, the constant `build.ManifestVersion`.

**Maintenance rule**: every PR that adopts an upstream change (a new SDK env var, a new CLI manifest field, a new plugin-framework annotation, or a provider schema field rename) must update this document and the relevant per-target contract in the same PR. Drift between these docs and the transformer implementation is a bug. Reviewers of implementation PRs should diff the implementation against the relevant sections here and in the target contract.

**File reference notation**

Inline references to files in other repositories use the format `` `repo-name: path/within/repo` ``, for example `` `celerity: apps/cli/internal/build/types.go` ``. The `repo-name` part identifies the project without tying the reference to any particular checkout location. Line ranges, when relevant, are appended after the path (for example `L18-26`). References to files inside this repository use standard relative markdown links.

The short names used throughout this document and their canonical projects:

| Short name | Project |
|---|---|
| `bluelink-transformer-celerity` | this repo (the Celerity transformer plugin) |
| `celerity` | the Celerity monorepo containing the CLI and related apps |
| `celerity-docs` | the Celerity framework documentation repo |
| `celerity-node-sdk` | the Celerity Node.js runtime SDK |
| `celerity-python-sdk` | the Celerity Python runtime SDK |
| `bluelink` | the Bluelink monorepo containing blueprint and plugin-framework libraries |
| `bluelink-provider-aws` | the Bluelink AWS provider plugin |
| `deploy-cli-sdk` | the shared deploy CLI SDK containing the TUI and code-only approval |

Individual source-of-truth references are declared inline in each section that depends on them (look for the **Source of truth:** callouts in sections 1.1, 1.4, 1.5, 2.1, 2.2, and 2.3). There is no consolidated table on purpose: keeping the references next to the prose they support means they get updated together, and avoids a second copy that can drift.

---

## 6. Deploy target contracts

Every supported deploy target has a companion contract document that pins down its concrete resource types, provider runtime mapping, target-specific build-manifest sub-manifest, access-model details, and any target-specific env vars the SDK reads.

| Deploy target | Contract |
|---|---|
| `aws-serverless` (AWS Lambda) | [aws-serverless.md](aws-serverless.md) |

Additional targets (`aws`, `gcloud`, `gcloud-serverless`, `azure`, `azure-serverless`) will have their own contract files as they are implemented.
