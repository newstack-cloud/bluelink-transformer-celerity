package handlerawsserverless

import (
	"fmt"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// The aws/apigatewayv2/api::aws/lambda/function link annotation (AppliesTo the
	// function) naming the route this handler serves, e.g. "GET /orders" for HTTP
	// or "$connect"/"myAction" for WebSocket.
	annotationAPIRouteKey = "aws.apigatewayv2.lambda.routeKey"
	// References the authorizer that protects this route.
	annotationAPIAuthorizerID = "aws.apigatewayv2.lambda.authorizerId"
	// "JWT" or "CUSTOM" for the protected route.
	annotationAPIAuthorizationType = "aws.apigatewayv2.lambda.authorizationType"

	// LabelKeyAPITarget names the concrete API an emitted handler function is a
	// route for. Its value is APITargetLabelValue(<abstract api name>, <protocol>),
	// and each per-protocol concrete API selects on this label alone.
	//
	// A single label carrying both parts, rather than one label per part, because
	// a link selector matches a resource that carries ANY one of the selector's
	// labels. Selecting on {application: myApi, protocol: http} therefore matches
	// every handler labelled application=myApi, WebSocket ones included, and a
	// hybrid API's HTTP API tries to build a route with a "$disconnect" route key
	// that API Gateway rejects. One label whose value cannot collide across
	// protocols is what makes the match exact.
	LabelKeyAPITarget = "celerity.internal.api.target"

	// LabelKeyGuardTarget names the API whose custom-guard authorizer invokes an
	// emitted handler function. Its value is GuardTargetLabelValue(<abstract api
	// name>, <guard name>).
	//
	// The provider's authorizer::function link is what grants API Gateway
	// permission to invoke the authorizer's backing function, and that link is
	// activated by a label selector on the authorizer. Without the pair, the
	// authorizer deploys pointing at a function API Gateway may not call, and every
	// route the guard protects fails at the authorizer with the handler never
	// reached.
	LabelKeyGuardTarget = "celerity.internal.api.guard.target"
)

const (
	defaultHTTPMethod   = "GET"
	defaultHTTPPath     = "/"
	defaultWSRoute      = "$default"
	authTypeJWT         = "JWT"
	authTypeCustom      = "CUSTOM"
	guardTypeJWTValue   = "jwt"
	guardTypeCustomVal  = "custom"
	guardSeparatorComma = ","
)

// APITargetLabelValue is the LabelKeyAPITarget value pairing an abstract API
// with one of its protocols. Both the handler emit (stamping the label) and the
// celerity/api emit (selecting on it) build the value here so the two cannot
// drift apart.
func APITargetLabelValue(apiName, protocol string) string {
	return fmt.Sprintf("%s::%s", apiName, protocol)
}

// GuardTargetLabelValue is the LabelKeyGuardTarget value pairing an abstract API
// with one of its custom guards. Both the handler emit (stamping the label) and
// the celerity/api emit (selecting on it) build the value here so the two cannot
// drift apart.
func GuardTargetLabelValue(apiName, guardName string) string {
	return fmt.Sprintf("%s::%s", apiName, guardName)
}

// LambdaFuncResourceName is the concrete function resource name emitted for a
// handler. It is exported so the celerity/api emit can reference a custom-guard
// handler's function when building a REQUEST authorizer.
func LambdaFuncResourceName(handlerName string) string {
	return lambdaFuncResourceName(handlerName)
}

// Stamps the provider aws/apigatewayv2/api::function link route/auth annotations
// onto an HTTP or WebSocket handler's Lambda, derived from the handler's own
// route/guard annotations and the linked API's auth config. A custom-guard handler
// (celerity.handler.guard.custom) is the authorizer target, not a route, so it
// never reaches here (its event source is not http/websocket).
func stampAPIRouteAnnotations(r *handler.ResolvedHandler, lambda *schema.Resource) error {
	if r.EventSource != handler.EventSourceHTTP && r.EventSource != handler.EventSourceWebSocket {
		return nil
	}
	if r.APILink == nil {
		return nil
	}

	// Attach the function to its own protocol's concrete API, and only that one
	// (see LabelKeyAPITarget).
	setLabel(
		lambda.Metadata,
		LabelKeyAPITarget,
		APITargetLabelValue(r.APILink.Name, string(r.EventSource)),
	)

	setStringAnnotation(lambda.Metadata, annotationAPIRouteKey, routeKeyForHandler(r))
	return stampRouteAuth(r, lambda)
}

func routeKeyForHandler(r *handler.ResolvedHandler) string {
	if r.EventSource == handler.EventSourceWebSocket {
		return annotationValue(r.Resource, handler.AnnotationKeyWebSocketRoute, defaultWSRoute)
	}
	method := annotationValue(r.Resource, handler.AnnotationKeyHTTPMethod, defaultHTTPMethod)
	path := annotationValue(r.Resource, handler.AnnotationKeyHTTPPath, defaultHTTPPath)
	return fmt.Sprintf("%s %s", method, path)
}

func stampRouteAuth(r *handler.ResolvedHandler, lambda *schema.Resource) error {
	if r.EventSource == handler.EventSourceWebSocket {
		// WebSocket routes don't use an API Gateway authorizer on aws-serverless:
		// API Gateway v2 WebSocket APIs don't support JWT/REQUEST authorizers (see
		// emitAuthorizers), so Celerity validates WebSocket auth in-message via the
		// authMessage strategy in the handler. Stamping an authorizer reference here
		// would point at an authorizer the API emit never creates (dangling for a
		// WebSocket-only API, cross-API for a hybrid one).
		return nil
	}
	if handler.IsPublic(r) {
		// Public handlers opt out of the API's default guard; leave the route open.
		return nil
	}

	guard, found := effectiveGuard(r)
	if !found {
		return nil
	}

	authType := guardAuthorizationType(r, guard)
	if authType == "" {
		// The handler is protected by a guard that does not resolve to a jwt or
		// custom guard on the linked API. Emitting the route without an authorizer
		// would silently expose it, so fail instead.
		return fmt.Errorf(
			"celerity/handler %q is protected by guard %q, which is not defined as a jwt or custom "+
				"guard on the linked celerity/api; the route cannot be emitted with authorization",
			r.Name, guard,
		)
	}

	authorizerRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf(
			"${resources.%s.spec.authorizerId}",
			apiAuthorizerResourceName(r.APILink.Name, guard),
		),
	)
	if err != nil {
		return err
	}
	lambda.Metadata.Annotations.Values[annotationAPIAuthorizerID] = authorizerRef.StringWithSubstitutions
	setStringAnnotation(lambda.Metadata, annotationAPIAuthorizationType, authType)
	return nil
}

// Resolves the single guard that protects this route: the first guard the handler
// is protectedBy, falling back to the API's defaultGuard. The provider link takes
// one authorizer per route, so only the first guard of a chain is wired on
// aws-serverless.
func effectiveGuard(r *handler.ResolvedHandler) (string, bool) {
	if guard, ok := firstGuard(annotationValue(r.Resource, handler.AnnotationKeyGuardProtectedBy, "")); ok {
		return guard, true
	}
	defaultGuard := core.StringValue(apiSpecNode(r, "$.auth.defaultGuard"))
	return firstGuard(defaultGuard)
}

func firstGuard(value string) (string, bool) {
	for _, raw := range strings.Split(value, guardSeparatorComma) {
		if guard := strings.TrimSpace(raw); guard != "" {
			return guard, true
		}
	}
	return "", false
}

func guardAuthorizationType(r *handler.ResolvedHandler, guard string) string {
	guardType := core.StringValue(apiSpecNode(r, fmt.Sprintf("$.auth.guards.%s.type", guard)))
	switch guardType {
	case guardTypeJWTValue:
		return authTypeJWT
	case guardTypeCustomVal:
		return authTypeCustom
	default:
		return ""
	}
}

func apiSpecNode(r *handler.ResolvedHandler, path string) *core.MappingNode {
	if r.APILink == nil || r.APILink.Resource == nil {
		return nil
	}
	node, _ := pluginutils.GetValueByPath(path, r.APILink.Resource.Spec)
	return node
}

func annotationValue(resource *schema.Resource, key, fallback string) string {
	value, ok := transformutils.GetAnnotation(resource, key, "")
	if !ok {
		return fallback
	}
	if str := core.StringValue(value); str != "" {
		return str
	}
	return fallback
}

// Mirrors the celerity/api emit's authorizer naming; the two conventions must stay
// in sync. Duplicated here to avoid a handler->api import cycle (the api emit
// imports this package for annotation keys).
func apiAuthorizerResourceName(apiName, guard string) string {
	return fmt.Sprintf("%s_%s_authorizer", apiName, guard)
}
