package consumer

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Deliberately a no-op.
//
// A celerity/consumer is contributory-only: the handler it links to absorbs it
// and emits the concrete event-source wiring (an SQS/DynamoDB/Kinesis event source
// mapping, an S3 notification or an SNS subscription) for the handler's Lambda
// function, so the consumer produces no concrete resources of its own and the
// aggregator drops it from the primaries. The emitter exists only because the
// framework requires an Emitters entry wherever a pipeline field such as Resolve
// is set.
func emitConsumer(
	_ context.Context,
	_ *transformutils.Run,
	_ *ResolvedConsumer,
	_ transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	return &transformutils.EmitResult{}, nil
}
