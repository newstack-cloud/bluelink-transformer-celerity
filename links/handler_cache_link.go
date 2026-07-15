package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

func HandlerToCacheLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/handler", "celerity/cache",
		"Allows a handler to read and write a cache.",
		"Grants the handler read and write access to the linked celerity/cache at runtime.",
	)
}
