//go:build unit

package transformer

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type TopicTransformTestSuite struct {
	suite.Suite
}

func TestTopicTransformTestSuite(t *testing.T) {
	suite.Run(t, new(TopicTransformTestSuite))
}

func (s *TopicTransformTestSuite) Test_emits_an_sns_topic_from_the_topic_spec() {
	tp := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/topic"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("events"),
			"encryptionKeyId", core.MappingNodeFromString("alias/events-key"),
		),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"team": "payments"}},
		},
	}

	resources := s.transformTopic(map[string]*schema.Resource{"myTopic": tp})

	sns, ok := resources["myTopic_sns_topic"]
	s.Require().True(ok, "expected an aws/sns/topic for the topic")
	s.Equal("aws/sns/topic", sns.Type.Value)
	s.Equal("events", core.StringValue(sns.Spec.Fields["topicName"]))
	s.Equal("alias/events-key", core.StringValue(sns.Spec.Fields["kmsMasterKeyId"]))
	s.Nil(sns.Spec.Fields["fifoTopic"], "a standard topic does not set fifoTopic")
	// The spec explicitly forbids contentBasedDeduplication for FIFO topics.
	s.Nil(sns.Spec.Fields["contentBasedDeduplication"])

	// tags is a LIST of {key, value} for aws/sns/topic.
	tags := sns.Spec.Fields["tags"]
	s.Require().NotNil(tags)
	s.Require().Len(tags.Items, 1)
	s.Equal("team", core.StringValue(tags.Items[0].Fields["key"]))

	// Labels preserved so a handler's linkSelector can match the topic.
	s.Equal("payments", sns.Metadata.Labels.Values["team"])

	s.Equal("celerity/topic", annotationLiteral(sns.Metadata.Annotations, transformutils.AnnotationSourceAbstractType))
	s.Equal("infrastructure", annotationLiteral(sns.Metadata.Annotations, transformutils.AnnotationResourceCategory))
}

func (s *TopicTransformTestSuite) Test_fifo_topic_appends_the_fifo_suffix() {
	tp := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/topic"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("events"),
			"fifo", core.MappingNodeFromBool(true),
		),
	}

	resources := s.transformTopic(map[string]*schema.Resource{"myTopic": tp})

	sns := resources["myTopic_sns_topic"]
	s.Require().NotNil(sns)
	s.True(core.BoolValue(sns.Spec.Fields["fifoTopic"]))
	s.Equal("events.fifo", core.StringValue(sns.Spec.Fields["topicName"]))
}

func (s *TopicTransformTestSuite) transformTopic(
	resources map[string]*schema.Resource,
) map[string]*schema.Resource {
	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: resources}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out.TransformedBlueprint.Resources.Values
}
