package consumer

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func consumerResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "ConsumerDefinition",
	}
}
