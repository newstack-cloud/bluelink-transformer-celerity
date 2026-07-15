package queue

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity Queue.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:   "celerity/queue",
		Label:  "Celerity Queue",
		Schema: queueResourceSchema(),
	}
}

// TransformResource implements the transformation logic for the Celerity queue resource.
func TransformResource(
	resourceName string,
	resource *schema.Resource,
	targetResources *schema.ResourceMap,
	transformerContext transform.Context,
) {
	// TODO: implement transformation logic
	targetResources.Values[resourceName] = resource
}
