package queue

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Queue.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/queue",
		Label:            "Celerity Queue",
		PlainTextSummary: "A queue for asynchronous message processing in a Celerity application.",
		FormattedSummary: "A queue for asynchronous message processing in a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a queue for asynchronous processing " +
			"of messages. A queue is typically paired with a consumer that processes " +
			"messages from it, while handlers write messages to it.",
		FormattedDescription: "The abstract resource type used to define a queue for asynchronous processing " +
			"of messages.\n\n" +
			"A queue is typically paired with a `celerity/consumer` resource that processes " +
			"messages from it, while handlers write messages to it.\n\n" +
			"See the " +
			"[`celerity/queue` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-queue) " +
			"for more information.",
		Schema:  queueResourceSchema(),
		Resolve: resolveQueue,
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitQueue),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedQueue) string {
					return queueConcreteName(r.Name)
				},
			),
		},
	}
}
