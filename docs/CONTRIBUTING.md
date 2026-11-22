# Contributing to Bluelink Transformer for Celerity

## Setup

The repo has no git hooks: run `golangci-lint run --build-tags=unit,integration,e2e ./...` before committing, and, after
`yarn install`, `yarn commitlint --from origin/main` to check your commit messages.

### Prerequisites

- [Go](https://golang.org/dl/) >=1.26
- [GolangCI-Lint](https://golangci-lint.run/welcome/install/#local-installation) - Used for linting and formatting.
- [Node.js](https://nodejs.org/en/download/) - Used for running scripts for commit message linting.
- [Yarn](https://yarnpkg.com/getting-started/install) - Used for managing dependencies for commit message linting.

Dependencies are managed with Go modules (go.mod) and will be installed automatically when you first run tests.

If you want to install dependencies manually you can run:

```bash
go mod download
```

### Node dependencies

There are node.js dependencies that provide commitlint, for checking commit messages.

Install dependencies from the root directory by simply running:
```bash
yarn
```

## Running Tests

This project uses Go build tags to separate test tiers. Test files include a build tag at the
top of the file:

- `//go:build unit` for unit tests
- `//go:build integration` for integration tests, against LocalStack (needs docker, the AWS CLI
  with `awslocal`, `jq` and `curl`)
- `//go:build e2e` for end-to-end tests, which deploy to a real AWS account

Test caching is disabled (`-count=1`) for all test modes since this project is heavily
integration-focused and cached test results can mask real issues.

### Unit Tests

Run all unit tests:

```bash
go test -tags=unit -count=1 ./...
```

### Test Runner Script

A convenience script is provided that handles environment setup, coverage, and reporting:

```bash
# Run all tests (unit + integration)
bash scripts/run-tests.sh

# Run only unit tests
bash scripts/run-tests.sh --unit

# Run only integration tests
bash scripts/run-tests.sh --integration
```

The script will:
- Source `.env.test` automatically when running integration, all or e2e tests
- Except with `--e2e`:
  - Generate a `coverage.txt` coverage profile
  - Generate `coverage.html` for local visual coverage inspection
  - In CI, generate a `report.json` for test reporting

`--e2e` empties `coverage.txt` and writes neither of the others, so a `coverage.html` left from
an earlier run is stale.

### Linting

Run the linter:

```bash
golangci-lint run --build-tags=unit,integration,e2e ./...
```

Without `--build-tags`, the linter skips every tagged file, which is nearly all tests.

## Further documentation

- [Commit Guidelines](./COMMIT_GUIDELINES.md)
- [Source Control and Release Strategy](./SOURCE_CONTROL_RELEASE_STRATEGY.md)
