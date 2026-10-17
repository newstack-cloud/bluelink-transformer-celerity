package config

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity Config.
func Resource(
	implementations ...shared.TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	return shared.ApplyTargetImplementations(&transformerv1.AbstractResourceDefinition{
		Type:             "celerity/config",
		Label:            "Celerity Config",
		PlainTextSummary: "A secrets and configuration store for a Celerity application.",
		FormattedSummary: "A secrets and configuration store for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a secrets and configuration store. " +
			"It holds configuration and sensitive information such as API keys, database " +
			"passwords and other credentials used by an application.",
		FormattedDescription: "The abstract resource type used to define a secrets and configuration store.\n\n" +
			"It holds configuration and sensitive information such as API keys, database " +
			"passwords and other credentials used by an application.\n\n" +
			"See the " +
			"[`celerity/config` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-config) " +
			"for more information.",
		Schema:  configResourceSchema(),
		Resolve: resolveConfig,
	}, implementations)
}
