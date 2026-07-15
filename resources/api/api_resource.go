package api

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity API.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:                 "celerity/api",
		Label:                "Celerity API",
		PlainTextSummary:     "",
		FormattedSummary:     "",
		PlainTextDescription: "",
		FormattedDescription: "",
		Schema:               apiResourceSchema(),
	}
}

// TransformResource implements the transformation logic for the Celerity API resource.
func TransformResource(
	resourceName string,
	resource *schema.Resource,
	targetResources *schema.ResourceMap,
	transformerContext transform.Context,
) {
	// TODO: implement transformation logic
	targetResources.Values[resourceName] = resource
}
