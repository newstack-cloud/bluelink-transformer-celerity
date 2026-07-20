//go:build unit

package pipeline

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// A queue forwarding to a topic provisions the intermediary forwarder lambda
// (inline code.zipFile) and its execution role. Staging the transformed
// blueprint proves the inline zipFile form is legal against the real generated
// aws/lambda/function schema, not just against the transformer's own emit.
func TestPipelineQueueTopicForwarder(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "queue_topic_forwarder.blueprint")

	forwarder := result.Transformed.Resources.Values["inboundQueue_topic_forwarder"]
	require.NotNil(t, forwarder, "expected the intermediary forwarder function in the transformed output")
	require.Equal(t, "aws/lambda/function", forwarder.Type.Value)

	code := forwarder.Spec.Fields["code"]
	require.NotNil(t, code, "expected the forwarder to carry a code object")
	require.NotEmpty(t, core.StringValue(code.Fields["zipFile"]),
		"expected the forwarder's code to be inline (code.zipFile)")

	role := result.Transformed.Resources.Values["inboundQueue_topic_forwarder_role"]
	require.NotNil(t, role, "expected the forwarder's execution role in the transformed output")
	require.Equal(t, "aws/iam/role", role.Type.Value)

	// Surviving Stage means the inline zipFile passed concrete validation
	// against the real provider schema and was planned.
	requirePlanned(t, result, "inboundQueue_topic_forwarder")
	requirePlanned(t, result, "inboundQueue_topic_forwarder_role")
	requirePlanned(t, result, "inboundQueue_sqs_queue")
	requirePlanned(t, result, "eventsTopic_sns_topic")
}
