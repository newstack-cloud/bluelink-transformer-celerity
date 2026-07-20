//go:build unit

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A producer handler -> queue -> consumer -> worker handler chain: for an
// in-blueprint queue the provider's aws/sqs/queue::aws/lambda/function link owns
// the event source mapping,
// so the transformer must emit NO aws/lambda/eventSourceMapping. The concrete
// queue and both lambdas are planned, the queue -> function link is staged, and
// the producer's backing link makes the internal resources config store appear.
func TestPipelineQueueConsumer(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "queue_consumer.blueprint")

	require.Empty(t, resourceNamesOfType(result.Transformed, "aws/lambda/eventSourceMapping"),
		"in-blueprint queue consumers must not emit a standalone event source mapping; "+
			"the provider queue -> function link owns the ESM")

	queue := result.Transformed.Resources.Values["ordersQueue_sqs_queue"]
	require.NotNil(t, queue, "expected the concrete SQS queue in the transformed output")
	require.Equal(t, "aws/sqs/queue", queue.Type.Value)

	requirePlanned(t, result, "ordersQueue_sqs_queue")
	requirePlanned(t, result, "submitOrder_lambda_func")
	requirePlanned(t, result, "processOrder_lambda_func")

	// The absorbed consumer wires the queue to the worker's lambda purely through
	// labels; the concrete queue -> function link must be staged.
	queueChanges := result.Changes.NewResources["ordersQueue_sqs_queue"]
	require.Contains(t, queueChanges.NewOutboundLinks, "processOrder_lambda_func",
		"expected a staged queue -> function link for the absorbed consumer")

	// submitOrder links a store-backed queue, so the internal resources config
	// store (an aws/ssm/parameterTree keyed by the queue's configKey) is planned.
	store := result.Transformed.Resources.Values["celerityResourcesConfigStore"]
	require.NotNil(t, store, "expected the internal resources config store in the transformed output")
	require.Equal(t, "aws/ssm/parameterTree", store.Type.Value)
	requirePlanned(t, result, "celerityResourcesConfigStore")

	// submitOrder also links a nameless bucket: the store entry references the
	// bucket's bucketName, which the provider marks computed-when-omitted, so
	// concrete validation and staging must accept the omitted name and resolve
	// the reference as known-after-deploy. (Guards the provider's
	// computed-when-omitted support; before it, a nameless handler-linked
	// bucket could not stage at all.)
	requirePlanned(t, result, "orderUploads_s3_bucket")
	storeValues := store.Spec.Fields["values"]
	require.NotNil(t, storeValues, "expected store values on the resources config store")
	require.Contains(t, renderSubstitutions(storeValues.Fields["orderUploads"]),
		"orderUploads_s3_bucket",
		"expected the name-less bucket's store entry to reference the concrete bucket")
}
