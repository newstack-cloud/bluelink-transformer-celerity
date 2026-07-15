package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

func HandlerToBucketLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/handler", "celerity/bucket",
		"Allows a handler to read and write objects in a bucket.",
		"Grants the handler read and write access to objects in the linked celerity/bucket at runtime.",
	)
}
