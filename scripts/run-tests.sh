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
bash scripts/run-tests.sh
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
elif [ "$TEST_MODE" == "all" ]; then
  TEST_TAGS="unit,integration"
fi

if [[ "$TEST_MODE" == "integration" || "$TEST_MODE" == "all" ]] && [ -f ".env.test" ]; then
  echo "Exporting environment variables from .env.test for integration tests ..."
  set -o allexport
  source .env.test
  set +o allexport
fi

go test -tags="$TEST_TAGS" -count=1 -timeout 90000ms -race -coverprofile=coverage.txt -coverpkg=./... -covermode=atomic `go list ./... | egrep -v '(/(testutils))$'`

if [ -z "$GITHUB_ACTION" ]; then
  # We are on a dev machine so produce html output of coverage
  # to get a visual to better reveal uncovered lines.
  go tool cover -html=coverage.txt -o coverage.html
fi

if [ -n "$GITHUB_ACTION" ]; then
  # We are in a CI environment so run tests again to generate JSON report.
  go test -count=1 -timeout 90000ms -json -tags "$TEST_TAGS" `go list ./... | egrep -v '(/(testutils))$'` > report.json
fi
