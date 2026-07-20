#!/usr/bin/env bash


# Default to running all tests
TEST_MODE="all"

POSITIONAL=()
while [[ $# -gt 0 ]]
do
key="$1"

case $key in
    --unit)
    TEST_MODE="unit"
    shift # past argument
    ;;
    --integration)
    TEST_MODE="integration"
    shift # past argument
    ;;
    --e2e)
    TEST_MODE="e2e"
    shift # past argument
    ;;
    --all)
    TEST_MODE="all"
    shift # past argument
    ;;
    -h|--help)
    HELP=yes
    shift # past argument
    ;;
    *)    # unknown option
    POSITIONAL+=("$1") # save it in an array for later
    shift # past argument
    ;;
esac
done
set -- "${POSITIONAL[@]}" # restore positional parameters

function help {
  cat << EOF
Test runner
Runs tests for the Celerity transformer:
bash scripts/run-tests.sh [--unit|--integration|--e2e|--all]

  --unit         unit + in-process container pipeline tests (no external deps)
  --integration  local-emulator (LocalStack) tests
  --e2e          real-cloud deploy tests (requires AWS credentials + AWS_REGION)
  --all          unit + integration (e2e is always explicit)
EOF
}

if [ -n "$HELP" ]; then
  help
  exit 0
fi

set -e
echo "" > coverage.txt

if [ "$TEST_MODE" == "unit" ]; then
  TEST_TAGS="unit"
elif [ "$TEST_MODE" == "integration" ]; then
  TEST_TAGS="integration"
elif [ "$TEST_MODE" == "e2e" ]; then
  # Real-cloud end-to-end deploys (tests/e2e). Requires AWS credentials and
  # AWS_REGION; deliberately NOT part of --all so local/CI runs never need
  # cloud access by surprise.
  TEST_TAGS="e2e"
elif [ "$TEST_MODE" == "all" ]; then
  TEST_TAGS="unit,integration"
fi

if [[ "$TEST_MODE" == "e2e" ]] && [ -f ".env.test" ]; then
  echo "Exporting environment variables from .env.test for e2e tests ..."
  set -o allexport
  source .env.test
  set +o allexport
fi

if [ "$TEST_MODE" == "e2e" ]; then
  # The e2e suite skips every test when AWS_REGION is unset, and a fully
  # skipped package still prints "ok" — which reads as a real pass. Guard
  # against that here: an explicit --e2e invocation with no region is an
  # error, not a silent no-op.
  if [ -z "$AWS_REGION" ]; then
    echo "ERROR: --e2e requires AWS_REGION (and AWS credentials, e.g. AWS_PROFILE)." >&2
    echo "Set them in .env.test (see .env.test.example) or the environment." >&2
    echo "Without AWS_REGION every e2e test skips and the run falsely looks green." >&2
    exit 1
  fi
  # E2E deploys real AWS resources; longer timeout, no local docker deps.
  # -v so per-test progress (and any SKIP) is visible — real deploys take
  # minutes, and a sub-second "ok" should never pass unnoticed.
  go test -tags="e2e" -count=1 -timeout 60m -race -v \
    -parallel "${E2E_CONCURRENCY:-6}" ./tests/e2e/...
  exit $?
fi

if [[ "$TEST_MODE" == "integration" || "$TEST_MODE" == "all" ]] && [ -f ".env.test" ]; then
  echo "Exporting environment variables from .env.test for integration tests ..."
  set -o allexport
  source .env.test
  set +o allexport
fi

if [[ "$TEST_MODE" == "integration" || "$TEST_MODE" == "all" ]]; then
  finish() {
    echo "Taking down test dependencies docker compose stack ..."
    docker compose -f docker-compose.test-deps.yml down
  }
  trap finish EXIT

  echo "Bringing up docker compose stack for test dependencies ..."
  docker compose -f docker-compose.test-deps.yml up -d

  echo "Waiting for LocalStack to be ready ..."
  start=$EPOCHSECONDS
  completed="false"
  while [ "$completed" != "true" ]; do
    sleep 5
    completed=$(curl -s localhost:4579/_localstack/init/ready | jq .completed)
    if (( EPOCHSECONDS - start > 60 )); then break; fi
  done

  echo "Populating S3 with test data ..."
  awslocal --endpoint-url=http://localhost:4579 s3 mb s3://test-bucket --region eu-west-2
  awslocal --endpoint-url=http://localhost:4579 s3api put-object --bucket test-bucket \
    --body shared/build/__testdata/s3/data/test-bucket/valid.manifest.json \
    --key valid.manifest.json --region eu-west-2
  awslocal --endpoint-url=http://localhost:4579 s3api put-object --bucket test-bucket \
    --body shared/build/__testdata/s3/data/test-bucket/invalid.manifest.json \
    --key invalid.manifest.json --region eu-west-2

  # LocalStack accepts any credentials, but the AWS SDK's default credential
  # chain still needs *something* present. Setting these here keeps the test
  # code itself free of credential plumbing.
  export AWS_ACCESS_KEY_ID=test
  export AWS_SECRET_ACCESS_KEY=test
fi

# go list must see the same build tags as go test: packages whose files are
# all build-tag-gated (e.g. tests/pipeline) are silently dropped from an
# untagged listing and would never run.
go test -tags="$TEST_TAGS" -count=1 -timeout 90000ms -race -coverprofile=coverage.txt -coverpkg=./... -covermode=atomic `go list -tags="$TEST_TAGS" ./... | egrep -v '(/(testutils))$'`

if [ -z "$GITHUB_ACTION" ]; then
  # We are on a dev machine so produce html output of coverage
  # to get a visual to better reveal uncovered lines.
  go tool cover -html=coverage.txt -o coverage.html
fi

if [ -n "$GITHUB_ACTION" ]; then
  # We are in a CI environment so run tests again to generate JSON report.
  go test -count=1 -timeout 90000ms -json -tags "$TEST_TAGS" `go list -tags="$TEST_TAGS" ./... | egrep -v '(/(testutils))$'` > report.json
fi
