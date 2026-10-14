//go:build unit

package pipeline

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// The internal label the transformer uses to attach each per-protocol concrete
// API to its own handlers (resources/handler/handler_api_annotations.go). Its
// value pairs the abstract API name with the protocol, because a link selector
// matches a resource carrying any one of the selector's labels, so the pairing
// has to live in a single label to be exact.
const apiTargetLabelKey = "celerity.internal.api.target"

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
	assertHybridAPILinkChanges(t, result)
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
	require.Equal(t, "chatApi::websocket", wsLambda.Metadata.Labels.Values[apiTargetLabelKey])
	httpLambda := transformedResource(t, result.Transformed, "createOrder_lambda_func")
	require.Equal(t, "shopApi::http", httpLambda.Metadata.Labels.Values[apiTargetLabelKey])

	// The concrete API selects on the target label alone. Carrying the author's
	// labels over as well would match the other protocol's handlers again.
	wsAPI := transformedResource(t, result.Transformed, "chatApi_websocket_api")
	require.Equal(t,
		map[string]string{apiTargetLabelKey: "chatApi::websocket"},
		wsAPI.LinkSelector.ByLabel.Values,
	)
	httpAPI := transformedResource(t, result.Transformed, "shopApi_http_api")
	require.Equal(t,
		map[string]string{apiTargetLabelKey: "shopApi::http"},
		httpAPI.LinkSelector.ByLabel.Values,
	)
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

// One abstract API declaring both protocols, whose handlers all carry the same
// author label. This is the case the two APIs above cannot cover: their label
// selections are disjoint, so they attach only their own handler whether the
// protocol scoping works or not.
//
// A hybrid API is what an HTTP API attaching a "$disconnect" route to itself
// looks like in a blueprint, and API Gateway rejects that route key outright.
func assertHybridAPILinkChanges(t *testing.T, result *StageResult) {
	t.Helper()

	httpLinks := result.Changes.NewResources["notificationsApi_http_api"].NewOutboundLinks
	require.Contains(t, httpLinks, "notificationsStatus_lambda_func",
		"the http API of a hybrid API must link the http handler")
	require.NotContains(t, httpLinks, "notificationsDisconnect_lambda_func",
		"the http API of a hybrid API must not link the websocket handler")

	wsLinks := result.Changes.NewResources["notificationsApi_websocket_api"].NewOutboundLinks
	require.Contains(t, wsLinks, "notificationsDisconnect_lambda_func",
		"the websocket API of a hybrid API must link the websocket handler")
	require.NotContains(t, wsLinks, "notificationsStatus_lambda_func",
		"the websocket API of a hybrid API must not link the http handler")
}
