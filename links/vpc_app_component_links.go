package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// VPCToAPILink declares the celerity/vpc -> celerity/api relationship.
func VPCToAPILink() *transformerv1.AbstractLinkDefinition {
	return vpcToAppComponentLink(&appComponentPlacement{
		ResourceType:     "celerity/api",
		Noun:             "API",
		ContainerPlaced:  "the API service",
		AnnotationKey:    handler.AnnotationKeyAPIVPCSubnetType,
		AnnotationLabel:  "API VPC subnet type",
		EgressKey:        handler.AnnotationKeyAPIVPCEgress,
		EgressLabel:      "API VPC egress",
		ComponentHandler: "the API's HTTP, WebSocket and guard handlers",
	})
}

// VPCToConsumerLink declares the celerity/vpc -> celerity/consumer relationship.
func VPCToConsumerLink() *transformerv1.AbstractLinkDefinition {
	return vpcToAppComponentLink(&appComponentPlacement{
		ResourceType:     "celerity/consumer",
		Noun:             "consumer",
		ContainerPlaced:  "the consumer application",
		AnnotationKey:    handler.AnnotationKeyConsumerVPCSubnetType,
		AnnotationLabel:  "Consumer VPC subnet type",
		EgressKey:        handler.AnnotationKeyConsumerVPCEgress,
		EgressLabel:      "Consumer VPC egress",
		ComponentHandler: "the consumer's handlers",
	})
}

// VPCToScheduleLink declares the celerity/vpc -> celerity/schedule relationship.
func VPCToScheduleLink() *transformerv1.AbstractLinkDefinition {
	return vpcToAppComponentLink(&appComponentPlacement{
		ResourceType:     "celerity/schedule",
		Noun:             "schedule",
		ContainerPlaced:  "the schedule application component",
		AnnotationKey:    handler.AnnotationKeyScheduleVPCSubnetType,
		AnnotationLabel:  "Schedule VPC subnet type",
		EgressKey:        handler.AnnotationKeyScheduleVPCEgress,
		EgressLabel:      "Schedule VPC egress",
		ComponentHandler: "the schedule's handlers",
	})
}

type appComponentPlacement struct {
	ResourceType     string
	Noun             string
	ContainerPlaced  string
	AnnotationKey    string
	AnnotationLabel  string
	EgressKey        string
	EgressLabel      string
	ComponentHandler string
}

// On containerised targets the component itself is the unit of compute, so the
// VPC places it directly. On serverless targets it is a trigger binding with no
// compute of its own, so the placement cascades to the handlers it is linked to.
// The same blueprint then expresses "run this component's compute in this
// network" on either target.
func vpcToAppComponentLink(component *appComponentPlacement) *transformerv1.AbstractLinkDefinition {
	formattedSummary := "Places a `" + component.ResourceType + "` within a `celerity/vpc`."
	formattedDescription := "Places " + component.ContainerPlaced + " inside the VPC when the " +
		component.Noun + " is deployed as a containerised application. On serverless targets the " +
		component.Noun + " is not itself deployed to a customer-managed network, so the placement " +
		"cascades to " + component.ComponentHandler + ": each is attached to the VPC as though it " +
		"were linked to the `celerity/vpc` directly. A `celerity/vpc` linked to a handler directly " +
		"takes precedence over an inherited placement, and a handler that inherits conflicting " +
		"placements from more than one component is an error. The `" + component.AnnotationKey +
		"` annotation selects the subnet tier for the handlers that inherit this placement; a " +
		"handler's own `" + handler.AnnotationKeyVPCSubnetType + "` annotation overrides it. The `" +
		component.EgressKey + "` annotation narrows their outbound reach on the same terms, " +
		"overridden by a handler's own `" + handler.AnnotationKeyVPCEgress + "`."

	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:        "celerity/vpc",
		ResourceTypeB:        component.ResourceType,
		PlainTextSummary:     stripMarkdown(formattedSummary),
		FormattedSummary:     formattedSummary,
		PlainTextDescription: stripMarkdown(formattedDescription),
		FormattedDescription: formattedDescription,
		// A component is placed within at most one VPC.
		CardinalityB: provider.LinkCardinality{Min: 0, Max: 1},
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			component.ResourceType + "::" + component.AnnotationKey: {
				Name:         component.AnnotationKey,
				Label:        component.AnnotationLabel,
				Type:         core.ScalarTypeString,
				AppliesTo:    provider.LinkAnnotationResourceB,
				DefaultValue: core.ScalarFromString(handler.SubnetTypePrivate),
				AllowedValues: []*core.ScalarValue{
					core.ScalarFromString(handler.SubnetTypePublic),
					core.ScalarFromString(handler.SubnetTypePrivate),
				},
				Description: "Selects whether compute placed in the VPC for this " + component.Noun +
					" runs in the VPC's public or private subnets. Defaults to private.",
			},
			// No default value and no allowed-values list, for the same reasons as the
			// handler's own egress annotation.
			component.ResourceType + "::" + component.EgressKey: {
				Name:        component.EgressKey,
				Label:       component.EgressLabel,
				Type:        core.ScalarTypeString,
				AppliesTo:   provider.LinkAnnotationResourceB,
				Description: vpcEgressDescription,
			},
		},
	}
}
