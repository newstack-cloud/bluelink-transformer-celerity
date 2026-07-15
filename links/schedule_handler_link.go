package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

const scheduleHandlerAnnotationDefKey = "celerity/handler::" + handler.AnnotationKeyScheduleHandler

func ScheduleToHandlerLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/schedule",
		ResourceTypeB:    "celerity/handler",
		PlainTextSummary: "Triggers a handler on a schedule.",
		FormattedSummary: "Triggers a `celerity/handler` on a `celerity/schedule`.",
		PlainTextDescription: "Invokes the handler on the cadence defined by the schedule, such as a " +
			"cron or rate expression.",
		FormattedDescription: "Invokes the handler on the cadence defined by the `celerity/schedule`, such " +
			"as a cron or rate expression.",
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			scheduleHandlerAnnotationDefKey: {
				Name:      handler.AnnotationKeyScheduleHandler,
				Label:     "Schedule handler",
				Type:      core.ScalarTypeBool,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Marks the handler as a scheduled handler so it is invoked on the linked " +
					"schedule's cadence.",
			},
		},
	}
}
