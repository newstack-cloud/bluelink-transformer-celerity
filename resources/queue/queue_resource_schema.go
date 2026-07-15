package queue

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func queueResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "QueueDefinition",
	}
}
