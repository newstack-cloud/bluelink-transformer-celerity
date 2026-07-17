//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type QueueTransformTestSuite struct {
	suite.Suite
}

func TestQueueTransformTestSuite(t *testing.T) {
	suite.Run(t, new(QueueTransformTestSuite))
}

func (s *QueueTransformTestSuite) Test_emits_an_sqs_queue_from_the_queue_spec() {
	q := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"visibilityTimeout", core.MappingNodeFromInt(45),
			"encryptionKeyId", core.MappingNodeFromString("alias/orders-key"),
		),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"team": "payments"}},
		},
	}

	resources := s.transform(map[string]*schema.Resource{"myQueue": q})

	sqs, ok := resources["myQueue_sqs_queue"]
	s.Require().True(ok, "expected an aws/sqs/queue for the queue")
	s.Equal("aws/sqs/queue", sqs.Type.Value)
	s.Equal("orders", core.StringValue(sqs.Spec.Fields["queueName"]))
	s.Equal(45, core.IntValue(sqs.Spec.Fields["visibilityTimeout"]))
	s.Equal("alias/orders-key", core.StringValue(sqs.Spec.Fields["kmsMasterKeyId"]))
	// A standard (non-fifo) queue does not set fifoQueue.
	s.Nil(sqs.Spec.Fields["fifoQueue"])

	// tags is a LIST of {key, value} for aws/sqs/queue.
	tags := sqs.Spec.Fields["tags"]
	s.Require().NotNil(tags)
	s.Require().Len(tags.Items, 1)
	s.Equal("team", core.StringValue(tags.Items[0].Fields["key"]))
	s.Equal("payments", core.StringValue(tags.Items[0].Fields["value"]))

	// Labels are preserved so a handler's linkSelector can match the queue.
	s.Require().NotNil(sqs.Metadata.Labels)
	s.Equal("payments", sqs.Metadata.Labels.Values["team"])

	// Framework annotations, infrastructure category.
	s.Equal("celerity/queue", annotationLiteral(sqs.Metadata.Annotations, transformutils.AnnotationSourceAbstractType))
	s.Equal("infrastructure", annotationLiteral(sqs.Metadata.Annotations, transformutils.AnnotationResourceCategory))
}

func (s *QueueTransformTestSuite) Test_fifo_queue_appends_the_fifo_suffix() {
	q := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"fifo", core.MappingNodeFromBool(true),
		),
	}

	resources := s.transform(map[string]*schema.Resource{"myQueue": q})

	sqs := resources["myQueue_sqs_queue"]
	s.Require().NotNil(sqs)
	s.True(core.BoolValue(sqs.Spec.Fields["fifoQueue"]))
	s.Equal("orders.fifo", core.StringValue(sqs.Spec.Fields["queueName"]),
		"a fifo queue name must carry the .fifo suffix")
}

func (s *QueueTransformTestSuite) Test_dead_letter_parent_preserves_linkSelector_and_maps_redrive() {
	// A parent queue points at its DLQ via linkSelector and sets the celerity
	// dead-letter annotation; the concrete queue must keep the linkSelector and
	// carry the provider redrive annotation.
	parent := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
		),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"role": "orders-dlq"}},
		},
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{
				Values: map[string]*substitutions.StringOrSubstitutions{
					"celerity.queue.deadLetterMaxAttempts": literalAnnotation("5"),
				},
			},
		},
	}
	dlq := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders-dlq")),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"role": "orders-dlq"}},
		},
	}

	resources := s.transform(map[string]*schema.Resource{
		"parentQueue": parent,
		"dlqQueue":    dlq,
	})

	src := resources["parentQueue_sqs_queue"]
	s.Require().NotNil(src)
	// linkSelector preserved so the aws/sqs/queue::aws/sqs/queue link resolves.
	s.Require().NotNil(src.LinkSelector)
	s.Require().NotNil(src.LinkSelector.ByLabel)
	s.Equal("orders-dlq", src.LinkSelector.ByLabel.Values["role"])
	// celerity annotation re-keyed to the provider redrive annotation, value verbatim.
	s.Equal("5", annotationLiteral(src.Metadata.Annotations, "aws.sqs.redrive.maxReceiveCount"))

	// The DLQ carries the label the parent selects on.
	dlqRes := resources["dlqQueue_sqs_queue"]
	s.Require().NotNil(dlqRes)
	s.Equal("orders-dlq", dlqRes.Metadata.Labels.Values["role"])
}

func (s *QueueTransformTestSuite) Test_handler_linkSelector_is_preserved_onto_the_lambda() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("myHandler"),
			"handler", core.MappingNodeFromString("handlers.save"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"app": "orders"}},
		},
	}
	q := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}},
		},
	}

	resources := s.transform(map[string]*schema.Resource{
		"myHandler": handlerRes,
		"myQueue":   q,
	})

	lambda := resources["myHandler_lambda_func"]
	s.Require().NotNil(lambda)
	// The Lambda inherits the handler's linkSelector so the provider's
	// function::queue link resolves against the concrete queue by label.
	s.Require().NotNil(lambda.LinkSelector)
	s.Require().NotNil(lambda.LinkSelector.ByLabel)
	s.Equal("orders", lambda.LinkSelector.ByLabel.Values["app"])

	// And the concrete queue carries the matching label.
	s.Equal("orders", resources["myQueue_sqs_queue"].Metadata.Labels.Values["app"])
}

func (s *QueueTransformTestSuite) Test_deploy_config_sets_retention_and_max_size() {
	q := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
	}

	// Global retention; per-queue max size (keyed by spec.name "orders").
	ctx := deployConfigContext(map[string]*core.ScalarValue{
		"aws.sqs.messageRetentionPeriod": core.ScalarFromInt(600),
		"aws.sqs.orders.maxMessageSize":  core.ScalarFromInt(1024),
	})

	resources := s.transformWith(map[string]*schema.Resource{"myQueue": q}, ctx)
	sqs := resources["myQueue_sqs_queue"]
	s.Require().NotNil(sqs)
	s.Equal(600, core.IntValue(sqs.Spec.Fields["messageRetentionPeriod"]))
	s.Equal(1024, core.IntValue(sqs.Spec.Fields["maximumMessageSize"]))
}

// A queue receiving bucket notifications maps its celerity.queue.bucket.* config
// onto the provider aws.s3.sqs.* annotations the aws/s3/bucket::aws/sqs/queue link
// consumes: one aws.s3.sqs.event.<index> per mapped event, plus filter prefix/suffix.
func (s *QueueTransformTestSuite) Test_bucket_notification_events_and_filters_are_stamped() {
	q := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{
				Values: map[string]*substitutions.StringOrSubstitutions{
					"celerity.queue.bucket.events":       literalAnnotation("created,deleted"),
					"celerity.queue.bucket.filterPrefix": literalAnnotation("incoming/"),
					"celerity.queue.bucket.filterSuffix": literalAnnotation(".json"),
				},
			},
		},
	}

	resources := s.transform(map[string]*schema.Resource{"myQueue": q})
	sqs := resources["myQueue_sqs_queue"]
	s.Require().NotNil(sqs)

	s.Equal("s3:ObjectCreated:*", annotationLiteral(sqs.Metadata.Annotations, "aws.s3.sqs.event.0"))
	s.Equal("s3:ObjectRemoved:*", annotationLiteral(sqs.Metadata.Annotations, "aws.s3.sqs.event.1"))
	s.Equal("incoming/", annotationLiteral(sqs.Metadata.Annotations, "aws.s3.sqs.filterPrefix"))
	s.Equal(".json", annotationLiteral(sqs.Metadata.Annotations, "aws.s3.sqs.filterSuffix"))
}

// A queue with no bucket-notification annotations stamps no aws.s3.sqs.* keys, so
// the provider applies its default event set.
func (s *QueueTransformTestSuite) Test_no_bucket_notification_config_stamps_nothing() {
	q := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
	}

	resources := s.transform(map[string]*schema.Resource{"myQueue": q})
	sqs := resources["myQueue_sqs_queue"]
	s.Require().NotNil(sqs)
	s.Nil(sqs.Metadata.Annotations.Values["aws.s3.sqs.event.0"])
	s.Nil(sqs.Metadata.Annotations.Values["aws.s3.sqs.filterPrefix"])
}

// An unmappable bucket event (metadataUpdated) is not stamped and surfaces a
// warning rather than being silently dropped.
func (s *QueueTransformTestSuite) Test_unsupported_bucket_event_warns() {
	q := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{
				Values: map[string]*substitutions.StringOrSubstitutions{
					"celerity.queue.bucket.events": literalAnnotation("created,metadataUpdated"),
				},
			},
		},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{"myQueue": q}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)

	sqs := out.TransformedBlueprint.Resources.Values["myQueue_sqs_queue"]
	s.Require().NotNil(sqs)
	// created is still stamped at index 0; metadataUpdated is dropped, not stamped.
	s.Equal("s3:ObjectCreated:*", annotationLiteral(sqs.Metadata.Annotations, "aws.s3.sqs.event.0"))
	s.Nil(sqs.Metadata.Annotations.Values["aws.s3.sqs.event.1"])

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelWarning && strings.Contains(d.Message, "metadataUpdated") {
			found = true
		}
	}
	s.True(found, "expected a warning about the unmappable metadataUpdated event")
}

func (s *QueueTransformTestSuite) transform(
	resources map[string]*schema.Resource,
) map[string]*schema.Resource {
	return s.transformWith(resources, validationContext())
}

func (s *QueueTransformTestSuite) transformWith(
	resources map[string]*schema.Resource,
	ctx transform.Context,
) map[string]*schema.Resource {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{Values: resources},
	}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: ctx,
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out.TransformedBlueprint.Resources.Values
}

// deployConfigContext is a transform context carrying deploy-config variables
// (plus the standard validation + deploy-target context vars).
func deployConfigContext(configVars map[string]*core.ScalarValue) transform.Context {
	return &fakeTransformContext{
		configVars: configVars,
		contextVars: map[string]*core.ScalarValue{
			core.ValidationContextVariableName: core.ScalarFromBool(true),
			"deployTarget":                     core.ScalarFromString(shared.AWSServerless),
		},
	}
}

func literalAnnotation(value string) *substitutions.StringOrSubstitutions {
	return &substitutions.StringOrSubstitutions{
		Values: []*substitutions.StringOrSubstitution{
			{StringValue: &value},
		},
	}
}
