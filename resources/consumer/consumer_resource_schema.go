package consumer

import "github.com/newstack-cloud/bluelink/libs/blueprint/core"
import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func consumerResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "ConsumerDefinition",
		Description: "A subscriber to messages on a topic, events in a data store or bucket, or " +
			"messages from a queue or externally defined message source. On aws-serverless a consumer " +
			"is a placeholder for a connection between an event source and a handler: the handler it " +
			"links to absorbs it and the event source is wired up as a trigger (event source mapping, " +
			"S3 notification or SNS subscription) for the handler's Lambda function.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"sourceId": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "A unique identifier for the topic, queue, message broker or other event " +
					"source the consumer listens to. The type of source is interpreted based on the target " +
					"environment. A Celerity topic is referenced with the celerity::topic:: prefix followed " +
					"by the topic ARN. Not required when the consumer is linked from a celerity/datastore, " +
					"celerity/bucket or celerity/queue (the source is derived from the link), or when " +
					"externalEvents is set. Must not be set when externalEvents has at least one entry.",
			},
			"batchSize": {
				Type: provider.ResourceDefinitionsSchemaTypeInteger,
				Description: "The maximum number of messages to retrieve in a single poll. On " +
					"aws-serverless this maps to the SQS or stream event source mapping batch size " +
					"(default 10 for SQS, 100 for streams; max 10,000). Some target environments limit or " +
					"ignore this value.",
			},
			"visibilityTimeout": {
				Type: provider.ResourceDefinitionsSchemaTypeInteger,
				Description: "The time in seconds that a message is hidden from other consumers after " +
					"being retrieved. Not applicable (N/A) on aws-serverless: SQS Lambda triggers derive " +
					"visibility from the queue, so this value is ignored and not emitted.",
			},
			"waitTimeSeconds": {
				Type: provider.ResourceDefinitionsSchemaTypeInteger,
				Description: "The time in seconds to wait for messages to become available before polling " +
					"again. Not applicable (N/A) on aws-serverless: the SQS Lambda trigger manages polling, " +
					"so this value is ignored and not emitted.",
			},
			"partialFailures": {
				Type:    provider.ResourceDefinitionsSchemaTypeBoolean,
				Default: core.MappingNodeFromBool(false),
				Description: "Whether partial failure reporting is supported so that only failed messages " +
					"are retried. On aws-serverless this maps to the event source mapping " +
					"ReportBatchItemFailures function response type.",
			},
			"routingKey": {
				Type:    provider.ResourceDefinitionsSchemaTypeString,
				Default: core.MappingNodeFromString("event"),
				Description: "The field in a JSON message payload used to route messages to a specific " +
					"handler. Only used when routing is activated via the celerity.handler.consumer.route " +
					"annotation on a handler.",
			},
			"externalEvents": {
				Type: provider.ResourceDefinitionsSchemaTypeMap,
				Description: "A mapping of cloud service event configurations the consumer responds to, " +
					"such as object storage events, database streams or data streams from an existing " +
					"(out-of-blueprint) source. Must not be present when sourceId is set.",
				MapValues: externalEventConfigurationSchema(),
			},

			// Computed output.
			"subscriberId": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Nullable: true,
				Description: "The ID of the subscription created for the consumer when the sourceId is a " +
					"Celerity topic (a queue ID or subscription ID depending on the target environment). " +
					"Only present when a queue or subscription is created to subscribe to the topic.",
			},
		},
	}
}

func externalEventConfigurationSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type: provider.ResourceDefinitionsSchemaTypeObject,
		Description: "Configuration for a cloud service event trigger the consumer responds to. Supports " +
			"a limited, general set of event sources (object storage, database streams and data streams).",
		Required: []string{"sourceType", "sourceConfiguration"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"sourceType": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The type of event source the consumer responds to. On aws-serverless " +
					"objectStorage maps to S3 notifications, dbStream to DynamoDB Streams and dataStream " +
					"to Kinesis.",
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("objectStorage"),
					core.MappingNodeFromString("dbStream"),
					core.MappingNodeFromString("dataStream"),
				},
			},
			"sourceConfiguration": {
				Type: provider.ResourceDefinitionsSchemaTypeObject,
				Description: "The event source configuration for the selected sourceType. The applicable " +
					"fields depend on sourceType: objectStorage uses events and bucket; dbStream and " +
					"dataStream use batchSize, the stream ID, partialFailures and startFromBeginning.",
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"events": {
						Type: provider.ResourceDefinitionsSchemaTypeArray,
						Description: "For objectStorage sources, the object storage events that should " +
							"trigger the consumer (allowed values: created, deleted, metadataUpdated).",
						Items: &provider.ResourceDefinitionsSchema{
							Type:        provider.ResourceDefinitionsSchemaTypeString,
							Description: "An object storage event: created, deleted or metadataUpdated.",
						},
					},
					"bucket": {
						Type: provider.ResourceDefinitionsSchemaTypeString,
						Description: "For objectStorage sources, the name of the bucket the consumer " +
							"responds to events from.",
					},
					"batchSize": {
						Type: provider.ResourceDefinitionsSchemaTypeInteger,
						Description: "For dbStream and dataStream sources, the maximum number of events to " +
							"retrieve per batch. On aws-serverless the maximum is 10,000.",
					},
					"dbStreamId": {
						Type: provider.ResourceDefinitionsSchemaTypeString,
						Description: "For dbStream sources, the ID of the database stream the consumer " +
							"responds to events from. On aws-serverless this is the DynamoDB stream ARN " +
							"(maps to the event source mapping eventSourceArn).",
					},
					"dataStreamId": {
						Type: provider.ResourceDefinitionsSchemaTypeString,
						Description: "For dataStream sources, the ID of the data stream the consumer " +
							"responds to events from. On aws-serverless this is the Kinesis stream ARN " +
							"(maps to the event source mapping eventSourceArn).",
					},
					"partialFailures": {
						Type: provider.ResourceDefinitionsSchemaTypeBoolean,
						Description: "For dbStream and dataStream sources, whether partial failure " +
							"reporting is supported so only failed records are retried. On aws-serverless " +
							"this maps to the event source mapping function response types.",
					},
					"startFromBeginning": {
						Type: provider.ResourceDefinitionsSchemaTypeBoolean,
						Description: "For dbStream and dataStream sources, whether to start processing from " +
							"the earliest available point. On aws-serverless true maps to a " +
							"startingPosition of TRIM_HORIZON.",
					},
				},
			},
		},
	}
}
