package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

func HandlerToTopicLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/handler", "celerity/topic",
		"Allows a handler to publish messages to a topic.",
		"Grants the handler permission to publish messages to the linked celerity/topic at runtime.",
	)
}
