package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

func HandlerToConfigLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/handler", "celerity/config",
		"Grants a handler access to a configuration store.",
		"Allows the handler to read configuration values and secrets from the linked celerity/config store at runtime.",
	)
}
