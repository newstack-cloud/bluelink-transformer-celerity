//go:build unit

package pipeline

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/stretchr/testify/require"
)

// An HTTP celerity/api with two route handlers: the transformer plans the
// API Gateway v2 API + stage and the api -> lambda concrete links appear in the
// change set, while the per-route glue (integration, route, invoke permission)
// is left to the provider's aws/apigatewayv2/api::aws/lambda/function link and
// must NOT be emitted by the transformer.
func TestPipelineAPITwoHandlers(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "api_two_handlers.blueprint")

	api := result.Transformed.Resources.Values["ordersApi_http_api"]
	require.NotNil(t, api, "expected the concrete HTTP API in the transformed output")
	require.Equal(t, "aws/apigatewayv2/api", api.Type.Value)

	stage := result.Transformed.Resources.Values["ordersApi_http_stage"]
	require.NotNil(t, stage, "expected the concrete HTTP API stage in the transformed output")
	require.Equal(t, "aws/apigatewayv2/stage", stage.Type.Value)

	// Provider links own the route glue, so the transformer must not emit any
	// of these types itself.
	for _, ownedByProviderLinks := range []string{
		"aws/apigatewayv2/integration",
		"aws/apigatewayv2/route",
		"aws/lambda/permission",
	} {
		require.Empty(t, resourceNamesOfType(result.Transformed, ownedByProviderLinks),
			"the transformer must not emit %s resources; the provider api::function link owns them",
			ownedByProviderLinks)
	}

	requirePlanned(t, result, "ordersApi_http_api")
	requirePlanned(t, result, "ordersApi_http_stage")
	requirePlanned(t, result, "listOrders_lambda_func")
	requirePlanned(t, result, "createOrder_lambda_func")

	// The concrete api -> lambda link (one per route handler) must be staged.
	apiChanges := result.Changes.NewResources["ordersApi_http_api"]
	require.Contains(t, apiChanges.NewOutboundLinks, "listOrders_lambda_func",
		"expected a staged api -> lambda link for the GET route handler")
	require.Contains(t, apiChanges.NewOutboundLinks, "createOrder_lambda_func",
		"expected a staged api -> lambda link for the POST route handler")
}

// Returns the names of every resource of the given concrete
// type in the transformed blueprint.
func resourceNamesOfType(bp *schema.Blueprint, resourceType string) []string {
	names := []string{}
	if bp == nil || bp.Resources == nil {
		return names
	}
	for name, resource := range bp.Resources.Values {
		if resource != nil && resource.Type != nil && resource.Type.Value == resourceType {
			names = append(names, name)
		}
	}
	return names
}

// Asserts that the change set plans the named resource as a new
// resource.
func requirePlanned(t *testing.T, result *StageResult, resourceName string) {
	t.Helper()
	require.Contains(t, result.Changes.NewResources, resourceName,
		"expected %q in the planned new resources", resourceName)
}

// renderSubstitutions renders a substitution-bearing MappingNode back to its
// canonical ${...} string form; empty for nil or literal nodes.
func renderSubstitutions(node *core.MappingNode) string {
	if node == nil || node.StringWithSubstitutions == nil {
		return ""
	}
	rendered, err := substitutions.SubstitutionsToString("", node.StringWithSubstitutions)
	if err != nil {
		return ""
	}
	return rendered
}
