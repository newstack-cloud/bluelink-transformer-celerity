//go:build unit

package pipeline

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// The internal label the transformer uses to scope each per-protocol concrete
// API to handlers of its own protocol (resources/handler/handler_api_annotations.go).
const apiProtocolLabelKey = "celerity.internal.api.protocol"

// One WebSocket API + ws handler and one HTTP API + http handler with disjoint
// label selections: each concrete API must attach only its own protocol's
// handler in the staged link changes, and the WebSocket API Gateway spec must
// pass the real provider schema.
func TestPipelineWebSocketAPIScoping(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "protocol_scoped_apis.blueprint")

	assertWebSocketAPISpec(t, result)
	assertPerProtocolScopingLabels(t, result)
	assertPerProtocolLinkChanges(t, result)
}

func assertWebSocketAPISpec(t *testing.T, result *StageResult) {
	t.Helper()
	wsAPI := transformedResource(t, result.Transformed, "chatApi_websocket_api")
	require.Equal(t, "aws/apigatewayv2/api", wsAPI.Type.Value)
	require.Equal(t, "WEBSOCKET",
		core.StringValue(specNode(t, result.Transformed, "chatApi_websocket_api", "$.protocolType")))
	// Default abstract routeKey is "event".
	require.Equal(t, "$request.body.event",
		core.StringValue(specNode(
			t, result.Transformed, "chatApi_websocket_api", "$.routeSelectionExpression",
		)))
	// Reaching the change set proves the spec passed the real provider schema.
	require.Contains(t, result.Changes.NewResources, "chatApi_websocket_api")
}

// The scoping mechanism itself: each lambda is stamped with its protocol label
// and each concrete API's link selector requires that label.
func assertPerProtocolScopingLabels(t *testing.T, result *StageResult) {
	t.Helper()
	wsLambda := transformedResource(t, result.Transformed, "onMessage_lambda_func")
	require.Equal(t, "websocket", wsLambda.Metadata.Labels.Values[apiProtocolLabelKey])
	httpLambda := transformedResource(t, result.Transformed, "createOrder_lambda_func")
	require.Equal(t, "http", httpLambda.Metadata.Labels.Values[apiProtocolLabelKey])

	wsAPI := transformedResource(t, result.Transformed, "chatApi_websocket_api")
	require.Equal(t, "websocket", wsAPI.LinkSelector.ByLabel.Values[apiProtocolLabelKey])
	httpAPI := transformedResource(t, result.Transformed, "shopApi_http_api")
	require.Equal(t, "http", httpAPI.LinkSelector.ByLabel.Values[apiProtocolLabelKey])
}

func assertPerProtocolLinkChanges(t *testing.T, result *StageResult) {
	t.Helper()
	wsLinks := result.Changes.NewResources["chatApi_websocket_api"].NewOutboundLinks
	require.Contains(t, wsLinks, "onMessage_lambda_func",
		"the websocket API must link its own protocol's handler")
	require.NotContains(t, wsLinks, "createOrder_lambda_func",
		"the websocket API must not link the http handler")

	httpLinks := result.Changes.NewResources["shopApi_http_api"].NewOutboundLinks
	require.Contains(t, httpLinks, "createOrder_lambda_func",
		"the http API must link its own protocol's handler")
	require.NotContains(t, httpLinks, "onMessage_lambda_func",
		"the http API must not link the websocket handler")
}
