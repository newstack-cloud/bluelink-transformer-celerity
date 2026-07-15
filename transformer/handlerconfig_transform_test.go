//go:build unit

package transformer

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type HandlerConfigTransformTestSuite struct {
	suite.Suite
}

func TestHandlerConfigTransformTestSuite(t *testing.T) {
	suite.Run(t, new(HandlerConfigTransformTestSuite))
}

// celerity/handlerConfig is contributory-only: the handler inherits its fields
// during resolve, its emitter is a no-op, and the abstract resource must not
// survive into the transformed (concrete) blueprint.
func (s *HandlerConfigTransformTestSuite) Test_handler_config_is_stripped_and_its_fields_inherited() {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myHandler": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
					Spec: core.MappingNodeFields(
						"handlerName", core.MappingNodeFromString("myHandler"),
						"handler", core.MappingNodeFromString("handlers.save"),
						// no runtime, no memory -> both inherited from the handlerConfig.
					),
				},
				"sharedCfg": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/handlerConfig"},
					Spec: core.MappingNodeFields(
						"runtime", core.MappingNodeFromString("nodejs24.x"),
						"memory", core.MappingNodeFromInt(512),
					),
				},
			},
		},
	}

	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          handlerConfigLinkGraph{handler: "myHandler", config: "sharedCfg"},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)

	resources := out.TransformedBlueprint.Resources.Values

	// Stripped: the abstract handlerConfig is consumed for inheritance and left
	// out of the concrete output entirely.
	s.NotContains(resources, "sharedCfg",
		"celerity/handlerConfig must not survive into the output blueprint")
	for name, res := range resources {
		s.NotEqual("celerity/handlerConfig", res.Type.Value,
			"no celerity/handlerConfig resource should remain in output (found %q)", name)
	}

	// Inherited: the handler still emits its lambda, carrying the fields it did
	// not set itself.
	lambda, ok := resources["myHandler_lambda_func"]
	s.Require().True(ok, "expected the handler to emit its lambda function")
	s.Equal("aws/lambda/function", lambda.Type.Value)
	// memory (inherited) -> memorySize on the lambda.
	s.Equal(512, core.IntValue(lambda.Spec.Fields["memorySize"]))
	// runtime (inherited) resolved to a non-empty concrete runtime; without
	// inheritance the handler would fail to resolve a runtime at all.
	s.NotEmpty(core.StringValue(lambda.Spec.Fields["runtime"]))
}

// handlerConfigLinkGraph is a minimal DeclaredLinkGraph with a single
// handler -> handlerConfig edge.
type handlerConfigLinkGraph struct {
	handler string
	config  string
}

func (g handlerConfigLinkGraph) Edges() []*linktypes.ResolvedLink {
	return []*linktypes.ResolvedLink{g.edge()}
}

func (g handlerConfigLinkGraph) EdgesFrom(name string) []*linktypes.ResolvedLink {
	if name == g.handler {
		return []*linktypes.ResolvedLink{g.edge()}
	}
	return nil
}

func (g handlerConfigLinkGraph) EdgesTo(name string) []*linktypes.ResolvedLink {
	if name == g.config {
		return []*linktypes.ResolvedLink{g.edge()}
	}
	return nil
}

func (handlerConfigLinkGraph) Resource(string) (*schema.Resource, linktypes.ResourceClass, bool) {
	return nil, "", false
}

func (g handlerConfigLinkGraph) edge() *linktypes.ResolvedLink {
	return &linktypes.ResolvedLink{
		Source:     g.handler,
		Target:     g.config,
		SourceType: "celerity/handler",
		TargetType: "celerity/handlerConfig",
	}
}
