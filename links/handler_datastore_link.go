package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

func HandlerToDatastoreLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/handler", "celerity/datastore",
		"Allows a handler to read and write a datastore.",
		"Grants the handler read and write access to the linked celerity/datastore at runtime.",
	)
}
