package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// QueueToQueueLink declares the celerity/queue -> celerity/queue relationship: the
// parent queue (defining the link selector) uses the linked-to queue as a
// dead-letter queue for messages that cannot be processed after the maximum number
// of attempts. On aws-serverless this maps to a redrive policy on the parent queue.
func QueueToQueueLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/queue",
		ResourceTypeB:    "celerity/queue",
		PlainTextSummary: "Uses a queue as another queue's dead-letter queue.",
		FormattedSummary: "Uses a `celerity/queue` as another `celerity/queue`'s dead-letter queue.",
		PlainTextDescription: "The parent queue uses the linked-to queue as a dead-letter queue for " +
			"messages that cannot be processed after the maximum number of attempts. On aws-serverless " +
			"this maps to a redrive policy on the parent queue.",
		FormattedDescription: "The parent `celerity/queue` uses the linked-to `celerity/queue` as a " +
			"dead-letter queue for messages that cannot be processed after the maximum number of " +
			"attempts. On aws-serverless this maps to a redrive policy on the parent queue.",
		// A queue uses at most one dead-letter queue.
		CardinalityB: provider.LinkCardinality{Min: 0, Max: 1},
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			"celerity/queue::" + queue.AnnotationKeyDeadLetterMaxAttempts: {
				Name:      queue.AnnotationKeyDeadLetterMaxAttempts,
				Label:     "Dead-letter max attempts",
				Type:      core.ScalarTypeInteger,
				AppliesTo: provider.LinkAnnotationResourceA,
				Examples: []*core.ScalarValue{
					core.ScalarFromInt(3),
					core.ScalarFromInt(10),
				},
				Description: "The maximum number of attempts to process a message on the parent queue " +
					"before it is sent to the dead-letter queue. Optional; the target environment's default " +
					"is used when unset.",
			},
		},
	}
}
