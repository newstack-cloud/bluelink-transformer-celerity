package topic

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Topic.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
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
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitTopic),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedTopic) string {
					return topicConcreteName(r.Name)
				},
			),
		},
	}
}
