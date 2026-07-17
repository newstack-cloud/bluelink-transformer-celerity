package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// QueueToConsumerLink declares the celerity/queue -> celerity/consumer
// relationship: the consumer processes messages polled from the queue. On
// aws-serverless the consumer is absorbed into its handler, whose Lambda is wired
// to the queue by an SQS event source mapping.
func QueueToConsumerLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/queue",
		ResourceTypeB:    "celerity/consumer",
		PlainTextSummary: "Delivers messages from a queue to a consumer.",
		FormattedSummary: "Delivers messages from a `celerity/queue` to a `celerity/consumer`.",
		PlainTextDescription: "Configures the consumer to process messages from the queue. On aws-serverless " +
			"the consumer's handler is wired to the queue by an SQS event source mapping.",
		FormattedDescription: "Configures the `celerity/consumer` to process messages from the " +
			"`celerity/queue`. On aws-serverless the consumer's handler is wired to the queue by an SQS " +
			"event source mapping.",
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			"celerity/consumer::" + handler.AnnotationKeyConsumerSourceQueue: {
				Name:      handler.AnnotationKeyConsumerSourceQueue,
				Label:     "Consumer queue",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Names the in-blueprint queue the consumer listens to. Only required to " +
					"disambiguate when the consumer matches several queues by link selector; with a single " +
					"linked queue the consumer listens to it by default.",
			},
		},
	}
}
