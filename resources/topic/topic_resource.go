package topic

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity Topic. Each deploy
// target's implementation is passed in by the registry rather than referenced
// here, so this package stays free of any one target.
func Resource(
	implementations ...shared.TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	return shared.ApplyTargetImplementations(&transformerv1.AbstractResourceDefinition{
		Type:             "celerity/topic",
		Label:            "Celerity Topic",
		PlainTextSummary: "A topic for publishing and subscribing to messages in a Celerity application.",
		FormattedSummary: "A topic for publishing and subscribing to messages in a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a topic that messages can be " +
			"published to and subscribed to. Topics are typically used in event-driven " +
			"architectures for asynchronous communication between applications and to " +
			"respond to infrastructure events.",
		FormattedDescription: "The abstract resource type used to define a topic that messages can be " +
			"published to and subscribed to.\n\n" +
			"Topics are typically used in event-driven architectures for asynchronous " +
			"communication between applications and to respond to infrastructure events, " +
			"with a `celerity/consumer` resource subscribing to the messages.\n\n" +
			"See the " +
			"[`celerity/topic` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-topic) " +
			"for more information.",
		Schema:  topicResourceSchema(),
		Resolve: resolveTopic,
	}, implementations)
}
