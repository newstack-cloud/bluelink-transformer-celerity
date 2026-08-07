package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

const (
	vpcSubnetTypeAnnotationDefKey = "celerity/handler::" + handler.AnnotationKeyVPCSubnetType
	vpcEgressAnnotationDefKey     = "celerity/handler::" + handler.AnnotationKeyVPCEgress
)

// Shared by the handler link and the application-component links, which offer the
// same choice at the level they cascade from.
const vpcEgressDescription = "Narrows what the handler may reach outside the VPC: " +
	"\"internet\", \"none\", or a comma-separated list of CIDR ranges. Leave it unset to keep " +
	"whatever outbound access the network's topology provides, which is not the same as " +
	"\"none\": placing a handler in a VPC never silently withdraws access it had before, and " +
	"this annotation only ever narrows."

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
			// No default value and no allowed-values list, unset is a meaningful third
			// state the deploy target resolves, and a CIDR list cannot be enumerated.
			vpcEgressAnnotationDefKey: {
				Name:        handler.AnnotationKeyVPCEgress,
				Label:       "VPC egress",
				Type:        core.ScalarTypeString,
				AppliesTo:   provider.LinkAnnotationResourceB,
				Description: vpcEgressDescription,
			},
		},
	}
}
