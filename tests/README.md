# Transformer test tiers

Beyond the per-resource unit tests in `transformer/` and `links/`, this
directory holds two suites that exercise the transformer through the real
bluelink blueprint container together with the real
`bluelink-provider-aws` provider.

## `tests/pipeline` — full pipeline, no upstream service calls (`//go:build unit`)

Runs in normal CI on every PR (`bash scripts/run-tests.sh --unit`). Each test
drives: `.blueprint` parse → abstract validation → declared-link-graph
construction → celerity transform → concrete validation against the real AWS
provider schemas → change staging for a new instance. No credentials, no
calls made to upstream services.

The harness (`harness.go`) drives a single `Loader.Load` for the whole
pipeline. Blueprint v0.51.1 fixed the four framework bugs this suite
originally worked around (discarded transform output, unresolvable abstract
namespaces, the schema-walk infinite recursion, and subwalk's nil-vs-empty
`Items` normalisation), and bluelink-provider-aws v0.4.1 dropped the
RE2-incompatible schema patterns — so the former two-phase load, the
`abstractNamespaceProvider` adapter, the schedule-specific validation bypass
and the `patternSanitizingProvider` are all gone.

## Iterating against local upstream checkouts

A gitignored `go.work` at the repo root points builds at the local
`bluelink` monorepo (`libs/blueprint`, `libs/plugin-framework`) and
`bluelink-provider-aws` checkouts, so upstream fixes can be validated against
these suites without cutting releases:

```
go test -tags=unit ./tests/pipeline/   # uses the local checkouts
GOWORK=off go test -tags=unit ./...    # uses the released go.mod versions
```

CI never sees `go.work` (gitignored), so it always builds against the released
versions pinned in `go.mod`. Before relying on a green run to claim an
upstream bug fixed, re-run with `GOWORK=off` to know which side you tested.

## `tests/e2e` — real AWS deploys (`//go:build e2e`)

Run explicitly via `bash scripts/run-tests.sh --e2e` (sources `.env.test`;
requires `AWS_REGION` and credentials, tests skip without them) or the
`e2e-tests.yaml` workflow (weekly + manual dispatch). Concurrency is capped by
`E2E_CONCURRENCY` (defaults to 6). The workflow assumes the OIDC role named by the
`E2E_AWS_ROLE_ARN` repository variable;
[`docs/e2e/github-actions-role-policy.json`](../docs/e2e/github-actions-role-policy.json)
is the inline permissions policy for that role. Everything mutable is scoped
to the run-unique `celerity-e2e-*` name prefix (plus the `/celerity/*`
SSM/secret trees, `bluelink-link-access-*` managed policies and un-scopable
list/read calls the leak sweep needs), so keep that policy in sync when a
fixture starts deploying a new AWS service.

The harness pre-stages real artifacts per run: a unique S3 bucket, in-memory
stub `app.zip`/layer zips, and a generated `build-manifest.json` handed to the
transformer via the `celerity.buildManifest` context variable. Every test
registers destroy + artifact cleanup via `t.Cleanup` before deploying.

This repo's e2e tests prove the emitted resources deploy and the
transformer-authored runtime pieces work (e.g. the queue→topic forwarder
actually forwards). Build-manifest generation, artifact packaging and the
transformer gRPC plugin surface belong to CLI / Deploy Engine e2e.
