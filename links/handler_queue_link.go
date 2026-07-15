package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

func HandlerToQueueLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/handler", "celerity/queue",
		"Allows a handler to send messages to a queue.",
		"Grants the handler permission to enqueue messages onto the linked celerity/queue at runtime.",
	)
}
