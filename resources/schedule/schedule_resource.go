package schedule

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Schedule.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/schedule",
		Label:            "Celerity Schedule",
		PlainTextSummary: "A scheduled trigger for handlers in a Celerity application.",
		FormattedSummary: "A scheduled trigger for handlers in a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a scheduled rule that triggers " +
			"handlers at a specific time or interval.",
		FormattedDescription: "The abstract resource type used to define a scheduled rule that triggers " +
			"`celerity/handler` resources at a specific time or interval.\n\n" +
			"See the " +
			"[`celerity/schedule` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-schedule) " +
			"for more information.",
		Schema:  scheduleResourceSchema(),
		Resolve: resolveSchedule,
		// Contributory-only: the handler absorbs the schedule and emits the
		// aws/events/rule. The framework still requires an Emitters entry
		// wherever a declarative pipeline field such as Resolve is set, so this
		// one emits nothing.
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitSchedule),
		},
	}
}
