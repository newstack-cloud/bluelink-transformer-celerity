package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

const vpcSubnetTypeAnnotationDefKey = "celerity/handler::" + handler.AnnotationKeyVPCSubnetType

func VPCToHandlerLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/vpc",
		ResourceTypeB:    "celerity/handler",
		PlainTextSummary: "Runs a handler within a VPC network.",
		FormattedSummary: "Runs a `celerity/handler` within a `celerity/vpc` network.",
		PlainTextDescription: "Places the handler inside the VPC so it can reach resources on the private " +
			"network such as databases, caches and internal services. The celerity.handler.vpc.subnetType " +
			"annotation selects public or private subnets (defaults to private). A handler belongs to at " +
			"most one VPC. " +
			"This link only has an effect on applications that are deployed " +
			"to serverless platforms that support function-level VPC networking, such as AWS Lambda.",
		FormattedDescription: "Places the handler inside the VPC so it can reach resources on the private " +
			"network such as databases, caches and internal services. The `celerity.handler.vpc.subnetType` " +
			"annotation selects `public` or `private` subnets (defaults to `private`). A handler belongs to " +
			"at most one `celerity/vpc`. " +
			"This link only has an effect on applications that are deployed " +
			"to serverless platforms that support function-level VPC networking, such as AWS Lambda.",
		// A handler belongs to at most one VPC.
		CardinalityB: provider.LinkCardinality{Min: 0, Max: 1},
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			vpcSubnetTypeAnnotationDefKey: {
				Name:         handler.AnnotationKeyVPCSubnetType,
				Label:        "VPC subnet type",
				Type:         core.ScalarTypeString,
				AppliesTo:    provider.LinkAnnotationResourceB,
				DefaultValue: core.ScalarFromString(handler.SubnetTypePrivate),
				AllowedValues: []*core.ScalarValue{
					core.ScalarFromString(handler.SubnetTypePublic),
					core.ScalarFromString(handler.SubnetTypePrivate),
				},
				Description: "Selects whether the handler runs in the VPC's public or private subnets. " +
					"Defaults to private.",
			},
		},
	}
}
