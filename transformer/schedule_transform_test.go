//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type ScheduleTransformTestSuite struct {
	suite.Suite
}

func TestScheduleTransformTestSuite(t *testing.T) {
	suite.Run(t, new(ScheduleTransformTestSuite))
}

// A schedule -> handler chain emits an aws/events/rule targeting the function, with
// the schedule expression and JSON-encoded input carried onto the target.
func (s *ScheduleTransformTestSuite) Test_schedule_emits_events_rule_targeting_the_function() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("reportHandler"),
			"handler", core.MappingNodeFromString("handlers.report"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.schedule", "true"),
		},
	}
	scheduleRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/schedule"},
		Spec: core.MappingNodeFields(
			"schedule", core.MappingNodeFromString("rate(1 day)"),
			"input", core.MappingNodeFields(
				"detailType", core.MappingNodeFromString("report"),
			),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"reportHandler": handlerRes,
			"dailyReport":   scheduleRes,
		},
		edges(edge("dailyReport", "reportHandler", "celerity/schedule", "celerity/handler")),
	)

	rule := resources["dailyReport_events_rule"]
	s.Require().NotNil(rule, "expected an aws/events/rule for the schedule")
	s.Equal("aws/events/rule", rule.Type.Value)
	s.Equal("rate(1 day)", core.StringValue(rule.Spec.Fields["scheduleExpression"]))

	targets := rule.Spec.Fields["targets"]
	s.Require().NotNil(targets)
	s.Require().Len(targets.Items, 1)
	target := targets.Items[0]

	// targets[].arn references the emitted function, which activates the provider's
	// aws/events/rule::aws/lambda/function link (no permission is emitted here).
	s.Equal("reportHandler_lambda_func", resourceRefName(target.Fields["arn"]))
	s.Equal("reportHandler_lambda_func", core.StringValue(target.Fields["id"]))
	s.Equal(`{"detailType":"report"}`, core.StringValue(target.Fields["input"]))

	// The abstract schedule does not survive into the concrete output.
	s.NotContains(resources, "dailyReport")
}

// A schedule with no expression must not emit an events/rule with an empty
// scheduleExpression (invalid on AWS); an error diagnostic is surfaced instead.
func (s *ScheduleTransformTestSuite) Test_schedule_without_expression_errors_and_emits_no_rule() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("reportHandler"),
			"handler", core.MappingNodeFromString("handlers.report"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{Annotations: annotationMap("celerity.handler.schedule", "true")},
	}
	scheduleRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/schedule"},
		Spec: core.MappingNodeFields(
			"input", core.MappingNodeFields("detailType", core.MappingNodeFromString("report")),
		),
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"reportHandler": handlerRes,
		"dailyReport":   scheduleRes,
	}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          edges(edge("dailyReport", "reportHandler", "celerity/schedule", "celerity/handler")),
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelError && strings.Contains(d.Message, "no schedule expression") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic about the missing schedule expression")
	s.NotContains(out.TransformedBlueprint.Resources.Values, "dailyReport_events_rule",
		"no events/rule should be emitted without a schedule expression")
}

func (s *ScheduleTransformTestSuite) transform(
	resources map[string]*schema.Resource,
	lg linktypes.DeclaredLinkGraph,
) map[string]*schema.Resource {
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
	return out.TransformedBlueprint.Resources.Values
}
