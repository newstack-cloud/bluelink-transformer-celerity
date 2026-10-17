package vpc

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity VPC.
func Resource(
	implementations ...shared.TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	return shared.ApplyTargetImplementations(&transformerv1.AbstractResourceDefinition{
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
	}, implementations)
}
