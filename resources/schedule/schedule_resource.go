package schedule

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Schedule.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:    "celerity/schedule",
		Label:   "Celerity Schedule",
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
