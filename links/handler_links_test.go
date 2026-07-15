//go:build unit

package links

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/stretchr/testify/suite"
)

type HandlerLinksTestSuite struct {
	suite.Suite
}

// The api->handler link is the only handler link with custom validation logic
// (validateHandlerEventSourceExclusive): a handler may serve HTTP or WebSocket
// but not both. The declarative shape of every link is checked structurally by
// the registry invariant test in package transformer; here we only exercise the
// behaviour.
func (s *HandlerLinksTestSuite) Test_api_handler_event_source_validation() {
	cases := []struct {
		name string
		// resource is the handler at the link target. A nil resource means the
		// target is absent from the link graph.
		resource    *schema.Resource
		expectError bool
	}{
		{
			name: "http and websocket together is rejected",
			resource: handlerWithAnnotations(
				handler.AnnotationKeyHTTPHandler,
				handler.AnnotationKeyWebSocketHandler,
			),
			expectError: true,
		},
		{
			name:     "http only is allowed",
			resource: handlerWithAnnotations(handler.AnnotationKeyHTTPHandler),
		},
		{
			name:     "websocket only is allowed",
			resource: handlerWithAnnotations(handler.AnnotationKeyWebSocketHandler),
		},
		{
			name:     "neither event source set is allowed",
			resource: handlerWithAnnotations(),
		},
		{
			name:     "missing target resource is allowed",
			resource: nil,
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			resources := map[string]*schema.Resource{}
			if tc.resource != nil {
				resources["h1"] = tc.resource
			}

			out, err := APIToHandlerLink().ValidateFunc(
				context.Background(),
				&transformerv1.AbstractLinkValidateInput{
					Edge:      &linktypes.ResolvedLink{Source: "api1", Target: "h1"},
					LinkGraph: &fakeLinkGraph{resources: resources},
				},
			)

			s.Require().NoError(err)
			if tc.expectError {
				s.Require().Len(out.Diagnostics, 1)
				s.Equal(core.DiagnosticLevelError, out.Diagnostics[0].Level)
			} else {
				s.Empty(out.Diagnostics)
			}
		})
	}
}

func TestHandlerLinksTestSuite(t *testing.T) {
	suite.Run(t, new(HandlerLinksTestSuite))
}

func handlerWithAnnotations(keys ...string) *schema.Resource {
	values := map[string]*substitutions.StringOrSubstitutions{}
	for _, key := range keys {
		values[key] = pluginutils.StringToSubstitutions("true")
	}
	return &schema.Resource{
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{Values: values},
		},
	}
}

type fakeLinkGraph struct {
	resources map[string]*schema.Resource
}

func (g *fakeLinkGraph) Edges() []*linktypes.ResolvedLink           { return nil }
func (g *fakeLinkGraph) EdgesFrom(string) []*linktypes.ResolvedLink { return nil }
func (g *fakeLinkGraph) EdgesTo(string) []*linktypes.ResolvedLink   { return nil }

func (g *fakeLinkGraph) Resource(name string) (*schema.Resource, linktypes.ResourceClass, bool) {
	resource, ok := g.resources[name]
	return resource, "", ok
}
