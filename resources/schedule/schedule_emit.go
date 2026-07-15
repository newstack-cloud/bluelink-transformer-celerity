package schedule

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// emitSchedule is deliberately a no-op.
//
// A celerity/schedule is a contributory-only resource: the handler it triggers
// absorbs it and emits the aws/events/rule, whose targets[].arn reference
// activates the provider's aws/events/rule::aws/lambda/function link (which in
// turn creates the invoke permission). The schedule therefore produces no
// concrete resources of its own, and the aggregator filters it out of the
// primaries entirely.
//
// The emitter exists only because the framework requires at least one Emitters
// entry wherever a declarative pipeline field such as Resolve is set.
func emitSchedule(
	_ context.Context,
	_ *transformutils.Run,
	_ *ResolvedSchedule,
	_ transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	return &transformutils.EmitResult{}, nil
}
