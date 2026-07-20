//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

const (
	// API Gateway endpoint DNS + route/permission propagation can take a
	// couple of minutes after deploy before the first 200.
	httpInvokeTimeout = 3 * time.Minute

	secretsManagerARNPrefix = "arn:aws:secretsmanager:"
)

// Safely reads a string field from a resource's deployed SpecData.
func stringField(node *core.MappingNode, field string) string {
	if node == nil || node.Fields == nil {
		return ""
	}
	return core.StringValue(node.Fields[field])
}

// TestDeployCoreServerlessApp deploys the core fixture (an HTTP API handler
// with a plaintext config store and an all-secret config store, plus a
// scheduled handler) through the full transform + deploy pipeline against real
// AWS, then asserts observable behaviour: the deployed HTTP API serves the
// fixture's GET /orders route with a 200 from the stub handler, the all-secret
// store is wired to the handler as a Secrets Manager-backed config store, and
// every deployed function's env vars are fully resolved. Destroy runs via the
// harness t.Cleanup.
//
// The fixture uses two handlers (one HTTP, one scheduled) rather than one
// because a handler may carry only a single event-source annotation
// (links/handler_event_source_validation.go rejects http + schedule on the
// same handler).
func TestDeployCoreServerlessApp(t *testing.T) {
	t.Parallel()
	h := Setup(t)
	manifestPath := h.PrestageArtifacts(t)

	inst := h.Deploy(t, "core.blueprint", manifestPath, nil)

	// Concrete resource names derive from the fixture's logical names
	// (<logicalName>_lambda_func); the deploy already asserted the finished
	// status was Deployed, so here we check computed outputs landed in state.
	apiHandlerARN := stringField(inst.ResourceSpec(t, "apiHandler_lambda_func"), "arn")
	require.NotEmpty(t, apiHandlerARN, "expected the HTTP handler's lambda ARN in state")

	syncHandlerARN := stringField(inst.ResourceSpec(t, "syncHandler_lambda_func"), "arn")
	require.NotEmpty(t, syncHandlerARN, "expected the scheduled handler's lambda ARN in state")

	// spec.baseUrl on the abstract api is rewritten by the transformer's
	// property map to the concrete aws/apigatewayv2/api apiEndpoint.
	baseURL := core.StringValue(inst.Export(t, "apiBaseUrl"))
	require.NotEmpty(t, baseURL, "expected the API base URL export from the deployed HTTP API")

	apiFunctionName := stringField(inst.ResourceSpec(t, "apiHandler_lambda_func"), "functionName")
	require.NotEmpty(t, apiFunctionName, "expected the HTTP handler's function name in state")
	syncFunctionName := stringField(inst.ResourceSpec(t, "syncHandler_lambda_func"), "functionName")
	require.NotEmpty(t, syncFunctionName, "expected the scheduled handler's function name in state")

	assertHTTPRouteServes200(t, strings.TrimSuffix(baseURL, "/")+"/orders")
	assertSecretConfigStoreWired(t, h, apiFunctionName)
	assertAllFunctionEnvVarsResolved(t, h, apiFunctionName, syncFunctionName)
}

// Polls the deployed API route until the stub handler
// answers 200. The stub returns {statusCode: 200} which API Gateway's payload
// format v2 maps to a plain 200 response; anything else (403/404 during
// route/permission propagation, transient network errors) is a retry.
func assertHTTPRouteServes200(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	lastStatus := ""
	waitFor(t, httpInvokeTimeout, 5*time.Second,
		fmt.Sprintf("HTTP 200 from deployed route %s", url),
		func() (bool, error) {
			resp, err := client.Get(url)
			if err != nil {
				return false, nil
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.Status != lastStatus {
				lastStatus = resp.Status
				t.Logf("GET %s -> %s", url, resp.Status)
			}
			return resp.StatusCode == http.StatusOK, nil
		})
}

// Asserts the all-secret celerity/config store
// (fixture name "appsecrets", no plaintext keys) reached the linked handler as
// a Secrets Manager-backed store: the CELERITY_CONFIG_APPSECRETS_STORE_ID env
// var carries a secret ARN and the _STORE_KIND names the secrets-manager
// backend. The plaintext "appconfig" store must stay parameter-store-backed on
// the same function.
func assertSecretConfigStoreWired(t *testing.T, h *Harness, functionName string) {
	t.Helper()
	client := lambda.NewFromConfig(h.AWSConfig)
	waitFor(t, wiringAssertTimeout, 5*time.Second,
		fmt.Sprintf("secrets-manager config store env vars on function %s", functionName),
		func() (bool, error) {
			vars, err := functionEnvVars(h, client, functionName)
			if err != nil {
				return false, err
			}
			secretsWired := strings.HasPrefix(
				vars["CELERITY_CONFIG_APPSECRETS_STORE_ID"], secretsManagerARNPrefix,
			) && vars["CELERITY_CONFIG_APPSECRETS_STORE_KIND"] == "secrets-manager"
			plaintextWired := vars["CELERITY_CONFIG_APPCONFIG_STORE_ID"] != "" &&
				vars["CELERITY_CONFIG_APPCONFIG_STORE_KIND"] == "parameter-store"
			return secretsWired && plaintextWired, nil
		})
}
