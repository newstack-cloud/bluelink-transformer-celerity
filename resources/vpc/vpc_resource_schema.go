package vpc

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
)

func vpcResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "VpcDefinition",
		Description: "A managed virtual network that handlers, caches and databases are placed into. On " +
			"AWS this maps to a synthetic aws/flex/vpc that owns the VPC, subnets, routing and " +
			"security groups.",
		Required: []string{"name"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The unique name of the VPC. Also the shared-identity key: a \"referenced\" VPC " +
					"locates the existing Celerity-managed VPC with the same name, so a referencing resource " +
					"must use the same name as the managed VPC it points at.",
			},
			"preset": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The preset determining the VPC topology (subnets, routing, access controls). " +
					"Ignored when \"mode\" is \"referenced\" (the topology already exists).",
				Default: core.MappingNodeFromString("standard"),
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("standard"),
					core.MappingNodeFromString("public"),
					core.MappingNodeFromString("isolated"),
					core.MappingNodeFromString("light"),
					core.MappingNodeFromString("light-public"),
				},
			},
			"mode": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "Whether Celerity provisions the VPC (\"managed\") or references an existing " +
					"Celerity-managed VPC of the same name (\"referenced\"). Referenced mode shares a VPC " +
					"across blueprints without a cross-blueprint link.",
				Default: core.MappingNodeFromString("managed"),
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("managed"),
					core.MappingNodeFromString("referenced"),
				},
			},

			// Computed output.
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The ID of the VPC in the target environment. On AWS this is the " +
					"VPC id.",
			},
		},
	}
}
