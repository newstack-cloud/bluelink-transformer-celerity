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

type APITransformTestSuite struct {
	suite.Suite
}

func TestAPITransformTestSuite(t *testing.T) {
	suite.Run(t, new(APITransformTestSuite))
}

// An http-only API emits a single HTTP API Gateway plus its stage, and the
// concrete API preserves the abstract API's linkSelector so the provider's
// api::function link (activated by a label selector on the source) resolves.
func (s *APITransformTestSuite) Test_http_only_emits_api_and_stage() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
		),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"application": "orders"}},
		},
	}

	resources := s.transform(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	api := resources["ordersApi_http_api"]
	s.Require().NotNil(api)
	s.Equal("aws/apigatewayv2/api", api.Type.Value)
	s.Equal("HTTP", core.StringValue(api.Spec.Fields["protocolType"]))

	// The concrete API preserves the abstract API's linkSelector.
	s.Require().NotNil(api.LinkSelector)
	s.Equal("orders", api.LinkSelector.ByLabel.Values["application"])

	stage := resources["ordersApi_http_stage"]
	s.Require().NotNil(stage)
	s.Equal("aws/apigatewayv2/stage", stage.Type.Value)
	s.Equal("$default", core.StringValue(stage.Spec.Fields["stageName"]))
	s.True(core.BoolValue(stage.Spec.Fields["autoDeploy"]))
	s.Equal("ordersApi_http_api", resourceRefName(stage.Spec.Fields["apiId"]))

	// No WebSocket API for an http-only declaration.
	s.NotContains(resources, "ordersApi_websocket_api")
}

// A hybrid API (http + websocket) emits two API Gateways and two stages, with the
// WebSocket routeSelectionExpression derived from the websocketConfig routeKey.
func (s *APITransformTestSuite) Test_hybrid_emits_two_apis() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(
				core.MappingNodeFromString("http"),
				core.MappingNodeFields(
					"websocketConfig", core.MappingNodeFields(
						"routeKey", core.MappingNodeFromString("action"),
					),
				),
			),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{"chatApi": apiRes},
		edges(),
	)

	s.Require().NotNil(resources["chatApi_http_api"])
	s.Require().NotNil(resources["chatApi_http_stage"])

	ws := resources["chatApi_websocket_api"]
	s.Require().NotNil(ws)
	s.Equal("WEBSOCKET", core.StringValue(ws.Spec.Fields["protocolType"]))
	s.Equal("$request.body.action", core.StringValue(ws.Spec.Fields["routeSelectionExpression"]))
	s.Require().NotNil(resources["chatApi_websocket_stage"])
}

// A hybrid API's two concrete APIs each scope their linkSelector to their own
// protocol (in addition to the author's labels), so the WebSocket API never
// attaches an HTTP handler (which would create an invalid route) and vice versa.
func (s *APITransformTestSuite) Test_hybrid_apis_are_protocol_scoped() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(
				core.MappingNodeFromString("http"),
				core.MappingNodeFields(
					"websocketConfig", core.MappingNodeFields(
						"routeKey", core.MappingNodeFromString("action"),
					),
				),
			),
		),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"application": "chat"}},
		},
	}

	resources := s.transform(map[string]*schema.Resource{"chatApi": apiRes}, edges())

	httpAPI := resources["chatApi_http_api"]
	s.Require().NotNil(httpAPI)
	s.Require().NotNil(httpAPI.LinkSelector)
	s.Equal("chat", httpAPI.LinkSelector.ByLabel.Values["application"])
	s.Equal("http", httpAPI.LinkSelector.ByLabel.Values["celerity.internal.api.protocol"])

	ws := resources["chatApi_websocket_api"]
	s.Require().NotNil(ws)
	s.Require().NotNil(ws.LinkSelector)
	s.Equal("chat", ws.LinkSelector.ByLabel.Values["application"])
	s.Equal("websocket", ws.LinkSelector.ByLabel.Values["celerity.internal.api.protocol"])
}

// A JWT guard emits an aws/apigatewayv2/authorizer with the issuer, audience and
// an identitySource derived from the guard's tokenSource.
func (s *APITransformTestSuite) Test_jwt_guard_emits_authorizer() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"auth", core.MappingNodeFields(
				"defaultGuard", core.MappingNodeFromString("jwt"),
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
						"issuer", core.MappingNodeFromString("https://identity.example.com/oauth2/v1/"),
						"tokenSource", core.MappingNodeFromString("$.headers.Authorization"),
						"audience", core.MappingNodeItems(
							core.MappingNodeFromString("https://identity.example.com/api/v1/"),
						),
					),
				),
			),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	authorizer := resources["ordersApi_jwt_authorizer"]
	s.Require().NotNil(authorizer)
	s.Equal("aws/apigatewayv2/authorizer", authorizer.Type.Value)
	s.Equal("JWT", core.StringValue(authorizer.Spec.Fields["authorizerType"]))
	s.Equal("ordersApi_http_api", resourceRefName(authorizer.Spec.Fields["apiId"]))

	jwtConfig := authorizer.Spec.Fields["jwtConfiguration"]
	s.Require().NotNil(jwtConfig)
	s.Equal("https://identity.example.com/oauth2/v1/", core.StringValue(jwtConfig.Fields["issuer"]))
	s.Equal(
		"https://identity.example.com/api/v1/",
		core.StringValue(specItem(jwtConfig.Fields["audience"], 0)),
	)

	identity := authorizer.Spec.Fields["identitySource"]
	s.Require().NotNil(identity)
	s.Equal("$request.header.Authorization", core.StringValue(specItem(identity, 0)))
}

// A custom domain emits a domainName carrying the certificate ARN plus an
// apiMapping per protocol.
func (s *APITransformTestSuite) Test_domain_emits_domain_and_mapping() {
	const certARN = "arn:aws:acm:us-east-1:123456789012:certificate/abc"

	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"domain", core.MappingNodeFields(
				"domainName", core.MappingNodeFromString("api.example.com"),
				"certificateId", core.MappingNodeFromString(certARN),
				"securityPolicy", core.MappingNodeFromString("TLS_1_2"),
			),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	domain := resources["ordersApi_domain"]
	s.Require().NotNil(domain)
	s.Equal("aws/apigatewayv2/domainName", domain.Type.Value)
	s.Equal("api.example.com", core.StringValue(domain.Spec.Fields["domainName"]))

	config := specItem(domain.Spec.Fields["domainNameConfigurations"], 0)
	s.Require().NotNil(config)
	s.Equal(certARN, core.StringValue(config.Fields["certificateArn"]))
	s.Equal("TLS_1_2", core.StringValue(config.Fields["securityPolicy"]))

	mapping := resources["ordersApi_http_api_mapping_0"]
	s.Require().NotNil(mapping)
	s.Equal("aws/apigatewayv2/apiMapping", mapping.Type.Value)
	s.Equal("ordersApi_http_api", resourceRefName(mapping.Spec.Fields["apiId"]))
	s.Equal("ordersApi_domain", resourceRefName(mapping.Spec.Fields["domainName"]))
}

func (s *APITransformTestSuite) Test_hybrid_domain_with_colliding_base_paths_errors() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(
				core.MappingNodeFromString("http"),
				core.MappingNodeFromString("websocket"),
			),
			"domain", core.MappingNodeFields(
				// No protocol-specific basePaths: both protocols map the root path.
				"domainName", core.MappingNodeFromString("api.example.com"),
				"certificateId", core.MappingNodeFromString("arn:aws:acm:us-east-1:123456789012:certificate/abc"),
			),
		),
	}

	resources, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"chatApi": apiRes},
		edges(),
	)

	found := false
	for _, d := range diagnostics {
		if d.Level == core.DiagnosticLevelError && strings.Contains(d.Message, "base paths collide") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic about colliding hybrid base paths")

	for name, res := range resources {
		s.NotEqual("aws/apigatewayv2/apiMapping", res.Type.Value,
			"no api mapping should be emitted on collision, found %s", name)
	}
	// The APIs and the domain itself are still emitted; only the mappings are skipped.
	s.Require().NotNil(resources["chatApi_http_api"])
	s.Require().NotNil(resources["chatApi_websocket_api"])
	s.Require().NotNil(resources["chatApi_domain"])
}

func (s *APITransformTestSuite) Test_hybrid_domain_with_protocol_specific_base_paths_emits_both() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(
				core.MappingNodeFromString("http"),
				core.MappingNodeFromString("websocket"),
			),
			"domain", core.MappingNodeFields(
				"domainName", core.MappingNodeFromString("api.example.com"),
				"certificateId", core.MappingNodeFromString("arn:aws:acm:us-east-1:123456789012:certificate/abc"),
				"basePaths", core.MappingNodeItems(
					core.MappingNodeFields(
						"protocol", core.MappingNodeFromString("http"),
						"basePath", core.MappingNodeFromString("/"),
					),
					core.MappingNodeFields(
						"protocol", core.MappingNodeFromString("websocket"),
						"basePath", core.MappingNodeFromString("/ws"),
					),
				),
			),
		),
	}

	resources, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"chatApi": apiRes},
		edges(),
	)

	for _, d := range diagnostics {
		s.NotEqual(core.DiagnosticLevelError, d.Level, "protocol-specific base paths must not error: %s", d.Message)
	}
	s.Require().NotNil(resources["chatApi_http_api_mapping_0"], "http mapping emitted")
	s.Require().NotNil(resources["chatApi_websocket_api_mapping_0"], "websocket mapping emitted")
}

// CORS config is mapped onto the concrete HTTP API's corsConfiguration.
func (s *APITransformTestSuite) Test_cors_mapped_onto_http_api() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"cors", core.MappingNodeFields(
				"allowCredentials", core.MappingNodeFromBool(true),
				"allowOrigins", core.MappingNodeItems(
					core.MappingNodeFromString("https://example.com"),
				),
				"allowMethods", core.MappingNodeItems(
					core.MappingNodeFromString("GET"),
					core.MappingNodeFromString("POST"),
				),
				"maxAge", core.MappingNodeFromInt(3600),
			),
		),
	}

	resources := s.transform(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	api := resources["ordersApi_http_api"]
	s.Require().NotNil(api)
	cors := api.Spec.Fields["corsConfiguration"]
	s.Require().NotNil(cors)
	s.True(core.BoolValue(cors.Fields["allowCredentials"]))
	s.Equal("https://example.com", core.StringValue(specItem(cors.Fields["allowOrigins"], 0)))
	s.Equal("GET", core.StringValue(specItem(cors.Fields["allowMethods"], 0)))
	// The integer maxAge passes straight through to the provider's integer field.
	s.Equal(3600, core.IntValue(cors.Fields["maxAge"]))
}

// A linked HTTP handler carries the provider route annotation derived from its
// own method/path annotations, plus the JWT authorizer wiring from the default
// guard.
func (s *APITransformTestSuite) Test_linked_http_handler_carries_route_annotation() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"auth", core.MappingNodeFields(
				"defaultGuard", core.MappingNodeFromString("jwt"),
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
						"issuer", core.MappingNodeFromString("https://identity.example.com/oauth2/v1/"),
						"tokenSource", core.MappingNodeFromString("$.headers.Authorization"),
						"audience", core.MappingNodeItems(
							core.MappingNodeFromString("orders-api"),
						),
					),
				),
			),
		),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"application": "orders"}},
		},
	}
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("createOrder"),
			"handler", core.MappingNodeFromString("handlers.create"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap(
				"celerity.handler.http", "true",
				"celerity.handler.http.method", "POST",
				"celerity.handler.http.path", "/orders",
			),
			Labels: &schema.StringMap{Values: map[string]string{"application": "orders"}},
		},
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"ordersApi":   apiRes,
			"createOrder": handlerRes,
		},
		edges(edge("ordersApi", "createOrder", "celerity/api", "celerity/handler")),
	)

	lambda := resources["createOrder_lambda_func"]
	s.Require().NotNil(lambda)
	s.Equal("POST /orders", annotationLiteral(lambda.Metadata.Annotations, "aws.apigatewayv2.lambda.routeKey"))
	s.Equal("JWT", annotationLiteral(lambda.Metadata.Annotations, "aws.apigatewayv2.lambda.authorizationType"))
	// The handler carries the http protocol label so only the HTTP API attaches it.
	s.Require().NotNil(lambda.Metadata.Labels)
	s.Equal("http", lambda.Metadata.Labels.Values["celerity.internal.api.protocol"])
	// The authorizerId references the concrete authorizer emitted by the API.
	s.Equal(
		"ordersApi_jwt_authorizer",
		resourceRefName(nodeFromAnnotation(lambda.Metadata.Annotations, "aws.apigatewayv2.lambda.authorizerId")),
	)
}

// A guarded WebSocket handler on a WebSocket-only API must NOT stamp an
// authorizer reference: the API emits no authorizer for WebSocket, so the
// reference would dangle. WebSocket auth is handled in-message by the handler.
func (s *APITransformTestSuite) Test_websocket_handler_does_not_stamp_authorizer() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("websocket")),
			"auth", core.MappingNodeFields(
				"defaultGuard", core.MappingNodeFromString("jwt"),
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
						"issuer", core.MappingNodeFromString("https://identity.example.com/oauth2/v1/"),
						"tokenSource", core.MappingNodeFromString("$.headers.Authorization"),
						"audience", core.MappingNodeItems(core.MappingNodeFromString("chat-api")),
					),
				),
			),
		),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"application": "chat"}},
		},
	}
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("onMessage"),
			"handler", core.MappingNodeFromString("handlers.message"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap(
				"celerity.handler.websocket", "true",
				"celerity.handler.websocket.route", "sendMessage",
			),
			Labels: &schema.StringMap{Values: map[string]string{"application": "chat"}},
		},
	}

	resources := s.transform(
		map[string]*schema.Resource{
			"chatApi":   apiRes,
			"onMessage": handlerRes,
		},
		edges(edge("chatApi", "onMessage", "celerity/api", "celerity/handler")),
	)

	lambda := resources["onMessage_lambda_func"]
	s.Require().NotNil(lambda)
	// The route key is still stamped, but no authorizer/authorizationType is.
	s.Equal("sendMessage", annotationLiteral(lambda.Metadata.Annotations, "aws.apigatewayv2.lambda.routeKey"))
	// The handler carries the websocket protocol label so only the WebSocket API attaches it.
	s.Require().NotNil(lambda.Metadata.Labels)
	s.Equal("websocket", lambda.Metadata.Labels.Values["celerity.internal.api.protocol"])
	s.Equal("", annotationLiteral(lambda.Metadata.Annotations, "aws.apigatewayv2.lambda.authorizerId"),
		"a WebSocket handler must not reference an API Gateway authorizer")
	s.Equal("", annotationLiteral(lambda.Metadata.Annotations, "aws.apigatewayv2.lambda.authorizationType"))
}

// tracingEnabled raises a specific provider-limitation warning: API Gateway v2
// (HTTP/WebSocket) stages expose no X-Ray active-tracing toggle, so tracing cannot
// be wired at the stage on aws-serverless.
func (s *APITransformTestSuite) Test_tracing_enabled_warns_about_apigatewayv2_limitation() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"tracingEnabled", core.MappingNodeFromBool(true),
		),
	}

	resources, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	// No tracing/X-Ray field is set on the emitted stage.
	stage := resources["ordersApi_http_stage"]
	s.Require().NotNil(stage)
	s.NotContains(stage.Spec.Fields, "tracingEnabled")

	s.True(
		hasWarningContaining(diagnostics, "do not support X-Ray active tracing"),
		"expected a specific API Gateway v2 X-Ray limitation warning",
	)
}

// A JWT guard using oauth2 discovery still emits an (OIDC) JWT authorizer but
// raises a scoped warning that oauth2 discovery is unsupported on aws-serverless.
func (s *APITransformTestSuite) Test_jwt_guard_oauth2_discovery_warns() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"auth", core.MappingNodeFields(
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
						"issuer", core.MappingNodeFromString("https://identity.example.com/oauth2/v1/"),
						"discoveryMode", core.MappingNodeFromString("oauth2"),
						"tokenSource", core.MappingNodeFromString("$.headers.Authorization"),
						"audience", core.MappingNodeItems(
							core.MappingNodeFromString("orders-api"),
						),
					),
				),
			),
		),
	}

	resources, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	// The JWT authorizer is still emitted (downgraded to OIDC).
	s.Require().NotNil(resources["ordersApi_jwt_authorizer"])
	s.True(
		hasWarningContaining(diagnostics, "oauth2"),
		"expected a scoped oauth2 discovery warning",
	)
}

// A guard using a non-bearer authScheme (basic/digest) raises a scoped warning
// because API Gateway only applies the bearer scheme.
func (s *APITransformTestSuite) Test_guard_non_bearer_auth_scheme_warns() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"auth", core.MappingNodeFields(
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
						"issuer", core.MappingNodeFromString("https://identity.example.com/oauth2/v1/"),
						"authScheme", core.MappingNodeFromString("basic"),
						"tokenSource", core.MappingNodeFromString("$.headers.Authorization"),
						"audience", core.MappingNodeItems(
							core.MappingNodeFromString("orders-api"),
						),
					),
				),
			),
		),
	}

	_, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	s.True(
		hasWarningContaining(diagnostics, "authScheme"),
		"expected a scoped non-bearer authScheme warning",
	)
}

// A jwt guard missing the conditionally-required issuer, audience and
// tokenSource fields errors before emission instead of producing an invalid
// API Gateway JWT authorizer.
func (s *APITransformTestSuite) Test_jwt_guard_missing_required_fields_errors() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("http")),
			"auth", core.MappingNodeFields(
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
					),
				),
			),
		),
	}

	resources, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"ordersApi": apiRes},
		edges(),
	)

	found := false
	for _, d := range diagnostics {
		if d.Level == core.DiagnosticLevelError &&
			strings.Contains(d.Message, "issuer") &&
			strings.Contains(d.Message, "audience") &&
			strings.Contains(d.Message, "tokenSource") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic listing the missing jwt guard fields")
	s.Nil(resources["ordersApi_jwt_authorizer"], "no authorizer should be emitted for an invalid jwt guard")
}

// A websocketConfig using authStrategy "connect" raises a scoped warning because
// serverless WebSocket APIs only support the authMessage strategy.
func (s *APITransformTestSuite) Test_websocket_connect_auth_strategy_warns() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(
				core.MappingNodeFields(
					"websocketConfig", core.MappingNodeFields(
						"authStrategy", core.MappingNodeFromString("connect"),
						"authGuard", core.MappingNodeFromString("jwt"),
					),
				),
			),
		),
	}

	_, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"chatApi": apiRes},
		edges(),
	)

	s.True(
		hasWarningContaining(diagnostics, "connect"),
		"expected a scoped WebSocket connect-strategy warning",
	)
}

func (s *APITransformTestSuite) Test_websocket_only_api_does_not_emit_authorizers() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		Spec: core.MappingNodeFields(
			"protocols", core.MappingNodeItems(core.MappingNodeFromString("websocket")),
			"auth", core.MappingNodeFields(
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
						"issuer", core.MappingNodeFromString("https://issuer.example.com"),
						"audience", core.MappingNodeItems(core.MappingNodeFromString("chat-api")),
						"tokenSource", core.MappingNodeFromString("$.headers.Authorization"),
					),
				),
			),
		),
	}

	resources, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"wsApi": apiRes},
		edges(),
	)

	// The guard is complete, so no error must be raised that would mask the
	// WebSocket-only behavior under test.
	for _, d := range diagnostics {
		s.NotEqual(core.DiagnosticLevelError, d.Level, "unexpected error diagnostic: %s", d.Message)
	}
	s.True(hasWarningContaining(diagnostics, "WebSocket-only"))
	for name, res := range resources {
		s.NotEqual("aws/apigatewayv2/authorizer", res.Type.Value,
			"no authorizer should be emitted for a WebSocket-only API, found %s", name)
	}
	s.Require().NotNil(resources["wsApi_websocket_api"], "the WebSocket API itself is still emitted")
}

func (s *APITransformTestSuite) Test_no_protocol_warns_and_emits_nothing() {
	apiRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/api"},
		// No protocols; a complete guard is set to prove no dangling authorizer is emitted.
		Spec: core.MappingNodeFields(
			"auth", core.MappingNodeFields(
				"guards", core.MappingNodeFields(
					"jwt", core.MappingNodeFields(
						"type", core.MappingNodeFromString("jwt"),
						"issuer", core.MappingNodeFromString("https://issuer.example.com"),
						"audience", core.MappingNodeItems(core.MappingNodeFromString("orders-api")),
						"tokenSource", core.MappingNodeFromString("$.headers.Authorization"),
					),
				),
			),
		),
	}

	resources, diagnostics := s.transformWithDiagnostics(
		map[string]*schema.Resource{"noProtoApi": apiRes},
		edges(),
	)

	// The guard is complete, so no error must be raised that would mask the
	// no-protocol behavior under test.
	for _, d := range diagnostics {
		s.NotEqual(core.DiagnosticLevelError, d.Level, "unexpected error diagnostic: %s", d.Message)
	}
	s.True(hasWarningContaining(diagnostics, "no recognised protocol"))
	for name, res := range resources {
		s.NotContains(res.Type.Value, "apigatewayv2",
			"no apigatewayv2 resource should be emitted without a protocol, found %s (%s)", name, res.Type.Value)
	}
}

func (s *APITransformTestSuite) transform(
	resources map[string]*schema.Resource,
	lg linktypes.DeclaredLinkGraph,
) map[string]*schema.Resource {
	out, _ := s.transformWithDiagnostics(resources, lg)
	return out
}

func (s *APITransformTestSuite) transformWithDiagnostics(
	resources map[string]*schema.Resource,
	lg linktypes.DeclaredLinkGraph,
) (map[string]*schema.Resource, []*core.Diagnostic) {
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
	return out.TransformedBlueprint.Resources.Values, out.Diagnostics
}

// specItem returns the element at index i of an array MappingNode, or nil.
func specItem(node *core.MappingNode, i int) *core.MappingNode {
	if node == nil || i >= len(node.Items) {
		return nil
	}
	return node.Items[i]
}

// nodeFromAnnotation wraps an annotation's substitution values in a MappingNode so
// resourceRefName can extract a ${resources.*} reference set on an annotation.
func nodeFromAnnotation(annos *schema.StringOrSubstitutionsMap, key string) *core.MappingNode {
	if annos == nil {
		return nil
	}
	value, ok := annos.Values[key]
	if !ok {
		return nil
	}
	return &core.MappingNode{StringWithSubstitutions: value}
}
