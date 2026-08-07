package config

import (
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Config.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropMap := createAWSPropertyMap()
	return &transformerv1.AbstractResourceDefinition{
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
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitConfig),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropMap,
				func(r *ResolvedConfig) string {
					return configConcreteName(r.Name)
				},
			),
		},
	}
}

func configConcreteName(name string) string {
	return fmt.Sprintf("%s_config_store", name)
}
