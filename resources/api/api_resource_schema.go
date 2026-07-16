package api

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
)

func apiResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "ApiDefinition",
		Description: "An HTTP API or a WebSocket API that routes incoming requests and messages to " +
			"linked handlers. The API itself carries no routes; routes are derived from the handlers " +
			"selected by the API's linkSelector, using each handler's route annotations " +
			"(celerity.handler.http.method/path, celerity.handler.websocket.route). On aws-serverless " +
			"each protocol maps to a separate API Gateway v2 API (a hybrid HTTP + WebSocket API becomes " +
			"two API Gateways).",
		Required: []string{"protocols"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"protocols":      protocolsSchema(),
			"cors":           corsSchema(),
			"domain":         domainSchema(),
			"tracingEnabled": tracingEnabledSchema(),
			"auth":           authSchema(),

			// Computed outputs.
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The ID of the created API in the target environment. On aws-serverless this " +
					"is the API Gateway ARN synthesised from the deployment region and the created API's id " +
					"(for example arn:aws:apigateway:us-east-1::/apis/1234567890).",
			},
			"baseUrl": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The base URL of the deployed API. On aws-serverless this is the API Gateway " +
					"endpoint (for example https://abcdef.execute-api.us-east-1.amazonaws.com). For a hybrid " +
					"API this is the HTTP API's endpoint.",
			},
		},
	}
}

func protocolsSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type: provider.ResourceDefinitionsSchemaTypeArray,
		Description: "The protocols the API supports: \"http\", \"websocket\", or a WebSocket named " +
			"configuration object. An API may support both HTTP and WebSocket protocols; on aws-serverless " +
			"a hybrid API maps to two separate API Gateways.",
		Items: &provider.ResourceDefinitionsSchema{
			Type: provider.ResourceDefinitionsSchemaTypeUnion,
			Description: "A protocol entry: either the string \"http\"/\"websocket\", or a " +
				"websocketNamedConfiguration object that additionally configures the WebSocket protocol.",
			OneOf: []*provider.ResourceDefinitionsSchema{
				{
					Type:        provider.ResourceDefinitionsSchemaTypeString,
					Description: "A protocol name: \"http\" or \"websocket\".",
					AllowedValues: []*core.MappingNode{
						core.MappingNodeFromString("http"),
						core.MappingNodeFromString("websocket"),
					},
				},
				websocketNamedConfigurationSchema(),
			},
		},
	}
}

func websocketNamedConfigurationSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "WebSocketNamedConfiguration",
		Description: "A named entry for WebSocket configuration used in the protocols list.",
		Required:    []string{"websocketConfig"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"websocketConfig": websocketConfigurationSchema(),
		},
	}
}

func websocketConfigurationSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "WebSocketConfiguration",
		Description: "Configuration for the WebSocket protocol of an API.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"routeKey": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The key in the message JSON object used to route text messages to the correct " +
					"handler. Not used for binary messages, whose route is extracted from the message prefix. " +
					"On aws-serverless this becomes the WebSocket API's routeSelectionExpression " +
					"($request.body.<routeKey>).",
				Default: core.MappingNodeFromString("event"),
			},
			"authStrategy": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The strategy for authenticating WebSocket connections. \"authMessage\" expects " +
					"the client to send a message carrying an auth token; \"connect\" expects the token on the " +
					"connection request (header or cookie). In serverless environments only \"authMessage\" is " +
					"supported, as custom WebSocket status codes are not available on serverless WebSocket APIs.",
				Default: core.MappingNodeFromString("authMessage"),
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("authMessage"),
					core.MappingNodeFromString("connect"),
				},
			},
			"authGuard": guardReferenceSchema(
				"The name of the auth guard (or ordered list of guards) in the API's auth guard map to " +
					"apply to every new WebSocket connection. Required when authStrategy is set. When an array " +
					"is given the guards execute in order and all must pass.",
			),
		},
	}
}

func corsSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type: provider.ResourceDefinitionsSchemaTypeUnion,
		Description: "Cross-Origin Resource Sharing (CORS) configuration for the API. Either the string " +
			"\"*\" (allow all origins) or a detailed corsConfiguration object. CORS applies only to the HTTP " +
			"protocol on aws-serverless.",
		OneOf: []*provider.ResourceDefinitionsSchema{
			{
				Type:          provider.ResourceDefinitionsSchemaTypeString,
				Description:   "A shorthand allowing all origins, expressed as \"*\".",
				AllowedValues: []*core.MappingNode{core.MappingNodeFromString("*")},
			},
			corsConfigurationSchema(),
		},
	}
}

func corsConfigurationSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "CorsConfiguration",
		Description: "Detailed Cross-Origin Resource Sharing (CORS) configuration for the API.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"allowCredentials": {
				Type:        provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "Whether the request is allowed to contain credentials set in cookies.",
			},
			"allowOrigins": stringArraySchema(
				"The origins allowed to access the API's endpoints.",
				"An allowed origin, for example https://example.com.",
			),
			"allowMethods": stringArraySchema(
				"The HTTP methods allowed when accessing the API's endpoints from an allowed origin.",
				"An allowed HTTP method, for example GET or POST.",
			),
			"allowHeaders": stringArraySchema(
				"The HTTP headers allowed when accessing the API's endpoints from an allowed origin.",
				"An allowed HTTP header, for example Content-Type.",
			),
			"exposeHeaders": stringArraySchema(
				"The HTTP headers that can be exposed in responses from the API's endpoints.",
				"An exposed HTTP header, for example Content-Length.",
			),
			"maxAge": {
				Type: provider.ResourceDefinitionsSchemaTypeInteger,
				Description: "The number of seconds to cache a CORS preflight request for. On aws-serverless " +
					"it is applied as the CORS maxAge (seconds).",
			},
		},
	}
}

func domainSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "DomainConfiguration",
		Description: "Configuration for attaching a custom domain to the API.",
		Required:    []string{"domainName", "certificateId"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"domainName": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Description: "The custom domain name to use for the API (for example api.example.com).",
			},
			"basePaths": basePathsSchema(),
			"normalizeBasePath": {
				Type: provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "Whether the base path should be normalised to remove non-alphanumeric " +
					"characters.",
				Default: core.MappingNodeFromBool(true),
			},
			"certificateId": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The ID of the certificate to use for the custom domain. On aws-serverless this " +
					"is the ARN of an ACM certificate; it is often supplied via a variable to keep the " +
					"blueprint decoupled from a specific target environment.",
			},
			"securityPolicy": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Description: "The Transport Layer Security (TLS) version and cipher suite for the domain.",
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("TLS_1_0"),
					core.MappingNodeFromString("TLS_1_2"),
				},
			},
		},
	}
}

func basePathsSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type: provider.ResourceDefinitionsSchemaTypeArray,
		Description: "The base paths to configure for the API, useful when the same domain hosts multiple " +
			"APIs. Each entry is either a plain string path or a basePathConfiguration mapping a protocol " +
			"to a base path. Hybrid APIs must use protocol-specific base paths, which cannot be mixed with " +
			"plain string paths.",
		Default: core.MappingNodeItems(core.MappingNodeFromString("/")),
		Items: &provider.ResourceDefinitionsSchema{
			Type: provider.ResourceDefinitionsSchemaTypeUnion,
			Description: "A base path: either a plain string path (for example \"/\") or a " +
				"basePathConfiguration object mapping a protocol to a base path.",
			OneOf: []*provider.ResourceDefinitionsSchema{
				{
					Type:        provider.ResourceDefinitionsSchemaTypeString,
					Description: "A plain base path, for example \"/\" or \"/api/v1\".",
				},
				basePathConfigurationSchema(),
			},
		},
	}
}

func basePathConfigurationSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "BasePathConfiguration",
		Description: "A base path scoped to a specific protocol of the API.",
		Required:    []string{"protocol", "basePath"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"protocol": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Description: "The protocol the base path serves.",
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("http"),
					core.MappingNodeFromString("websocket"),
				},
			},
			"basePath": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Description: "The base path to configure for the specified protocol (for example \"/api\").",
			},
		},
	}
}

func tracingEnabledSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type: provider.ResourceDefinitionsSchemaTypeBoolean,
		Description: "Whether tracing is enabled for the API. On aws-serverless this cannot be honoured: " +
			"AWS API Gateway v2 (HTTP and WebSocket) APIs do not support X-Ray active tracing on the " +
			"stage, so a warning is emitted and no stage-level tracing is enabled. Enable X-Ray tracing " +
			"on the linked Lambda handlers to trace requests instead.",
	}
}

func authSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "AuthConfiguration",
		Description: "Configuration for authorization that controls access to the API.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"guards": {
				Type: provider.ResourceDefinitionsSchemaTypeMap,
				Description: "A mapping of guard names to guard configurations used to control access to the " +
					"API. On aws-serverless a \"jwt\" guard maps to an API Gateway JWT authorizer and a " +
					"\"custom\" guard maps to a REQUEST (Lambda) authorizer backed by the handler that " +
					"implements it.",
				MapValues: authGuardConfigurationSchema(),
			},
			"defaultGuard": guardReferenceSchema(
				"The guard (or ordered list of guards) applied by default to every endpoint of the API. " +
					"Individual handlers opt out with the celerity.handler.public annotation. When an array is " +
					"given the guards execute in order and all must pass.",
			),
		},
	}
}

func authGuardConfigurationSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "AuthGuardConfiguration",
		Description: "Configuration for a single guard that controls access to the API.",
		Required:    []string{"type"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"type": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The type of guard. \"jwt\" validates a JSON Web Token; \"custom\" delegates to " +
					"a handler linked to the API with the celerity.handler.guard.custom annotation.",
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("jwt"),
					core.MappingNodeFromString("custom"),
				},
			},
			"issuer": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The token issuer used to validate a JWT (required when type is \"jwt\", ignored " +
					"otherwise). The issuer URL is used to retrieve OIDC or OAuth2 discovery documents.",
			},
			"discoveryMode": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The discovery mode used to retrieve the OIDC or OAuth2 discovery documents when " +
					"type is \"jwt\". Serverless platforms often support only OIDC discovery.",
				Default: core.MappingNodeFromString("oidc"),
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("oidc"),
					core.MappingNodeFromString("oauth2"),
				},
			},
			"tokenSource": tokenSourceSchema(),
			"audience": stringArraySchema(
				"The list of intended recipients (audiences) of a JWT; a valid token must provide an "+
					"\"aud\" matching at least one entry. Required when type is \"jwt\", ignored otherwise.",
				"A JWT audience value, for example a client ID or a resource URL.",
			),
			"authScheme": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The auth scheme of the token sourced from the request or message; its prefix is " +
					"stripped from the token before validation.",
				Default: core.MappingNodeFromString("bearer"),
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("bearer"),
					core.MappingNodeFromString("basic"),
					core.MappingNodeFromString("digest"),
				},
			},
		},
	}
}

func tokenSourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type: provider.ResourceDefinitionsSchemaTypeUnion,
		Description: "The source of the token in the request or message (required when the guard type is " +
			"\"jwt\"). Either a single \"$.*\" path string (for example $.headers.Authorization) or an array " +
			"of per-protocol valueSourceConfiguration entries. On aws-serverless the HTTP source becomes the " +
			"JWT authorizer's identitySource (for example $request.header.Authorization).",
		OneOf: []*provider.ResourceDefinitionsSchema{
			{
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "A single token source path, for example $.headers.Authorization, " +
					"$.query.token or $.cookies.authToken.",
			},
			{
				Type:        provider.ResourceDefinitionsSchemaTypeArray,
				Description: "Per-protocol token sources; currently one source is supported per protocol.",
				Items:       valueSourceConfigurationSchema(),
			},
		},
	}
}

func valueSourceConfigurationSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeObject,
		Label:       "ValueSourceConfiguration",
		Description: "A protocol-scoped source used to extract a value such as a JWT from a request or message.",
		Required:    []string{"protocol", "source"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"protocol": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Description: "The protocol the value source is used for.",
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("http"),
					core.MappingNodeFromString("websocket"),
				},
			},
			"source": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The source of the value, expressed as \"$.*\" (for example " +
					"$.headers.Authorization or $.data.token).",
			},
		},
	}
}

// Models the recurring "string | array[string]" guard reference union used by
// auth.defaultGuard and websocketConfig.authGuard.
func guardReferenceSchema(description string) *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeUnion,
		Description: description,
		OneOf: []*provider.ResourceDefinitionsSchema{
			{
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Description: "A single guard name.",
			},
			{
				Type:        provider.ResourceDefinitionsSchemaTypeArray,
				Description: "An ordered list of guard names; all must pass.",
				Items: &provider.ResourceDefinitionsSchema{
					Type:        provider.ResourceDefinitionsSchemaTypeString,
					Description: "A guard name defined in the API's auth guard map.",
				},
			},
		},
	}
}

func stringArraySchema(description, itemDescription string) *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeArray,
		Description: description,
		Items: &provider.ResourceDefinitionsSchema{
			Type:        provider.ResourceDefinitionsSchemaTypeString,
			Description: itemDescription,
		},
	}
}
