# Bluelink Transformer for Celerity

The Bluelink transformer plugin (`newstack-cloud/celerity`) that turns Celerity's abstract
resources (`celerity/handler`, `celerity/api`, `celerity/queue`, ...) and their links into
concrete resources for a deploy target, today AWS serverless through bluelink-provider-aws.
The Celerity CLI configures it for the deploy engine, and the Bluelink Registry publishes its
docs.

## Workspace

This repo is one of NewStack's. NewStack's maintainers develop it alongside the others in a
shared workspace, using the NewStack Claude Code plugin for how work is planned, reviewed,
committed and coordinated across repos. The plugin isn't public, and nothing here depends on it:
this file and the repo's own docs cover what a contribution needs.

## Layout

| Path | Holds |
|---|---|
| `transformer/` | The transformer: plugin wiring, the resolve and emit pipeline, and most per-resource tests (`*_transform_test.go`) |
| `resources/<type>/` | Each abstract resource: schema, resolve, and its emit and property map per target |
| `links/` | Links between abstract resources, and to `vpc` |
| `shared/`, `types/` | Shared helpers (build manifest loading in `shared/build`, AWS helpers in `shared/aws`) and types |
| `tests/pipeline`, `tests/diagnostics` | Parse → transform → validate → stage against the real provider schemas, with no calls out ([tests/README.md](tests/README.md)) |
| `tests/e2e` | Deploys `testdata/` blueprints to a real AWS account, in process (provider, transformer and in-memory state; no engine or docker) |
| `docs/contract/` | The transformer contract: what it accepts from the CLI, SDKs and specs, and what it emits ([index.md](docs/contract/index.md), per target in `aws-serverless.md`) |
| `docs/design/` | Design documents |

The contract is the reference implementation changes are checked against: a change to what
the transformer accepts or emits updates it in the same pull request.

## Checks

Commands are in [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

- **Tools:** Go (`go.mod`), `golangci-lint` v2. Integration tests also need docker, the AWS
  CLI with `awslocal`, `jq` and `curl`.
- **Lint:** `golangci-lint run --build-tags=unit,integration,e2e ./...`. Without
  `--build-tags`, as CI runs it, lint skips every tagged file: nearly all tests and the
  `tests/` harnesses.
- **Build tags:** test files are tagged `unit`, `integration` or `e2e` (all but
  `shared/aws/identity_tags_test.go`), so plain `go test ./...` runs almost nothing. A focused
  run: `go test -tags=unit -count=1 ./transformer/ -run <Suite>`. `go vet -tags=e2e
  ./tests/e2e/...` compiles the e2e suite without deploying.
- **`scripts/run-tests.sh`** (`--unit`, `--integration`, `--all`, `--e2e`; default `--all`):
  - `--unit` runs the unit tests and `tests/pipeline`, with no external calls.
  - `--integration` and `--all` load `.env.test` if present, start
    `docker-compose.test-deps.yml` (LocalStack on 4579, fixed container name, so one run at a
    time), seed S3 and set test AWS credentials. The tests reach only LocalStack.
  - `--e2e` loads `.env.test` (from `.env.test.example`) and needs `AWS_REGION` and
    credentials. It deploys to a real AWS account, six tests at a time
    (`E2E_CONCURRENCY`), for up to an hour. After a normal finish it sweeps the account for
    resources this run leaked and fails if it found any; a timeout or killed process skips
    that and can leave resources to delete by hand. `--all` never includes it.
- **Local upstream checkouts:** a gitignored `go.work` can point builds at local bluelink and
  bluelink-provider-aws checkouts; CI never sees it. Re-run with `GOWORK=off` before claiming
  an upstream fix, as [tests/README.md](tests/README.md) explains.
- **Gates:**
  - No git hooks: commitlint and lint run only when you run them.
  - CI (`ci.yaml`) runs `--unit`, lint and SonarCloud on pull requests and pushes to `main`.
    Nothing in CI runs `--integration`, so run it after touching `shared/build` or
    `shared/aws`. `e2e-tests.yaml` runs the e2e suite every Monday and by hand; nothing runs
    it on a pull request.

## Running locally

The transformer runs as a plugin of the deploy engine, which finds it under each root in
`BLUELINK_DEPLOY_ENGINE_PLUGIN_PATH` at `transformers/newstack-cloud/celerity/<version>/plugin`.
To check docgen output without touching an engine, build into the gitignored `plugins/`
directory and run `bluelink-plugin-docgen -plugin=newstack-cloud/celerity` with the path set to
it, as `release.yaml` does. See Safety.

## Compatibility

- **Abstract resource types:** the spec fields, link annotations and defaults of each
  `celerity/*` type are what Celerity blueprints are written against, and celerity-docs
  documents them (`content/docs/framework/applications/resources/`). Additive only; a rename
  or removal needs the maintainer's agreement and matching changes in celerity's
  `blueprint-config-parser` and CLI.
- **Inputs:** the Celerity CLI's build manifest (schema version `"1"`, from celerity's
  `apps/cli/buildmanifest`; the transformer doesn't check it), its deploy configuration and the
  SDKs' runtime conventions, as `docs/contract/` records. A manifest that can't be loaded
  doesn't fail the transform: handlers get placeholder code and a warning, so validation and
  plans work before `celerity build`, but a deploy fails.
- **Outputs:** changing the names or types of emitted resources makes the next deploy of an
  existing app replace them. No deployed engines exist yet (as of 2026-10-21), so that's
  allowed; say in the pull request what changed.
- **Dependencies:** built against the bluelink libraries and bluelink-provider-aws versions in
  `go.mod`. Renovate raises them and auto-merges once CI passes, but doesn't touch
  `bluelink-registry-info.json`, which declares the provider range the registry installs
  (`^0.5.0`): a provider minor bump needs that range raised in the same pull request.
- **Registry docs:** releases publish `docs.json` from `bluelink-plugin-docgen` (pinned in
  `release.yaml`), which the Bluelink Registry reads.

## Conventions

- Commits: [docs/COMMIT_GUIDELINES.md](docs/COMMIT_GUIDELINES.md), with the types in
  `commitlint.config.js` (`deps:` for dependency updates, as Renovate writes them). Scopes are
  free-form and optional (`feat(vpc): ...`).
- Releases: svu sets the version from `feat`, `fix` and breaking commits, and git-cliff writes
  the changelog, through a release pull request on `main`
  (`docs/SOURCE_CONTROL_RELEASE_STRATEGY.md`). `chore:` and `deps:` commits alone don't release.
  Don't edit `CHANGELOG.md` or create tags.

## Safety

- Don't run `scripts/run-tests.sh --e2e`, `go test -tags=e2e`, or the e2e workflow unless
  asked: each deploys to a real AWS account.
- Don't build the plugin into a deploy engine's plugin path or restart an engine: it may be
  serving end-to-end runs. Say when a change needs a rebuild, and wait.
- Don't create tags or releases, or merge release pull requests.

## Known mistakes

<!-- One line per mistake an agent has made here, phrased as the rule that prevents it. -->
