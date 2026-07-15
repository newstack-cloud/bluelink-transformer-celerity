package topic

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity Topic.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:   "celerity/topic",
		Label:  "Celerity Topic",
		Schema: topicResourceSchema(),
	}
}

// TransformResource implements the transformation logic for the Celerity topic resource.
func TransformResource(
	resourceName string,
	resource *schema.Resource,
	targetResources *schema.ResourceMap,
	transformerContext transform.Context,
) {
	// TODO: implement transformation logic
	targetResources.Values[resourceName] = resource
}
