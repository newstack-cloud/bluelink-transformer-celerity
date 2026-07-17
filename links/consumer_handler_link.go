package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

const (
	consumerHandlerAnnotationDefKey = "celerity/handler::" + handler.AnnotationKeyConsumerHandler
	consumerRouteAnnotationDefKey   = "celerity/handler::" + handler.AnnotationKeyConsumerRoute
)

func ConsumerToHandlerLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/consumer",
		ResourceTypeB:    "celerity/handler",
		PlainTextSummary: "Delivers events from a consumer to a handler.",
		FormattedSummary: "Delivers events from a `celerity/consumer` to a `celerity/handler`.",
		PlainTextDescription: "Connects an event consumer to the handler that processes its messages. " +
			"Events from the consumer's source, for example a queue or datastore stream, are delivered to " +
			"the handler; the celerity.handler.consumer.route annotation tags the handler for routing when " +
			"several share one function.",
		FormattedDescription: "Connects an event consumer to the handler that processes its messages. " +
			"Events from the consumer's source, for example a `celerity/queue` or `celerity/datastore` " +
			"stream, are delivered to the handler; the `celerity.handler.consumer.route` annotation tags " +
			"the handler for routing when several share one function.",
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			consumerHandlerAnnotationDefKey: {
				Name:      handler.AnnotationKeyConsumerHandler,
				Label:     "Consumer handler",
				Type:      core.ScalarTypeBool,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Marks the handler as an event consumer so messages from the linked " +
					"consumer's source are delivered to it.",
			},
			consumerRouteAnnotationDefKey: {
				Name:      handler.AnnotationKeyConsumerRoute,
				Label:     "Consumer route",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Secondary routing key used to dispatch events to this handler when several " +
					"consumer handlers share one function.",
			},
		},
		ValidateFunc: validateHandlerTargetSingleEventSource,
	}
}
