package queue

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
)

func queueResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "QueueDefinition",
		Description: "A managed message queue that handlers send messages to and consumers process " +
			"messages from. On AWS this maps to an SQS queue.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The unique name of the queue. If a name is not provided, a unique name is " +
					"generated based on the blueprint the queue is defined in. When \"fifo\" is true, the " +
					"target environment may require the name to end with the \".fifo\" suffix; the " +
					"transformer appends it automatically when missing.",
			},
			"fifo": {
				Type: provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "If true, the queue is configured as a FIFO (first-in, first-out) queue: " +
					"messages are processed in the order they are received and duplicates are not introduced.",
				Default: core.MappingNodeFromBool(false),
			},
			"visibilityTimeout": {
				Type: provider.ResourceDefinitionsSchemaTypeInteger,
				Description: "The time in seconds that a message is hidden from other consumers after it " +
					"has been received from the queue. On AWS this maps to the SQS queue's " +
					"visibility timeout.",
			},
			"encryptionKeyId": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The ID of the KMS key used to encrypt messages at rest. Optional; when " +
					"omitted, the target environment's default encryption is used. On AWS this " +
					"maps to the SQS queue's KMS master key id.",
			},

			// Computed output.
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The ID of the created queue in the target environment. On AWS " +
					"this is the queue ARN.",
			},
		},
	}
}
