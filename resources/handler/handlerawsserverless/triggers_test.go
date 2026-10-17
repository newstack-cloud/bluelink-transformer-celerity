//go:build unit

package handlerawsserverless

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TriggersTestSuite struct {
	suite.Suite
}

func TestTriggersTestSuite(t *testing.T) {
	suite.Run(t, new(TriggersTestSuite))
}

// Residual 1: an in-blueprint celerity/topic consumer's SNS subscription must
// have its topicArn rewritten from the abstract topic reference to the concrete
// <topic>_sns_topic, exactly like the Lambda spec is rewritten.
//
// The subscription itself is a shared parent (one fan-out per consumer, however
// many handlers absorb it), so the handler contributes the topicArn rather than
// emitting the resource.
func (s *TriggersTestSuite) Test_emit_rewrites_in_blueprint_topic_subscription_topicArn() {
	topicSourceID := mustSubNode(s.T(), "${resources.ordersTopic.spec.id}")
	r := topicConsumerHandler("orderHandler", "orderConsumer", topicSourceID)

	// The chained rewriter the pipeline hands the emitter carries every primary's
	// rewriter, including the topic's (spec.id -> concrete spec.topicArn).
	topicPM := transformutils.PropertyMap{
		Renames: map[string][]string{"spec.id": {"spec", "topicArn"}},
	}
	rw := topicPM.Rewriter("ordersTopic", "ordersTopic_sns_topic")

	result := emitForTest(s.T(), r, rw)

	contribution := result.SharedParentContributions["topicSubscription:orderConsumer"]
	s.Require().NotNil(contribution, "expected a topic subscription contribution for the consumer")

	name, path := resourcePropertyRef(s.T(), contribution.Fields["topicArn"])
	s.Equal("ordersTopic_sns_topic", name,
		"subscription topicArn must reference the concrete SNS topic, not the abstract name")
	s.Equal([]string{"spec", "topicArn"}, path)
}

// A literal celerity::topic::<arn> source has no in-blueprint reference to rewrite;
// the topicArn must survive the rewrite pass unchanged as a literal.
func (s *TriggersTestSuite) Test_emit_leaves_literal_topic_subscription_arn_unchanged() {
	r := topicConsumerHandler("orderHandler", "orderConsumer",
		core.MappingNodeFromString("arn:aws:sns:us-east-1:123456789012:orders"))

	rw := func(*substitutions.SubstitutionResourceProperty) *substitutions.Substitution {
		return nil
	}

	result := emitForTest(s.T(), r, rw)

	contribution := result.SharedParentContributions["topicSubscription:orderConsumer"]
	s.Require().NotNil(contribution)
	s.Equal(
		"arn:aws:sns:us-east-1:123456789012:orders",
		core.StringValue(contribution.Fields["topicArn"]),
	)
}

// Residual 2: the handler contributes a chained rewriter mapping a topic
// consumer's ${<consumer>.spec.subscriberId} to the concrete fan-out queue.
func (s *TriggersTestSuite) Test_consumerSubscriberRewriters_resolves_subscriberId_to_topic_queue() {
	r := &handler.ResolvedHandler{
		ConsumerBindings: []*handler.ConsumerBinding{
			{ConsumerName: "orderConsumer", SourceKind: handler.ConsumerSourceTopic},
		},
	}

	rewriters := consumerSubscriberRewriters(r)
	s.Require().Len(rewriters, 1)

	sub := rewriters[0](subscriberIDRef("orderConsumer"))
	s.Require().NotNil(sub)
	s.Require().NotNil(sub.ResourceProperty)
	s.Equal("orderConsumer_topic_queue", sub.ResourceProperty.ResourceName)
	s.Equal([]string{"spec", "arn"}, refPath(sub.ResourceProperty))
}

func (s *TriggersTestSuite) Test_consumerSubscriberRewriters_ignores_non_topic_consumers() {
	r := &handler.ResolvedHandler{
		ConsumerBindings: []*handler.ConsumerBinding{
			{ConsumerName: "queueConsumer", SourceKind: handler.ConsumerSourceQueue},
			{ConsumerName: "streamConsumer", SourceKind: handler.ConsumerSourceExternal},
		},
	}

	s.Empty(consumerSubscriberRewriters(r))
}

func (s *TriggersTestSuite) Test_subscriberIDRewriter_leaves_unrelated_refs_untouched() {
	rewriter := subscriberIDRewriter("orderConsumer")

	// Wrong resource name.
	s.Nil(rewriter(subscriberIDRef("otherConsumer")))
	// Right resource, wrong property.
	otherProp := &substitutions.SubstitutionResourceProperty{
		ResourceName: "orderConsumer",
		Path: []*substitutions.SubstitutionPathItem{
			{FieldName: "spec"}, {FieldName: "sourceId"},
		},
	}
	s.Nil(rewriter(otherProp))
}

// Residual 3: external-ARN consumers contribute scoped source-read grants to the
// handler's role plan (which the seed turns into policy statements).
func (s *TriggersTestSuite) Test_buildAWSRolePlan_collects_external_consumer_sources() {
	r := &handler.ResolvedHandler{
		Name: "orderHandler",
		ConsumerBindings: []*handler.ConsumerBinding{
			{
				ConsumerName: "streamConsumer",
				SourceKind:   handler.ConsumerSourceExternal,
				ExternalStreams: []*handler.ExternalStreamBinding{
					{
						Key:        "orders",
						SourceType: "dbStream",
						EventSourceArn: core.MappingNodeFromString(
							"arn:aws:dynamodb:us-east-1:123456789012:table/orders/stream/2024",
						),
					},
					{
						Key:        "clicks",
						SourceType: "dataStream",
						EventSourceArn: core.MappingNodeFromString(
							"arn:aws:kinesis:us-east-1:123456789012:stream/clicks",
						),
					},
				},
			},
			{
				ConsumerName: "sqsConsumer",
				SourceKind:   handler.ConsumerSourceExternal,
				// A raw SQS queue URL must be converted to an ARN for the IAM resource.
				ExternalSQSArn: core.MappingNodeFromString(
					"https://sqs.us-east-1.amazonaws.com/123456789012/ext-queue",
				),
			},
		},
	}

	plan := RolePlanFor(r)
	s.Require().Len(plan.ExternalSources, 3)

	// Sorted by service then ARN: dynamodb-stream, kinesis-stream, sqs.
	s.Equal("dynamodb-stream", plan.ExternalSources[0].Service)
	s.Equal(
		"arn:aws:dynamodb:us-east-1:123456789012:table/orders/stream/2024",
		plan.ExternalSources[0].ARN,
	)
	s.Equal("kinesis-stream", plan.ExternalSources[1].Service)
	s.Equal("sqs", plan.ExternalSources[2].Service)
	s.Equal(
		"arn:aws:sqs:us-east-1:123456789012:ext-queue",
		plan.ExternalSources[2].ARN,
		"an external SQS queue URL should be converted to its ARN form",
	)
}

func (s *TriggersTestSuite) Test_sqsURLToARN_converts_url_and_leaves_arn_untouched() {
	arn, ok := sqsURLToARN("https://sqs.eu-west-2.amazonaws.com/999988887777/my-queue")
	s.Require().True(ok)
	s.Equal("arn:aws:sqs:eu-west-2:999988887777:my-queue", arn)

	_, ok = sqsURLToARN("arn:aws:sqs:us-east-1:123456789012:already-arn")
	s.False(ok, "an ARN is not a URL and should not be converted")
}

// --- helpers ---

func topicConsumerHandler(handlerName, consumerName string, topicARN *core.MappingNode) *handler.ResolvedHandler {
	return &handler.ResolvedHandler{
		Name: handlerName,
		Resource: &schema.Resource{
			Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
			Spec: core.MappingNodeFields(
				"handlerName", core.MappingNodeFromString(handlerName),
				"handler", core.MappingNodeFromString("handlers.order"),
				"runtime", core.MappingNodeFromString("nodejs24.x"),
			),
		},
		EventSource: handler.EventSourceConsumer,
		ConsumerBindings: []*handler.ConsumerBinding{
			{
				ConsumerName: consumerName,
				// A resolved binding always carries its abstract consumer resource;
				// topicFanoutQueue reads its annotations (deadLetterQueue, batchSize).
				ConsumerResource: &schema.Resource{
					Type: &schema.ResourceTypeWrapper{Value: "celerity/consumer"},
					Spec: &core.MappingNode{Fields: map[string]*core.MappingNode{}},
				},
				SourceKind: handler.ConsumerSourceTopic,
				TopicARN:   topicARN,
			},
		},
	}
}

func emitForTest(
	t *testing.T,
	r *handler.ResolvedHandler,
	rw transformutils.ResourcePropertyRewriter,
) *transformutils.EmitResult {
	t.Helper()
	emitter := newEmitter(&shared.Dependencies{})
	run := &transformutils.Run{TransformContext: triggerValidationContext()}
	result, err := emitter.emit(context.Background(), run, r, rw)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func subscriberIDRef(consumerName string) *substitutions.SubstitutionResourceProperty {
	return &substitutions.SubstitutionResourceProperty{
		ResourceName: consumerName,
		Path: []*substitutions.SubstitutionPathItem{
			{FieldName: "spec"}, {FieldName: "subscriberId"},
		},
	}
}

func refPath(ref *substitutions.SubstitutionResourceProperty) []string {
	path := make([]string, 0, len(ref.Path))
	for _, item := range ref.Path {
		if item.FieldName != "" {
			path = append(path, item.FieldName)
		}
	}
	return path
}

func resourcePropertyRef(t *testing.T, node *core.MappingNode) (string, []string) {
	t.Helper()
	require.NotNil(t, node)
	require.NotNil(t, node.StringWithSubstitutions)
	require.Len(t, node.StringWithSubstitutions.Values, 1)
	sub := node.StringWithSubstitutions.Values[0].SubstitutionValue
	require.NotNil(t, sub)
	require.NotNil(t, sub.ResourceProperty)
	return sub.ResourceProperty.ResourceName, refPath(sub.ResourceProperty)
}

func mustSubNode(t *testing.T, expr string) *core.MappingNode {
	t.Helper()
	node, err := shared.SubstitutionMappingNode(expr)
	require.NoError(t, err)
	return node
}

type triggerFakeTransformContext struct {
	contextVars map[string]*core.ScalarValue
}

func triggerValidationContext() transform.Context {
	return &triggerFakeTransformContext{
		contextVars: map[string]*core.ScalarValue{
			core.ValidationContextVariableName: core.ScalarFromBool(true),
			"deployTarget":                     core.ScalarFromString(shared.AWSServerless),
		},
	}
}

func (f *triggerFakeTransformContext) TransformerConfigVariable(string) (*core.ScalarValue, bool) {
	return nil, false
}

func (f *triggerFakeTransformContext) TransformerConfigVariables() map[string]*core.ScalarValue {
	return nil
}

func (f *triggerFakeTransformContext) ContextVariable(name string) (*core.ScalarValue, bool) {
	v, ok := f.contextVars[name]
	return v, ok
}

func (f *triggerFakeTransformContext) ContextVariables() map[string]*core.ScalarValue {
	return f.contextVars
}
