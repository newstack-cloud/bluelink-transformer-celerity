package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

// QueueToTopicLink declares the celerity/queue -> celerity/topic relationship:
// messages received by the queue are forwarded to the topic. On aws-serverless an
// intermediary forwarding function is provisioned to publish the queue's messages
// to the topic.
func QueueToTopicLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/queue", "celerity/topic",
		"Forwards messages from a queue to a topic.",
		"Forwards messages received by a `celerity/queue` to a `celerity/topic`. On aws-serverless an "+
			"intermediary forwarding function is provisioned to publish the queue's messages to the topic.",
	)
}
