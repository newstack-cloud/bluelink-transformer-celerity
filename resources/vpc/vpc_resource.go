package vpc

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity VPC.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/vpc",
		Label:            "Celerity VPC",
		PlainTextSummary: "A virtual private cloud for a Celerity application.",
		FormattedSummary: "A virtual private cloud for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a virtual private cloud in which " +
			"Celerity applications, databases and caches can be placed.",
		FormattedDescription: "The abstract resource type used to define a virtual private cloud in which " +
			"Celerity applications, databases and caches can be placed.\n\n" +
			"See the " +
			"[`celerity/vpc` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-vpc) " +
			"for more information.",
		Schema:  vpcResourceSchema(),
		Resolve: resolveVPC,
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitVPC),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedVPC) string {
					return ConcreteResourceName(r.Name)
				},
			),
		},
	}
}
