package schedule

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Deliberately a no-op.
//
// A celerity/schedule is contributory-only: the handler it triggers absorbs it
// and emits the aws/events/rule, whose targets[].arn reference activates the
// provider's aws/events/rule::aws/lambda/function link (which in turn creates the
// invoke permission), so the schedule produces no concrete resources of its own
// and the aggregator drops it from the primaries. The emitter exists only because
// the framework requires an Emitters entry wherever a pipeline field such as
// Resolve is set.
func emitSchedule(
	_ context.Context,
	_ *transformutils.Run,
	_ *ResolvedSchedule,
	_ transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	return &transformutils.EmitResult{}, nil
}
