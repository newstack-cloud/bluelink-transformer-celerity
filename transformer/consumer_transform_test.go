//go:build unit

package transformer

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type ConsumerTransformTestSuite struct {
	suite.Suite
}

func TestConsumerTransformTestSuite(t *testing.T) {
	suite.Run(t, new(ConsumerTransformTestSuite))
}

// A queue -> consumer -> handler chain wires the concrete aws/sqs/queue to the
// function via the label union, and stamps the SQS event-source-mapping
// annotations from the consumer's spec.
func (s *ConsumerTransformTestSuite) Test_queue_consumer_wires_sqs_poll_trigger() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("orderHandler"),
			"handler", core.MappingNodeFromString("handlers.process"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
			Labels:      &schema.StringMap{Values: map[string]string{"app": "orders"}},
		},
	}
	queueRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		// The abstract queue selected the consumer by label.
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"role": "orders-consumer"}},
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields(
			"batchSize", core.MappingNodeFromInt(25),
			"partialFailures", core.MappingNodeFromBool(true),
			// visibilityTimeout is N/A on serverless and must never surface.
			"visibilityTimeout", core.MappingNodeFromInt(30),
		),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"role": "orders-consumer"}},
		},
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"orderHandler":   handlerRes,
			"ordersQueue":    queueRes,
			"ordersConsumer": consumerRes,
		},
		edges(
			edge("ordersQueue", "ordersConsumer", "celerity/queue", "celerity/consumer"),
			edge("ordersConsumer", "orderHandler", "celerity/consumer", "celerity/handler"),
		),
	)

	lambda := resources["orderHandler_lambda_func"]
	s.Require().NotNil(lambda)

	// Label union: the function carries the handler's own label and the absorbed
	// consumer's label so the concrete queue's linkSelector now matches it.
	s.Require().NotNil(lambda.Metadata.Labels)
	s.Equal("orders", lambda.Metadata.Labels.Values["app"])
	s.Equal("orders-consumer", lambda.Metadata.Labels.Values["role"])

	// The concrete queue keeps the linkSelector that now resolves to the function.
	sqs := resources["ordersQueue_sqs_queue"]
	s.Require().NotNil(sqs)
	s.Require().NotNil(sqs.LinkSelector)
	s.Equal("orders-consumer", sqs.LinkSelector.ByLabel.Values["role"])

	// SQS event-source-mapping annotations stamped from the consumer spec.
	s.Equal("25", annotationLiteral(lambda.Metadata.Annotations, "aws.sqs.lambda.batchSize"))
	s.Equal("true", annotationLiteral(lambda.Metadata.Annotations, "aws.sqs.lambda.reportBatchItemFailures"))

	// visibilityTimeout is dropped entirely on serverless.
	s.Nil(lambda.Spec.Fields["visibilityTimeout"])
	for key := range lambda.Metadata.Annotations.Values {
		s.NotContains(key, "visibilityTimeout")
		s.NotContains(key, "waitTimeSeconds")
	}

	// The abstract consumer does not survive into the concrete output.
	s.NotContains(resources, "ordersConsumer")
}

// A consumer whose sourceId is a literal Celerity-topic ARN wires the SNS -> SQS ->
// Lambda fan-out: an intermediary queue subscribed to the topic (sqs protocol) that
// triggers the function, plus a dead-letter queue by default.
func (s *ConsumerTransformTestSuite) Test_topic_consumer_emits_sns_sqs_fanout() {
	const topicARN = "arn:aws:sns:us-east-1:123456789012:orders-topic"

	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("notifyHandler"),
			"handler", core.MappingNodeFromString("handlers.notify"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields(
			"sourceId", core.MappingNodeFromString("celerity::topic::"+topicARN),
			"batchSize", core.MappingNodeFromInt(20),
			"partialFailures", core.MappingNodeFromBool(true),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"notifyHandler": handlerRes,
			"topicConsumer": consumerRes,
		},
		edges(edge("topicConsumer", "notifyHandler", "celerity/consumer", "celerity/handler")),
	)

	// (a) An intermediary SQS fan-out queue; its ARN is the consumer's subscriberId.
	queue := resources["topicConsumer_topic_queue"]
	s.Require().NotNil(queue, "expected an SQS fan-out queue for the topic-sourced consumer")
	s.Equal("aws/sqs/queue", queue.Type.Value)
	// Its linkSelector carries the synthetic poll label so the queue -> function
	// event source mapping link resolves against the emitted Lambda.
	s.Require().NotNil(queue.LinkSelector)
	s.Equal("true", queue.LinkSelector.ByLabel.Values["celerity.consumer.topicPoll.topicConsumer"])

	// (b) An SNS subscription (sqs protocol) delivering to the queue.
	sub := resources["topicConsumer_sns_subscription"]
	s.Require().NotNil(sub, "expected an aws/sns/subscription for the topic-sourced consumer")
	s.Equal("aws/sns/subscription", sub.Type.Value)
	s.Equal("sqs", core.StringValue(sub.Spec.Fields["protocol"]))
	s.Equal(topicARN, core.StringValue(sub.Spec.Fields["topicArn"]))
	// The endpoint references the fan-out queue's ARN (reference-implied SNS->SQS link).
	s.Equal("topicConsumer_topic_queue", resourceRefName(sub.Spec.Fields["endpoint"]))

	// (c) The function carries the poll label + the SQS event-source-mapping
	// annotations derived from the topic-path batchSize/partialFailures.
	lambda := resources["notifyHandler_lambda_func"]
	s.Require().NotNil(lambda)
	s.Require().NotNil(lambda.Metadata.Labels)
	s.Equal("true", lambda.Metadata.Labels.Values["celerity.consumer.topicPoll.topicConsumer"])
	s.Equal("20", annotationLiteral(lambda.Metadata.Annotations, "aws.sqs.lambda.batchSize"))
	s.Equal("true", annotationLiteral(lambda.Metadata.Annotations, "aws.sqs.lambda.reportBatchItemFailures"))

	// (d) A dead-letter queue is created by default and carries the poll label so the
	// fan-out queue's redrive link selects it.
	dlq := resources["topicConsumer_topic_dlq"]
	s.Require().NotNil(dlq, "expected a dead-letter queue by default")
	s.Equal("aws/sqs/queue", dlq.Type.Value)
	s.Equal("true", dlq.Metadata.Labels.Values["celerity.consumer.topicPoll.topicConsumer"])
}

// celerity.consumer.deadLetterQueue = false suppresses the DLQ; a
// deadLetterQueueMaxAttempts value drives the fan-out queue's redrive annotation.
func (s *ConsumerTransformTestSuite) Test_topic_consumer_dead_letter_queue_toggle() {
	const topicARN = "arn:aws:sns:us-east-1:123456789012:orders-topic"

	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("notifyHandler"),
			"handler", core.MappingNodeFromString("handlers.notify"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields(
			"sourceId", core.MappingNodeFromString("celerity::topic::"+topicARN),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.consumer.deadLetterQueue", "false"),
		},
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"notifyHandler": handlerRes,
			"topicConsumer": consumerRes,
		},
		edges(edge("topicConsumer", "notifyHandler", "celerity/consumer", "celerity/handler")),
	)

	s.NotContains(resources, "topicConsumer_topic_dlq", "DLQ must be suppressed when disabled")
	s.Contains(resources, "topicConsumer_topic_queue")
	q := resources["topicConsumer_topic_queue"]
	s.Equal("", annotationLiteral(q.Metadata.Annotations, "aws.sqs.redrive.maxReceiveCount"))
}

// When the sourceId is a substitution referencing an in-blueprint celerity/topic,
// the same fan-out is wired, and the subscription's topicArn is rewritten (like the
// Lambda spec) from the abstract topic reference to the concrete <topic>_sns_topic.
func (s *ConsumerTransformTestSuite) Test_in_blueprint_topic_consumer_emits_fanout() {
	sourceID, err := shared.SubstitutionMappingNode("${ordersTopic.spec.id}")
	s.Require().NoError(err)

	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("notifyHandler"),
			"handler", core.MappingNodeFromString("handlers.notify"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	topicRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/topic"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: &core.MappingNode{Fields: map[string]*core.MappingNode{"sourceId": sourceID}},
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"notifyHandler": handlerRes,
			"ordersTopic":   topicRes,
			"topicConsumer": consumerRes,
		},
		edges(edge("topicConsumer", "notifyHandler", "celerity/consumer", "celerity/handler")),
	)

	s.Require().NotNil(resources["topicConsumer_topic_queue"])
	sub := resources["topicConsumer_sns_subscription"]
	s.Require().NotNil(sub)
	s.Equal("sqs", core.StringValue(sub.Spec.Fields["protocol"]))
	// topicArn is rewritten to the concrete SNS topic the pipeline emits for the
	// in-blueprint celerity/topic, not the dangling abstract name.
	s.Equal("ordersTopic_sns_topic", resourceRefName(sub.Spec.Fields["topicArn"]))
}

// A ${<consumer>.spec.subscriberId} reference resolves to the concrete SQS fan-out
// queue created for the topic consumer. The consumer is contributory (its rewriter
// is never chained); the absorbing handler — a primary — contributes this mapping.
func (s *ConsumerTransformTestSuite) Test_topic_consumer_subscriberId_resolves_to_fanout_queue() {
	const topicARN = "arn:aws:sns:us-east-1:123456789012:orders-topic"

	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("notifyHandler"),
			"handler", core.MappingNodeFromString("handlers.notify"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields(
			"sourceId", core.MappingNodeFromString("celerity::topic::"+topicARN),
		),
	}
	subscriberValue, err := shared.SubstitutionBlueprintValue("${topicConsumer.spec.subscriberId}")
	s.Require().NoError(err)

	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
			"notifyHandler": handlerRes,
			"topicConsumer": consumerRes,
		}},
		Values: &schema.ValueMap{Values: map[string]*schema.Value{
			"subscriberOut": subscriberValue,
		}},
	}

	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: bp,
			LinkGraph: edges(edge(
				"topicConsumer", "notifyHandler", "celerity/consumer", "celerity/handler",
			)),
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)

	resolved := out.TransformedBlueprint.Values.Values["subscriberOut"]
	s.Require().NotNil(resolved)
	name, path := resourceRefNameAndPath(resolved.Value)
	s.Equal("topicConsumer_topic_queue", name,
		"subscriberId must resolve to the concrete fan-out queue")
	s.Equal([]string{"spec", "arn"}, path)
}

// An externalEvents dbStream source emits a standalone DynamoDB Streams event source
// mapping targeting the function.
func (s *ConsumerTransformTestSuite) Test_external_dbStream_consumer_emits_event_source_mapping() {
	const streamARN = "arn:aws:dynamodb:us-east-1:123456789012:table/Orders/stream/2021-07-01T00:00:00.000"

	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("streamHandler"),
			"handler", core.MappingNodeFromString("handlers.stream"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields(
			"externalEvents", core.MappingNodeFields(
				"orders", core.MappingNodeFields(
					"sourceType", core.MappingNodeFromString("dbStream"),
					"sourceConfiguration", core.MappingNodeFields(
						"dbStreamId", core.MappingNodeFromString(streamARN),
						"batchSize", core.MappingNodeFromInt(50),
						"partialFailures", core.MappingNodeFromBool(true),
						"startFromBeginning", core.MappingNodeFromBool(true),
					),
				),
			),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"streamHandler":  handlerRes,
			"streamConsumer": consumerRes,
		},
		edges(edge("streamConsumer", "streamHandler", "celerity/consumer", "celerity/handler")),
	)

	esm := resources["streamConsumer_orders_esm"]
	s.Require().NotNil(esm, "expected an event source mapping for the external dbStream")
	s.Equal("aws/lambda/eventSourceMapping", esm.Type.Value)
	s.Equal(streamARN, core.StringValue(esm.Spec.Fields["eventSourceArn"]))
	s.Equal("streamHandler_lambda_func", resourceRefName(esm.Spec.Fields["functionName"]))
	s.Equal(50, core.IntValue(esm.Spec.Fields["batchSize"]))
	s.Equal("TRIM_HORIZON", core.StringValue(esm.Spec.Fields["startingPosition"]))
	frt := esm.Spec.Fields["functionResponseTypes"]
	s.Require().NotNil(frt)
	s.Require().Len(frt.Items, 1)
	s.Equal("ReportBatchItemFailures", core.StringValue(frt.Items[0]))

	// The standalone ESM has no provider link to inject source-read IAM, so the
	// handler's execution role seed must grant it, scoped to the external stream ARN.
	roleName := resourceRefName(resources["streamHandler_lambda_func"].Spec.Fields["role"])
	role := resources[roleName]
	s.Require().NotNil(role, "expected the referenced execution role to be emitted")
	policy := externalSourcesPolicy(s.T(), role)
	s.Require().NotNil(policy, "expected a celerity-external-event-sources policy on the role")
	statement := policy.Fields["statement"].Items[0].Fields
	s.Equal(streamARN, core.StringValue(statement["resource"]))
	s.Equal(
		[]string{
			"dynamodb:GetRecords",
			"dynamodb:GetShardIterator",
			"dynamodb:DescribeStream",
			"dynamodb:ListStreams",
		},
		core.StringSliceValue(statement["action"]),
	)
}

// externalSourcesPolicy returns the celerity-external-event-sources inline policy
// document from an aws/iam/role resource, or nil when absent.
func externalSourcesPolicy(t *testing.T, role *schema.Resource) *core.MappingNode {
	t.Helper()
	policies := role.Spec.Fields["policies"]
	if policies == nil {
		return nil
	}
	for _, entry := range policies.Items {
		if core.StringValue(entry.Fields["policyName"]) == "celerity-external-event-sources" {
			return entry.Fields["policyDocument"]
		}
	}
	return nil
}

// An externalEvents dataStream source emits a standalone Kinesis event source
// mapping; startFromBeginning defaults to a LATEST starting position.
func (s *ConsumerTransformTestSuite) Test_external_dataStream_consumer_emits_event_source_mapping() {
	const streamARN = "arn:aws:kinesis:us-east-1:123456789012:stream/Events"

	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("streamHandler"),
			"handler", core.MappingNodeFromString("handlers.stream"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields(
			"externalEvents", core.MappingNodeFields(
				"events", core.MappingNodeFields(
					"sourceType", core.MappingNodeFromString("dataStream"),
					"sourceConfiguration", core.MappingNodeFields(
						"dataStreamId", core.MappingNodeFromString(streamARN),
					),
				),
			),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"streamHandler":  handlerRes,
			"streamConsumer": consumerRes,
		},
		edges(edge("streamConsumer", "streamHandler", "celerity/consumer", "celerity/handler")),
	)

	esm := resources["streamConsumer_events_esm"]
	s.Require().NotNil(esm, "expected an event source mapping for the external dataStream")
	s.Equal(streamARN, core.StringValue(esm.Spec.Fields["eventSourceArn"]))
	s.Equal("LATEST", core.StringValue(esm.Spec.Fields["startingPosition"]))
	s.Nil(esm.Spec.Fields["batchSize"], "no batchSize was configured")
	s.Nil(esm.Spec.Fields["functionResponseTypes"], "partialFailures was not set")
}

// An external SQS sourceId given as a queue URL must emit an eventSourceArn in
// ARN form, since AWS's event source mapping rejects a queue URL.
func (s *ConsumerTransformTestSuite) Test_external_sqs_url_sourceId_emits_arn_event_source() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("extHandler"),
			"handler", core.MappingNodeFromString("handlers.ext"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields(
			"sourceId", core.MappingNodeFromString(
				"https://sqs.us-east-1.amazonaws.com/123456789012/ext-queue"),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"extHandler":  handlerRes,
			"extConsumer": consumerRes,
		},
		edges(edge("extConsumer", "extHandler", "celerity/consumer", "celerity/handler")),
	)

	esm := resources["extConsumer_sqs_esm"]
	s.Require().NotNil(esm, "expected an event source mapping for the external SQS source")
	s.Equal(
		"arn:aws:sqs:us-east-1:123456789012:ext-queue",
		core.StringValue(esm.Spec.Fields["eventSourceArn"]),
		"the queue URL must be normalised to its ARN",
	)
	s.Equal("extHandler_lambda_func", resourceRefName(esm.Spec.Fields["functionName"]))
}

// A metadataUpdated bucket event has no S3 equivalent and is surfaced as a warning
// rather than dropped silently (L1).
func (s *ConsumerTransformTestSuite) Test_bucket_metadata_updated_event_warns() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("bucketHandler"),
			"handler", core.MappingNodeFromString("handlers.bucket"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
			Labels:      &schema.StringMap{Values: map[string]string{"app": "invoices"}},
		},
	}
	bucketRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/bucket"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("invoices")),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"role": "invoice-consumer"}},
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.consumer.bucket.events", "created,metadataUpdated"),
			Labels:      &schema.StringMap{Values: map[string]string{"role": "invoice-consumer"}},
		},
	}

	out := s.transformOutput(
		map[string]*schema.Resource{
			"bucketHandler":  handlerRes,
			"invoicesBucket": bucketRes,
			"bucketConsumer": consumerRes,
		},
		edges(
			edge("invoicesBucket", "bucketConsumer", "celerity/bucket", "celerity/consumer"),
			edge("bucketConsumer", "bucketHandler", "celerity/consumer", "celerity/handler"),
		),
	)

	s.True(
		hasWarningContaining(out.Diagnostics, "metadataUpdated"),
		"expected a warning about the unsupported metadataUpdated event",
	)
}

// Two bucket consumers absorbed by one handler must occupy distinct
// aws.s3.lambda.event.<index> slots; a per-binding index reset would overwrite
// the first bucket's events with the second's.
func (s *ConsumerTransformTestSuite) Test_two_bucket_consumers_use_unique_s3_event_indices() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("bucketHandler"),
			"handler", core.MappingNodeFromString("handlers.bucket"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	bucketRes := func(name, role string) *schema.Resource {
		return &schema.Resource{
			Type:         &schema.ResourceTypeWrapper{Value: "celerity/bucket"},
			Spec:         core.MappingNodeFields("name", core.MappingNodeFromString(name)),
			LinkSelector: &schema.LinkSelector{ByLabel: &schema.StringMap{Values: map[string]string{"role": role}}},
		}
	}
	consumerRes := func(events, role string) *schema.Resource {
		return &schema.Resource{
			Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
			Metadata: &schema.Metadata{
				Annotations: annotationMap("celerity.consumer.bucket.events", events),
				Labels:      &schema.StringMap{Values: map[string]string{"role": role}},
			},
		}
	}

	out := s.transformOutput(
		map[string]*schema.Resource{
			"bucketHandler":  handlerRes,
			"invoicesBucket": bucketRes("invoices", "invoice-consumer"),
			"receiptsBucket": bucketRes("receipts", "receipt-consumer"),
			"invoiceConsumer": consumerRes("created", "invoice-consumer"),
			"receiptConsumer": consumerRes("deleted", "receipt-consumer"),
		},
		edges(
			edge("invoicesBucket", "invoiceConsumer", "celerity/bucket", "celerity/consumer"),
			edge("receiptsBucket", "receiptConsumer", "celerity/bucket", "celerity/consumer"),
			edge("invoiceConsumer", "bucketHandler", "celerity/consumer", "celerity/handler"),
			edge("receiptConsumer", "bucketHandler", "celerity/consumer", "celerity/handler"),
		),
	)

	lambda := out.TransformedBlueprint.Resources.Values["bucketHandler_lambda_func"]
	s.Require().NotNil(lambda)

	// Both bindings' events survive on distinct indices (order follows binding order).
	got := []string{
		annotationLiteral(lambda.Metadata.Annotations, "aws.s3.lambda.event.0"),
		annotationLiteral(lambda.Metadata.Annotations, "aws.s3.lambda.event.1"),
	}
	s.ElementsMatch(
		[]string{"s3:ObjectCreated:*", "s3:ObjectRemoved:*"},
		got,
		"each bucket consumer's event must occupy its own index, not overwrite the other",
	)

	// The two buckets request divergent event sets, which the provider applies as
	// a union to both; that limitation is surfaced rather than left silent.
	s.True(
		hasWarningContaining(out.Diagnostics, "combined into one list"),
		"expected a warning that divergent bucket event sets are unioned",
	)
}

// Two queue consumers with differing batch settings on one handler warn, because
// aws-serverless applies a single SQS batch configuration per function.
func (s *ConsumerTransformTestSuite) Test_diverging_queue_consumer_settings_warn() {
	out := s.transformTwoQueueConsumers(5, 25)
	s.True(
		hasWarningContaining(out.Diagnostics, "only one consumer's settings take effect"),
		"expected a shared-setting conflict warning for divergent queue batch sizes",
	)
}

// Identical settings across two same-kind consumers must NOT warn — the check
// only speaks up on genuine divergence, so it stays quiet for normal use.
func (s *ConsumerTransformTestSuite) Test_matching_queue_consumer_settings_do_not_warn() {
	out := s.transformTwoQueueConsumers(10, 10)
	s.False(
		hasWarningContaining(out.Diagnostics, "only one consumer's settings take effect"),
		"consumers with identical settings must not produce a conflict warning",
	)
}

// transformTwoQueueConsumers absorbs two queue consumers (each on its own queue)
// into a single handler, with the given per-consumer batch sizes.
func (s *ConsumerTransformTestSuite) transformTwoQueueConsumers(batchA, batchB int) *transform.SpecTransformerTransformOutput {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("orderHandler"),
			"handler", core.MappingNodeFromString("handlers.process"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	queueRes := func(role string) *schema.Resource {
		return &schema.Resource{
			Type:         &schema.ResourceTypeWrapper{Value: "celerity/queue"},
			Spec:         core.MappingNodeFields("name", core.MappingNodeFromString(role)),
			LinkSelector: &schema.LinkSelector{ByLabel: &schema.StringMap{Values: map[string]string{"role": role}}},
		}
	}
	consumerRes := func(role string, batch int) *schema.Resource {
		return &schema.Resource{
			Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
			Spec: core.MappingNodeFields("batchSize", core.MappingNodeFromInt(batch)),
			Metadata: &schema.Metadata{
				Labels: &schema.StringMap{Values: map[string]string{"role": role}},
			},
		}
	}

	return s.transformOutput(
		map[string]*schema.Resource{
			"orderHandler": handlerRes,
			"queueA":       queueRes("queue-a"),
			"queueB":       queueRes("queue-b"),
			"consumerA":    consumerRes("queue-a", batchA),
			"consumerB":    consumerRes("queue-b", batchB),
		},
		edges(
			edge("queueA", "consumerA", "celerity/queue", "celerity/consumer"),
			edge("queueB", "consumerB", "celerity/queue", "celerity/consumer"),
			edge("consumerA", "orderHandler", "celerity/consumer", "celerity/handler"),
			edge("consumerB", "orderHandler", "celerity/consumer", "celerity/handler"),
		),
	)
}

// When a consumer matches multiple same-type sources, the disambiguation annotation
// selects the named source (L2).
func (s *ConsumerTransformTestSuite) Test_multiple_queue_sources_honour_disambiguation_annotation() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("orderHandler"),
			"handler", core.MappingNodeFromString("handlers.process"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.consumer", "true"),
		},
	}
	consumerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
		Spec: core.MappingNodeFields("batchSize", core.MappingNodeFromInt(5)),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.consumer.queue", "priorityQueue"),
		},
	}

	out := s.transformOutput(
		map[string]*schema.Resource{
			"orderHandler":   handlerRes,
			"priorityQueue":  queueStub(),
			"standardQueue":  queueStub(),
			"ordersConsumer": consumerRes,
		},
		edges(
			edge("priorityQueue", "ordersConsumer", "celerity/queue", "celerity/consumer"),
			edge("standardQueue", "ordersConsumer", "celerity/queue", "celerity/consumer"),
			edge("ordersConsumer", "orderHandler", "celerity/consumer", "celerity/handler"),
		),
	)

	// The named source is selected, so no ambiguity warning is raised.
	s.False(
		hasWarningContaining(out.Diagnostics, "matches multiple"),
		"a named disambiguation annotation must suppress the ambiguity warning",
	)
}

func queueStub() *schema.Resource {
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("queue")),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"role": "orders-consumer"}},
		},
	}
}

func (s *ConsumerTransformTestSuite) transform(
	resources map[string]*schema.Resource,
	lg linktypes.DeclaredLinkGraph,
) map[string]*schema.Resource {
	return s.transformOutput(resources, lg).TransformedBlueprint.Resources.Values
}

func (s *ConsumerTransformTestSuite) transformOutput(
	resources map[string]*schema.Resource,
	lg linktypes.DeclaredLinkGraph,
) *transform.SpecTransformerTransformOutput {
	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: resources}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          lg,
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}

// --- shared test fakes ---

// annotationMap builds a metadata annotations map from literal key/value pairs.
func annotationMap(kv ...string) *schema.StringOrSubstitutionsMap {
	values := map[string]*substitutions.StringOrSubstitutions{}
	for i := 0; i+1 < len(kv); i += 2 {
		values[kv[i]] = literalAnnotation(kv[i+1])
	}
	return &schema.StringOrSubstitutionsMap{Values: values}
}

// resourceRefName extracts the referenced resource name from a MappingNode holding
// a single ${resources.<name>.<path>} substitution.
func resourceRefName(node *core.MappingNode) string {
	if node == nil || node.StringWithSubstitutions == nil ||
		len(node.StringWithSubstitutions.Values) != 1 {
		return ""
	}
	sub := node.StringWithSubstitutions.Values[0].SubstitutionValue
	if sub == nil || sub.ResourceProperty == nil {
		return ""
	}
	return sub.ResourceProperty.ResourceName
}

// resourceRefNameAndPath extracts the referenced resource name and field-name path
// from a MappingNode holding a single ${resources.<name>.<path>} substitution.
func resourceRefNameAndPath(node *core.MappingNode) (string, []string) {
	if node == nil || node.StringWithSubstitutions == nil ||
		len(node.StringWithSubstitutions.Values) != 1 {
		return "", nil
	}
	sub := node.StringWithSubstitutions.Values[0].SubstitutionValue
	if sub == nil || sub.ResourceProperty == nil {
		return "", nil
	}
	path := []string{}
	for _, item := range sub.ResourceProperty.Path {
		if item.FieldName != "" {
			path = append(path, item.FieldName)
		}
	}
	return sub.ResourceProperty.ResourceName, path
}

type linkEdge struct {
	source     string
	target     string
	sourceType string
	targetType string
}

func edge(source, target, sourceType, targetType string) linkEdge {
	return linkEdge{source: source, target: target, sourceType: sourceType, targetType: targetType}
}

// edgeLinkGraph is a general-purpose DeclaredLinkGraph built from a list of edges.
type edgeLinkGraph struct {
	list []linkEdge
}

func edges(list ...linkEdge) edgeLinkGraph {
	return edgeLinkGraph{list: list}
}

func (g edgeLinkGraph) resolved(filter func(linkEdge) bool) []*linktypes.ResolvedLink {
	links := []*linktypes.ResolvedLink{}
	for _, e := range g.list {
		if filter(e) {
			links = append(links, &linktypes.ResolvedLink{
				Source:     e.source,
				Target:     e.target,
				SourceType: e.sourceType,
				TargetType: e.targetType,
			})
		}
	}
	return links
}

func (g edgeLinkGraph) Edges() []*linktypes.ResolvedLink {
	return g.resolved(func(linkEdge) bool { return true })
}

func (g edgeLinkGraph) EdgesFrom(name string) []*linktypes.ResolvedLink {
	return g.resolved(func(e linkEdge) bool { return e.source == name })
}

func (g edgeLinkGraph) EdgesTo(name string) []*linktypes.ResolvedLink {
	return g.resolved(func(e linkEdge) bool { return e.target == name })
}

func (edgeLinkGraph) Resource(string) (*schema.Resource, linktypes.ResourceClass, bool) {
	return nil, "", false
}
