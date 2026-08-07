package consumer

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Consumer.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/consumer",
		Label:            "Celerity Consumer",
		PlainTextSummary: "A subscriber to messages or events for a Celerity application.",
		FormattedSummary: "A subscriber to messages or events for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a subscriber to messages on a topic, " +
			"events in a data store or a bucket, or messages from a queue, an externally " +
			"defined queue or a message broker.",
		FormattedDescription: "The abstract resource type used to define a subscriber to messages on a " +
			"`celerity/topic`, events in a `celerity/datastore` or a `celerity/bucket`, or " +
			"messages from a `celerity/queue`, an externally defined queue or a message " +
			"broker.\n\n" +
			"See the " +
			"[`celerity/consumer` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-consumer) " +
			"for more information.",
		Schema:  consumerResourceSchema(),
		Resolve: resolveConsumer,
		// Contributory-only: the handler absorbs the consumer and emits the
		// concrete event-source trigger. The framework still requires an Emitters
		// entry wherever a declarative pipeline field such as Resolve is set, so
		// this one emits nothing.
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitConsumer),
		},
	}
}
