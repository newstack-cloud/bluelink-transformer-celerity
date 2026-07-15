package links

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	httpHandlerAnnotationDefKey      = "celerity/handler::" + handler.AnnotationKeyHTTPHandler
	httpMethodAnnotationDefKey       = "celerity/handler::" + handler.AnnotationKeyHTTPMethod
	httpPathAnnotationDefKey         = "celerity/handler::" + handler.AnnotationKeyHTTPPath
	webSocketHandlerAnnotationDefKey = "celerity/handler::" + handler.AnnotationKeyWebSocketHandler
	webSocketRouteAnnotationDefKey   = "celerity/handler::" + handler.AnnotationKeyWebSocketRoute
	guardProtectedByAnnotationDefKey = "celerity/handler::" + handler.AnnotationKeyGuardProtectedBy
	publicAnnotationDefKey           = "celerity/handler::" + handler.AnnotationKeyPublic
	guardCustomAnnotationDefKey      = "celerity/handler::" + handler.AnnotationKeyGuardCustom
)

func APIToHandlerLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/api",
		ResourceTypeB:    "celerity/handler",
		PlainTextSummary: "Routes requests from an API to a handler.",
		FormattedSummary: "Routes requests from a `celerity/api` to a `celerity/handler`.",
		PlainTextDescription: "Attaches a handler to an API so incoming HTTP or WebSocket requests are " +
			"routed to it. The handler's event-source annotation (celerity.handler.http or " +
			"celerity.handler.websocket) selects the protocol it serves; a handler serves one protocol " +
			"and attaches to at most one API.",
		FormattedDescription: "Attaches a handler to an API so incoming HTTP or WebSocket requests are " +
			"routed to it. The handler's event-source annotation (`celerity.handler.http` or " +
			"`celerity.handler.websocket`) selects the protocol it serves; a handler serves one protocol " +
			"and attaches to at most one `celerity/api`.",
		// A handler can have at most one API.
		CardinalityB: provider.LinkCardinality{Min: 0, Max: 1},
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			httpHandlerAnnotationDefKey: {
				Name:        handler.AnnotationKeyHTTPHandler,
				Label:       "HTTP handler",
				Type:        core.ScalarTypeBool,
				AppliesTo:   provider.LinkAnnotationResourceB,
				Description: "Enables the handler to respond to HTTP requests for the linked Celerity API.",
			},
			httpMethodAnnotationDefKey: {
				Name:         handler.AnnotationKeyHTTPMethod,
				Label:        "HTTP method",
				Type:         core.ScalarTypeString,
				AppliesTo:    provider.LinkAnnotationResourceB,
				DefaultValue: core.ScalarFromString("GET"),
				AllowedValues: []*core.ScalarValue{
					core.ScalarFromString("GET"),
					core.ScalarFromString("POST"),
					core.ScalarFromString("PUT"),
					core.ScalarFromString("PATCH"),
					core.ScalarFromString("DELETE"),
					core.ScalarFromString("OPTIONS"),
					core.ScalarFromString("HEAD"),
					core.ScalarFromString("CONNECT"),
					core.ScalarFromString("TRACE"),
				},
				Description: "The HTTP method the handler responds to.",
			},
			httpPathAnnotationDefKey: {
				Name:         handler.AnnotationKeyHTTPPath,
				Label:        "HTTP path",
				Type:         core.ScalarTypeString,
				AppliesTo:    provider.LinkAnnotationResourceB,
				DefaultValue: core.ScalarFromString("/"),
				Examples: []*core.ScalarValue{
					core.ScalarFromString("/orders"),
					core.ScalarFromString("/orders/{order_id}"),
					core.ScalarFromString("/{proxy+}"),
				},
				Description: "The HTTP path the handler responds to. May include path parameters in curly " +
					"braces (for example /orders/{order_id}) and wildcards (for example /{proxy+}).",
			},
			webSocketHandlerAnnotationDefKey: {
				Name:        handler.AnnotationKeyWebSocketHandler,
				Label:       "WebSocket handler",
				Type:        core.ScalarTypeBool,
				AppliesTo:   provider.LinkAnnotationResourceB,
				Description: "Enables the handler to respond to WebSocket messages for the linked Celerity API.",
			},
			webSocketRouteAnnotationDefKey: {
				Name:         handler.AnnotationKeyWebSocketRoute,
				Label:        "WebSocket route",
				Type:         core.ScalarTypeString,
				AppliesTo:    provider.LinkAnnotationResourceB,
				DefaultValue: core.ScalarFromString("$default"),
				Examples: []*core.ScalarValue{
					core.ScalarFromString("$connect"),
					core.ScalarFromString("$disconnect"),
					core.ScalarFromString("myAction"),
				},
				Description: "The route key the handler responds to for WebSocket messages, matched against " +
					"the API's configured routeKey. Includes the predefined $connect, $disconnect and " +
					"$default route keys.",
			},
			guardProtectedByAnnotationDefKey: {
				Name:      handler.AnnotationKeyGuardProtectedBy,
				Label:     "Protected by",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Examples: []*core.ScalarValue{
					core.ScalarFromString("jwt"),
					core.ScalarFromString("jwt,rbac"),
				},
				Description: "The guard, or ordered comma-separated list of guards, that protects this HTTP " +
					"handler. Each guard must be defined on the linked API and all must pass.",
			},
			publicAnnotationDefKey: {
				Name:      handler.AnnotationKeyPublic,
				Label:     "Public",
				Type:      core.ScalarTypeBool,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Marks the HTTP handler as public, opting it out of authentication even when a " +
					"default guard is configured on the linked API.",
			},
			guardCustomAnnotationDefKey: {
				Name:      handler.AnnotationKeyGuardCustom,
				Label:     "Custom guard",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Names the custom auth guard this handler implements to validate incoming " +
					"requests. The guard must be defined on the linked API.",
			},
		},
		ValidateFunc: validateHandlerEventSourceExclusive,
	}
}

func validateHandlerEventSourceExclusive(
	ctx context.Context,
	input *transformerv1.AbstractLinkValidateInput,
) (*transformerv1.AbstractLinkValidateOutput, error) {
	// api->handler: handler is the target
	handlerRes, _, ok := input.LinkGraph.Resource(input.Edge.Target)
	if !ok || handlerRes == nil {
		return &transformerv1.AbstractLinkValidateOutput{}, nil
	}

	_, isHTTP := transformutils.GetAnnotation(handlerRes, handler.AnnotationKeyHTTPHandler, "")
	_, isWS := transformutils.GetAnnotation(handlerRes, handler.AnnotationKeyWebSocketHandler, "")
	if isHTTP && isWS {
		return &transformerv1.AbstractLinkValidateOutput{
			Diagnostics: []*core.Diagnostic{
				{
					Level: core.DiagnosticLevelError,
					Message: fmt.Sprintf(
						"a celerity/handler cannot be both an HTTP and a WebSocket handler; set only one of %s or %s",
						handler.AnnotationKeyHTTPHandler,
						handler.AnnotationKeyWebSocketHandler,
					),
				},
			},
		}, nil
	}

	return &transformerv1.AbstractLinkValidateOutput{}, nil
}
