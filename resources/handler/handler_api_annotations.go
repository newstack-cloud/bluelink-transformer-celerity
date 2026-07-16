package handler

import (
	"fmt"
	"strings"

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
	annAPIRouteKey = "aws.apigatewayv2.lambda.routeKey"
	// References the authorizer that protects this route.
	annAPIAuthorizerID = "aws.apigatewayv2.lambda.authorizerId"
	// "JWT" or "CUSTOM" for the protected route.
	annAPIAuthorizationType = "aws.apigatewayv2.lambda.authorizationType"
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
func stampAPIRouteAnnotations(r *ResolvedHandler, lambda *schema.Resource) error {
	if r.EventSource != EventSourceHTTP && r.EventSource != EventSourceWebSocket {
		return nil
	}
	if r.APILink == nil {
		return nil
	}

	setStringAnnotation(lambda.Metadata, annAPIRouteKey, routeKeyForHandler(r))
	return stampRouteAuth(r, lambda)
}

func routeKeyForHandler(r *ResolvedHandler) string {
	if r.EventSource == EventSourceWebSocket {
		return annotationValue(r.Resource, AnnotationKeyWebSocketRoute, defaultWSRoute)
	}
	method := annotationValue(r.Resource, AnnotationKeyHTTPMethod, defaultHTTPMethod)
	path := annotationValue(r.Resource, AnnotationKeyHTTPPath, defaultHTTPPath)
	return fmt.Sprintf("%s %s", method, path)
}

func stampRouteAuth(r *ResolvedHandler, lambda *schema.Resource) error {
	if isPublicHandler(r) {
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
	lambda.Metadata.Annotations.Values[annAPIAuthorizerID] = authorizerRef.StringWithSubstitutions
	setStringAnnotation(lambda.Metadata, annAPIAuthorizationType, authType)
	return nil
}

func isPublicHandler(r *ResolvedHandler) bool {
	value, ok := transformutils.GetAnnotation(r.Resource, AnnotationKeyPublic, "")
	return ok && core.BoolValue(value)
}

// Resolves the single guard that protects this route: the first guard the handler
// is protectedBy, falling back to the API's defaultGuard. The provider link takes
// one authorizer per route, so only the first guard of a chain is wired on
// aws-serverless.
func effectiveGuard(r *ResolvedHandler) (string, bool) {
	if guard, ok := firstGuard(annotationValue(r.Resource, AnnotationKeyGuardProtectedBy, "")); ok {
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

func guardAuthorizationType(r *ResolvedHandler, guard string) string {
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

func apiSpecNode(r *ResolvedHandler, path string) *core.MappingNode {
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
