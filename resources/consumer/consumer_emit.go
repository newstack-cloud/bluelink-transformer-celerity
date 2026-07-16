package consumer

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// emitConsumer is deliberately a no-op.
//
// A celerity/consumer is a contributory-only resource: the handler it links to
// absorbs it and emits the concrete event-source wiring (an SQS/DynamoDB/Kinesis
// event source mapping, an S3 notification or an SNS subscription) for the
// handler's Lambda function. The consumer therefore produces no concrete resources
// of its own, and the aggregator filters it out of the primaries entirely.
//
// The emitter exists only because the framework requires at least one Emitters
// entry wherever a declarative pipeline field such as Resolve is set.
func emitConsumer(
	_ context.Context,
	_ *transformutils.Run,
	_ *ResolvedConsumer,
	_ transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	return &transformutils.EmitResult{}, nil
}
