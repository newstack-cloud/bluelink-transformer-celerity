//go:build unit

package pipeline

import (
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// A handler inheriting runtime/memory/timeout/env from a linked
// celerity/handlerConfig, plus an api::config association. Inherited values
// must land in the emitted lambda spec, the handlerConfig must be stripped
// from the transformed output, and the api::config link must stay a no-op on
// aws-serverless while the config still emits its own store.
func TestPipelineHandlerConfigInheritance(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "handler_inheritance.blueprint")

	assertInheritedLambdaSpec(t, result)
	assertHandlerConfigStripped(t, result)
	assertAPIConfigNoOp(t, result)
}

// The handler sets no runtime/memory/timeout/env of its own,
// so every value must come from the
// linked handlerConfig, beating the schema defaults (memory 512, timeout 30).
func assertInheritedLambdaSpec(t *testing.T, result *StageResult) {
	t.Helper()
	transformed := result.Transformed
	require.Equal(t, 1024,
		core.IntValue(specNode(t, transformed, "reportStatus_lambda_func", "$.memorySize")))
	require.Equal(t, 60,
		core.IntValue(specNode(t, transformed, "reportStatus_lambda_func", "$.timeout")))
	require.Equal(t, "nodejs24.x",
		core.StringValue(specNode(t, transformed, "reportStatus_lambda_func", "$.runtime")))
	require.Equal(t, "debug",
		core.StringValue(specNode(
			t, transformed, "reportStatus_lambda_func", "$.environment.variables.LOG_LEVEL",
		)))
}

// handlerConfig is contributor-only: it must emit no concrete resource, and no
// abstract celerity/* resource may survive the transform at all.
func assertHandlerConfigStripped(t *testing.T, result *StageResult) {
	t.Helper()
	require.NotContains(t, result.Transformed.Resources.Values, "sharedDefaults",
		"the handlerConfig contributor must be stripped from the transformed output")
	for typeName, names := range transformedResourceTypes(result.Transformed) {
		require.False(t, strings.HasPrefix(typeName, "celerity/"),
			"abstract type %q must not survive the transform (resources: %v)", typeName, names)
	}
}

// On aws-serverless api::config is an explicit no-op (links/api_config_link.go):
// the config still emits its own store (a secretsmanager secret here, since the
// fixture has no plaintext keys), but nothing is wired between the concrete API
// and the store.
func assertAPIConfigNoOp(t *testing.T, result *StageResult) {
	t.Helper()
	secret := transformedResource(t, result.Transformed, "appSettings_config_secret")
	require.Equal(t, "aws/secretsmanager/secret", secret.Type.Value)
	require.Contains(t, result.Changes.NewResources, "appSettings_config_secret")

	apiChanges, ok := result.Changes.NewResources["appApi_http_api"]
	require.True(t, ok, "expected the concrete http API in the change set")
	require.NotContains(t, apiChanges.NewOutboundLinks, "appSettings_config_secret",
		"api::config must not produce concrete wiring on aws-serverless")
}
