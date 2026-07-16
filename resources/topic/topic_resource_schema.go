package topic

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
)

func topicResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "TopicDefinition",
		Description: "A managed publish/subscribe topic that handlers publish messages to and consumers " +
			"subscribe to. On AWS this maps to an SNS topic.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The unique name of the topic. If a name is not provided, a unique name is " +
					"generated based on the blueprint the topic is defined in. When \"fifo\" is true, the " +
					"target environment may require the name to end with the \".fifo\" suffix; the " +
					"transformer appends it automatically when missing.",
			},
			"fifo": {
				Type: provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "If true, the topic is configured as a FIFO (first-in, first-out) topic: " +
					"messages are delivered in the order they are published and duplicates are not introduced.",
				Default: core.MappingNodeFromBool(false),
			},
			"encryptionKeyId": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The ID of the KMS key used to encrypt messages at rest. Optional; when " +
					"omitted, the target environment's default encryption is used. On AWS this " +
					"maps to the SNS topic's KMS master key id.",
			},

			// Computed output.
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The ID of the created topic in the target environment. On AWS " +
					"this is the topic ARN.",
			},
		},
	}
}
