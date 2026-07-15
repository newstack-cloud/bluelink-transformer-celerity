package sqldatabase

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity SQL Database.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:   "celerity/sqlDatabase",
		Label:  "Celerity SQL Database",
		Schema: sqlDatabaseResourceSchema(),
	}
}

// TransformResource implements the transformation logic for the Celerity SQL database resource.
func TransformResource(
	resourceName string,
	resource *schema.Resource,
	targetResources *schema.ResourceMap,
	transformerContext transform.Context,
) {
	// TODO: implement transformation logic
	targetResources.Values[resourceName] = resource
}
