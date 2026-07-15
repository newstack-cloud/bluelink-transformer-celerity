package topic

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func topicResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "TopicDefinition",
	}
}
