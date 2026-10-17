package apiawsserverless

import (
	"context"
	"fmt"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/api"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler/handlerawsserverless"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	protocolHTTP      = "http"
	protocolWebSocket = "websocket"

	defaultWSRouteKey = "event"
	defaultStageName  = "$default"
)

// Captures which protocols the abstract API declares and the WebSocket route
// key used to build the WebSocket API's routeSelectionExpression.
type protocolInfo struct {
	hasHTTP    bool
	hasWS      bool
	wsRouteKey string
}

// Emit builds the API Gateway v2 API a celerity/api maps to, with the stage,
// routes and integrations that go with it.
func Emit(
	_ context.Context,
	run *transformutils.Run,
	r *api.ResolvedAPI,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	ctx := run.TransformContext
	info := parseProtocols(r.Resource.Spec)

	resources := map[string]*schema.Resource{}
	var diagnostics []*core.Diagnostic

	if err := emitProtocolAPIs(r, info, shared.ResolveAppName(run), resources); err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		return &transformutils.EmitResult{
			Diagnostics: []*core.Diagnostic{noRecognisedProtocolWarning(r)},
		}, nil
	}

	diagnostics = append(diagnostics, websocketAuthWarnings(r)...)

	authDiags, err := emitAuthorizers(ctx, r, info, resources)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, authDiags...)

	domainDiags, err := emitDomain(r, info, resources)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, domainDiags...)

	diagnostics = append(diagnostics, stageTracingUnsupported(r)...)

	derivedValues, idDiag := synthesizeIDValue(ctx, r, info)
	if idDiag != nil {
		diagnostics = append(diagnostics, idDiag)
	}

	// Rewrite any ${resources.x.spec.y} references a user embedded in a value
	// (e.g. domain.certificateId pointing at another resource). Concrete refs
	// this emit builds use concrete names and are left untouched.
	for _, res := range resources {
		res.Spec = subwalk.WalkMappingNode(res.Spec, transformutils.RewriteResourcePropertyRefs(rw))
	}

	return &transformutils.EmitResult{
		Resources:     resources,
		DerivedValues: derivedValues,
		Diagnostics:   diagnostics,
	}, nil
}

// Builds the concrete API's linkSelector, the single
// handlerawsserverless.LabelKeyAPITarget label naming this API and protocol.
//
// The author's own labels are deliberately not carried over. A link selector
// matches a resource carrying ANY one of the selector's labels, so keeping
// "application: myApi" alongside the target label would match every handler of
// that application again, including the other protocol's, which is the whole
// thing this is here to prevent. Nothing is lost by dropping them, the handler
// emit stamps the target label only on handlers it resolved as linked to this
// API in the first place, through the author's selector.
//
// A nil or label-less selector is returned unchanged, preserving the existing
// behaviour that an API without a label selector attaches no handlers.
func protocolScopedLinkSelector(
	selector *schema.LinkSelector,
	apiName string,
	protocol string,
) *schema.LinkSelector {
	if selector == nil || selector.ByLabel == nil {
		return selector
	}

	return &schema.LinkSelector{
		ByLabel: &schema.StringMap{
			Values: map[string]string{
				handlerawsserverless.LabelKeyAPITarget: handlerawsserverless.APITargetLabelValue(apiName, protocol),
			},
		},
	}
}

// One concrete API per declared protocol. A hybrid API emits two, because a
// single API Gateway v2 API cannot serve both.
func emitProtocolAPIs(
	r *api.ResolvedAPI,
	info protocolInfo,
	appName string,
	resources map[string]*schema.Resource,
) error {
	if info.hasHTTP {
		if err := emitProtocolAPI(r, info, protocolHTTP, appName, resources); err != nil {
			return err
		}
	}

	if info.hasWS {
		if err := emitProtocolAPI(r, info, protocolWebSocket, appName, resources); err != nil {
			return err
		}
	}

	return nil
}

// Nothing is emitted for an API with no recognised protocol. Authorizer, domain
// and id emission would otherwise reference an API resource that does not exist,
// since the primary concrete name falls back to the HTTP one.
func noRecognisedProtocolWarning(r *api.ResolvedAPI) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/api %q declares no recognised protocol; expected \"http\", \"websocket\" or a "+
				"websocketConfig object in spec.protocols",
			r.Name,
		),
		Range: shared.DiagnosticRange(
			shared.NodeMeta(specNode(r.Resource.Spec, "$.protocols")),
			shared.ResourceMeta(r.Resource),
		),
	}
}

// API Gateway v2 APIs does not support the X-Ray active-tracing toggle: aws/apigatewayv2/stage
// has no tracing field, since X-Ray active tracing is only available on API Gateway
// REST/v1 stages. The stage's defaultRouteSettings.dataTraceEnabled is CloudWatch
// request/response logging rather than X-Ray, so tracingEnabled cannot be honoured
// at the stage on this target.
func stageTracingUnsupported(r *api.ResolvedAPI) []*core.Diagnostic {
	tracingNode := specNode(r.Resource.Spec, "$.tracingEnabled")
	if !core.BoolValue(tracingNode) {
		return nil
	}

	return []*core.Diagnostic{{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/api %q sets tracingEnabled, but AWS API Gateway v2 (HTTP and WebSocket) APIs do "+
				"not support X-Ray active tracing on the stage (X-Ray active tracing is only available on "+
				"API Gateway REST/v1 stages); no stage-level tracing has been enabled. Enable X-Ray tracing "+
				"on the linked Lambda handlers to trace requests on aws-serverless",
			r.Name,
		),
		Range: shared.DiagnosticRange(
			shared.NodeMeta(tracingNode),
			shared.ResourceMeta(r.Resource),
		),
	}}
}

func emitProtocolAPI(
	r *api.ResolvedAPI,
	info protocolInfo,
	protocol string,
	appName string,
	resources map[string]*schema.Resource,
) error {
	spec := core.MappingNodeFields(
		// The abstract API carries no author-facing name field, so the deployed
		// name is always transformer-invented and therefore app-scoped
		// (apigatewayv2 API names are account-visible; two apps sharing an
		// account must not collide). 128 is the apigatewayv2 name limit.
		"name", core.MappingNodeFromString(
			shared.AppScopedPhysicalName(appName, fmt.Sprintf("%s-%s", r.Name, protocol), 128)),
		"protocolType", core.MappingNodeFromString(protocolTypeValue(protocol)),
	)

	if protocol == protocolWebSocket {
		spec.Fields["routeSelectionExpression"] = core.MappingNodeFromString(
			fmt.Sprintf("$request.body.%s", info.wsRouteKey),
		)
	}

	// CORS applies only to HTTP APIs on aws-serverless.
	if protocol == protocolHTTP {
		if cors := corsConfigNode(r.Resource.Spec); cors != nil {
			spec.Fields["corsConfiguration"] = cors
		}
	}

	// apigatewayv2 tags are a string map, not the list of {key, value} objects
	// most AWS resource types take.
	if tags := sharedaws.MapTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      appName,
		ResourceName: r.Name,
		ResourceType: "celerity/api",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	apiResName := apiResourceName(r.Name, protocol)
	resources[apiResName] = &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/apigatewayv2/api"},
		Spec:         spec,
		Metadata:     apiMetadata(r),
		LinkSelector: protocolScopedLinkSelector(r.Resource.LinkSelector, r.Name, protocol),
	}

	stageRes, err := stageResource(r, apiResName, appName)
	if err != nil {
		return err
	}
	resources[stageResourceName(r.Name, protocol)] = stageRes
	return nil
}

func stageResource(r *api.ResolvedAPI, apiResName string, appName string) (*schema.Resource, error) {
	apiIDRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.apiId}", apiResName),
	)
	if err != nil {
		return nil, err
	}

	spec := core.MappingNodeFields(
		"apiId", apiIDRef,
		"stageName", core.MappingNodeFromString(defaultStageName),
		"autoDeploy", core.MappingNodeFromBool(true),
	)

	// apigatewayv2 stages take tags as a {key: value} map rather than a list.
	if tags := sharedaws.MapTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      appName,
		ResourceName: r.Name,
		ResourceType: "celerity/api",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/apigatewayv2/stage"},
		Spec:     spec,
		Metadata: apiMetadata(r),
	}, nil
}

// A JWT authorizer is emitted for "jwt" guards and a REQUEST (Lambda) authorizer
// for "custom" guards backed by the handler that implements the guard.
// Authorizers attach to the HTTP API when present, otherwise the WebSocket API.
func emitAuthorizers(
	ctx transform.Context,
	r *api.ResolvedAPI,
	info protocolInfo,
	resources map[string]*schema.Resource,
) ([]*core.Diagnostic, error) {
	guards, ok := pluginutils.GetValueByPath("$.auth.guards", r.Resource.Spec)
	if !ok || guards == nil || guards.Fields == nil {
		return nil, nil
	}

	targetAPIResName := authorizerTargetAPIResName(r.Name, info)
	apiIDRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.apiId}", targetAPIResName),
	)
	if err != nil {
		return nil, err
	}

	var diagnostics []*core.Diagnostic
	for guardName, cfg := range guards.Fields {
		if !info.hasHTTP {
			// WebSocket-only API. API Gateway v2 WebSocket APIs don't support JWT
			// authorizers, and a REQUEST authorizer there runs at $connect — the
			// "connect" strategy aws-serverless doesn't support. Celerity WebSocket
			// auth uses the in-message authMessage strategy (validated by the
			// handler), so no gateway authorizer is emitted.
			diagnostics = append(diagnostics, &core.Diagnostic{
				Level: core.DiagnosticLevelWarning,
				Message: fmt.Sprintf(
					"celerity/api %q guard %q is not applied as an API Gateway authorizer on a WebSocket-only "+
						"API; WebSocket APIs authenticate via the in-message authMessage strategy on aws-serverless",
					r.Name, guardName,
				),
				Range: shared.DiagnosticRange(
					shared.NodeMeta(cfg),
					shared.ResourceMeta(r.Resource),
				),
			})
			continue
		}
		// The guard type selects which authorizer resource is emitted, so it
		// cannot be deferred to deploy time.
		if diag := shared.DeferredValueError(
			shared.AWSServerless, "api", r.Name,
			fmt.Sprintf("guard %q type", guardName), specNode(cfg, "$.type"), r.Resource,
		); diag != nil {
			diagnostics = append(diagnostics, diag)
			continue
		}

		guardType := core.StringValue(specNode(cfg, "$.type"))
		diagnostics = append(diagnostics, guardConfigWarnings(r.Name, guardName, cfg)...)
		switch guardType {
		case "jwt":
			if diag := jwtGuardError(r.Name, guardName, cfg, r.Resource); diag != nil {
				diagnostics = append(diagnostics, diag)
				continue
			}
			resources[authorizerResourceName(r.Name, guardName)] = jwtAuthorizer(r.Name, guardName, cfg, apiIDRef)
		case "custom":
			authRes, diag := customAuthorizer(ctx, r, guardName, apiIDRef)
			if diag != nil {
				diagnostics = append(diagnostics, diag)
				continue
			}
			resources[authorizerResourceName(r.Name, guardName)] = authRes
		}
	}
	return diagnostics, nil
}

// guardConfigNode locates a named guard's configuration in the API spec, so a
// diagnostic about that guard can point at where it was declared.
func guardConfigNode(r *api.ResolvedAPI, guardName string) *core.MappingNode {
	guards, ok := pluginutils.GetValueByPath("$.auth.guards", r.Resource.Spec)
	if !ok || guards == nil {
		return nil
	}

	return guards.Fields[guardName]
}

// Raises scoped warnings for guard configuration that aws-serverless (API
// Gateway) cannot honour: oauth2 discovery (JWT authorizers only support OIDC)
// and non-bearer auth schemes (only bearer is applied).
func guardConfigWarnings(apiName, guardName string, cfg *core.MappingNode) []*core.Diagnostic {
	var diagnostics []*core.Diagnostic
	discoveryModeNode := specNode(cfg, "$.discoveryMode")
	if core.StringValue(discoveryModeNode) == "oauth2" {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/api %q guard %q sets discoveryMode \"oauth2\", but AWS API Gateway JWT "+
					"authorizers only support OIDC discovery; the guard has been emitted as an OIDC JWT "+
					"authorizer and oauth2 discovery is ignored on aws-serverless",
				apiName, guardName,
			),
			Range: shared.DiagnosticRange(
				shared.NodeMeta(discoveryModeNode),
				shared.NodeMeta(cfg),
			),
		})
	}
	authSchemeNode := specNode(cfg, "$.authScheme")
	if scheme := core.StringValue(authSchemeNode); scheme != "" && scheme != "bearer" {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/api %q guard %q sets authScheme %q, but AWS API Gateway authorizers only apply "+
					"the \"bearer\" scheme; the %q scheme is ignored on aws-serverless",
				apiName, guardName, scheme, scheme,
			),
			Range: shared.DiagnosticRange(
				shared.NodeMeta(authSchemeNode),
				shared.NodeMeta(cfg),
			),
		})
	}
	return diagnostics
}

// An API Gateway JWT authorizer is invalid without an issuer, at least one
// audience and an identity source, and the abstract schema can only mark these
// conditionally required on the "jwt" type in prose. A jwt guard missing any of
// them is reported as an error and skipped rather than emitting a broken
// authorizer.
func jwtGuardError(
	apiName, guardName string,
	cfg *core.MappingNode,
	resource *schema.Resource,
) *core.Diagnostic {
	// tokenSource is rewritten into an API Gateway identity source
	// ($.headers.Authorization -> $request.header.Authorization), which needs
	// the literal prefix. Reported on its own, because "missing tokenSource"
	// would be wrong, it is set, just not to something usable here.
	if diag := shared.DeferredValueError(
		shared.AWSServerless, "api", apiName,
		fmt.Sprintf("guard %q tokenSource", guardName),
		specNode(cfg, "$.tokenSource"), resource,
	); diag != nil {
		return diag
	}

	var missing []string
	if !shared.HasStringValue(specNode(cfg, "$.issuer")) {
		missing = append(missing, "issuer")
	}
	if audience := specNode(cfg, "$.audience"); audience == nil || len(audience.Items) == 0 {
		missing = append(missing, "audience")
	}
	if identitySourceNode(specNode(cfg, "$.tokenSource")) == nil {
		missing = append(missing, "tokenSource")
	}
	if len(missing) == 0 {
		return nil
	}
	return &core.Diagnostic{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/api %q guard %q has type \"jwt\" but is missing required field(s) %s; a jwt guard must "+
				"set issuer, audience and tokenSource to emit a valid API Gateway JWT authorizer",
			apiName, guardName, strings.Join(missing, ", "),
		),
		// The fields are missing, so the guard block itself is the closest thing
		// to point at.
		Range: shared.DiagnosticRange(shared.NodeMeta(cfg)),
	}
}

func jwtAuthorizer(
	apiName string,
	guardName string,
	cfg *core.MappingNode,
	apiIDRef *core.MappingNode,
) *schema.Resource {
	jwtConfig := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	if issuer, ok := pluginutils.GetValueByPath("$.issuer", cfg); ok {
		jwtConfig.Fields["issuer"] = issuer
	}
	if audience, ok := pluginutils.GetValueByPath("$.audience", cfg); ok {
		jwtConfig.Fields["audience"] = audience
	}

	spec := core.MappingNodeFields(
		"apiId", apiIDRef,
		"authorizerType", core.MappingNodeFromString("JWT"),
		"name", core.MappingNodeFromString(guardName),
		"jwtConfiguration", jwtConfig,
	)

	if identity := identitySourceNode(specNode(cfg, "$.tokenSource")); identity != nil {
		spec.Fields["identitySource"] = identity
	}

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/apigatewayv2/authorizer"},
		Spec:     spec,
		Metadata: infraMetadata(apiName, guardName+" authorizer"),
	}
}

func customAuthorizer(
	ctx transform.Context,
	r *api.ResolvedAPI,
	guardName string,
	apiIDRef *core.MappingNode,
) (*schema.Resource, *core.Diagnostic) {
	guardRange := shared.DiagnosticRange(
		shared.NodeMeta(guardConfigNode(r, guardName)),
		shared.ResourceMeta(r.Resource),
	)

	handlerName, found := customGuardHandler(r, guardName)
	if !found {
		return nil, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/api %q defines custom guard %q but no linked celerity/handler carries the %s "+
					"annotation naming it; the authorizer has been skipped",
				r.Name, guardName, handler.AnnotationKeyGuardCustom,
			),
			Range: guardRange,
		}
	}

	region, ok := deploymentRegion(ctx)
	if !ok {
		return nil, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/api %q custom guard %q requires a deployment region to build the authorizer "+
					"invoke URI; set \"aws.region\" in the deploy configuration. The authorizer has been skipped",
				r.Name, guardName,
			),
			Range: guardRange,
		}
	}

	// Lambda REQUEST authorizer invoke URI referencing the concrete function.
	uri, err := shared.SubstitutionMappingNode(fmt.Sprintf(
		"arn:aws:apigateway:%s:lambda:path/2015-03-31/functions/${resources.%s.spec.arn}/invocations",
		region, handlerawsserverless.LambdaFuncResourceName(handlerName),
	))
	if err != nil {
		return nil, &core.Diagnostic{
			Level:   core.DiagnosticLevelWarning,
			Message: fmt.Sprintf("celerity/api %q custom guard %q: %s", r.Name, guardName, err.Error()),
			Range:   guardRange,
		}
	}

	spec := core.MappingNodeFields(
		"apiId", apiIDRef,
		"authorizerType", core.MappingNodeFromString("REQUEST"),
		"name", core.MappingNodeFromString(guardName),
		"authorizerUri", uri,
		"authorizerPayloadFormatVersion", core.MappingNodeFromString("2.0"),
		"enableSimpleResponses", core.MappingNodeFromBool(true),
	)

	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/apigatewayv2/authorizer"},
		Spec: spec,
		// Selects the guard's own function so the provider's authorizer::function
		// link runs and grants API Gateway permission to invoke it. Without the
		// link the authorizer still deploys and still names the function, and every
		// route it guards fails with the function never invoked.
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{
				Values: map[string]string{
					handlerawsserverless.LabelKeyGuardTarget: handlerawsserverless.GuardTargetLabelValue(r.Name, guardName),
				},
			},
		},
		Metadata: infraMetadata(r.Name, guardName+" authorizer"),
	}, nil
}

// Finds the linked handler that implements the named custom guard via the
// celerity.handler.guard.custom annotation.
func customGuardHandler(r *api.ResolvedAPI, guardName string) (string, bool) {
	for _, linked := range r.Handlers {
		if linked.Resource == nil {
			continue
		}
		value, ok := transformutils.GetAnnotation(
			linked.Resource,
			handler.AnnotationKeyGuardCustom,
			"",
		)
		if ok && core.StringValue(value) == guardName {
			return linked.Name, true
		}
	}
	return "", false
}

func emitDomain(
	r *api.ResolvedAPI,
	info protocolInfo,
	resources map[string]*schema.Resource,
) ([]*core.Diagnostic, error) {
	domain, ok := pluginutils.GetValueByPath("$.domain", r.Resource.Spec)
	if !ok || domain == nil {
		return nil, nil
	}

	domainName := specNode(domain, "$.domainName")
	domainConfig := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	if cert, ok := pluginutils.GetValueByPath("$.certificateId", domain); ok {
		domainConfig.Fields["certificateArn"] = cert
	}
	if policy, ok := pluginutils.GetValueByPath("$.securityPolicy", domain); ok {
		domainConfig.Fields["securityPolicy"] = policy
	}

	resources[domainResourceName(r.Name)] = &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/apigatewayv2/domainName"},
		Spec: core.MappingNodeFields(
			"domainName", domainName,
			"domainNameConfigurations", core.MappingNodeItems(domainConfig),
		),
		Metadata: infraMetadata(r.Name, r.Name+" domain"),
	}

	return emitAPIMappings(r, info, domain, resources)
}

func emitAPIMappings(
	r *api.ResolvedAPI,
	info protocolInfo,
	domain *core.MappingNode,
	resources map[string]*schema.Resource,
) ([]*core.Diagnostic, error) {
	// A hybrid (HTTP + WebSocket) API maps both protocols onto the single
	// aws/apigatewayv2/domainName. On aws-serverless a custom domain cannot host
	// both protocols at the same base path, so the base paths must be distinct
	// per protocol. Emit an error and skip the mappings when they would collide.
	if info.hasHTTP && info.hasWS {
		httpKeys := mappingKeysForProtocol(domain, protocolHTTP)
		wsKeys := mappingKeysForProtocol(domain, protocolWebSocket)
		if keysCollide(httpKeys, wsKeys) {
			return []*core.Diagnostic{
				{
					Level: core.DiagnosticLevelError,
					Message: fmt.Sprintf(
						"celerity/api %q configures a custom domain for both HTTP and WebSocket protocols, but "+
							"their base paths collide: on aws-serverless a single API Gateway v2 custom domain "+
							"cannot map both protocols at the same base path. Configure protocol-specific base "+
							"paths (a domain.basePaths entry per protocol) or a separate custom domain per protocol",
						r.Name,
					),
					Range: shared.DiagnosticRange(
						shared.NodeMeta(specNode(domain, "$.basePaths")),
						shared.NodeMeta(domain),
						shared.ResourceMeta(r.Resource),
					),
				},
			}, nil
		}
	}

	domainRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.domainName}", domainResourceName(r.Name)),
	)
	if err != nil {
		return nil, err
	}

	for _, protocol := range presentProtocols(info) {
		keys := mappingKeysForProtocol(domain, protocol)
		for index, key := range keys {
			mapping, err := apiMapping(r, protocol, key, domainRef)
			if err != nil {
				return nil, err
			}
			resources[apiMappingResourceName(r.Name, protocol, index)] = mapping
		}
	}
	return nil, nil
}

// keysCollide reports whether any mapping key appears in both protocols' key
// sets — a single custom domain cannot map two APIs at the same base path.
func keysCollide(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, k := range a {
		set[k] = struct{}{}
	}
	for _, k := range b {
		if _, ok := set[k]; ok {
			return true
		}
	}
	return false
}

func apiMapping(
	r *api.ResolvedAPI,
	protocol string,
	mappingKey string,
	domainRef *core.MappingNode,
) (*schema.Resource, error) {
	apiIDRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.apiId}", apiResourceName(r.Name, protocol)),
	)
	if err != nil {
		return nil, err
	}

	// Reference the emitted stage's name so the mapping depends on the stage
	// resource rather than repeating the literal stage name (which leaves no edge).
	stageRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.stageName}", stageResourceName(r.Name, protocol)),
	)
	if err != nil {
		return nil, err
	}

	spec := core.MappingNodeFields(
		"apiId", apiIDRef,
		"domainName", domainRef,
		"stage", stageRef,
	)
	if mappingKey != "" {
		spec.Fields["apiMappingKey"] = core.MappingNodeFromString(mappingKey)
	}

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/apigatewayv2/apiMapping"},
		Spec:     spec,
		Metadata: infraMetadata(r.Name, r.Name+" api mapping"),
	}, nil
}

// A base path of "/" (or empty) maps to the root key "".
//
// domain.normalizeBasePath is intentionally not applied on aws-serverless: it is a
// Celerity-runtime routing concern (stripping non-alphanumeric characters from the
// framework's own base-path routing), whereas API Gateway apiMapping keys are used
// verbatim (only leading/trailing slashes are trimmed here). It is therefore a
// no-op for this target.
func mappingKeysForProtocol(domain *core.MappingNode, protocol string) []string {
	basePaths, ok := pluginutils.GetValueByPath("$.basePaths", domain)
	if !ok || basePaths == nil || len(basePaths.Items) == 0 {
		return []string{""}
	}

	keys := []string{}
	for _, item := range basePaths.Items {
		if item.Scalar != nil {
			keys = append(keys, normalizeMappingKey(core.StringValue(item)))
			continue
		}
		itemProtocol := core.StringValue(specNode(item, "$.protocol"))
		if itemProtocol == protocol {
			keys = append(keys, normalizeMappingKey(core.StringValue(specNode(item, "$.basePath"))))
		}
	}
	if len(keys) == 0 {
		return []string{""}
	}
	return keys
}

// Builds the spec.id ARN as a derived value referenced by the property map. The
// provider aws/apigatewayv2/api exposes no ARN attribute, so the ID is composed as
// the API Gateway control-plane ARN (arn:aws:apigateway:<region>::/apis/<apiId>),
// which needs only the deployment region and the created API's apiId (control-plane
// ARNs omit the account id). When no region is configured the ARN is approximated
// with an empty region segment and a warning is raised.
func synthesizeIDValue(
	ctx transform.Context,
	r *api.ResolvedAPI,
	info protocolInfo,
) (map[string]*schema.Value, *core.Diagnostic) {
	primary := primaryConcreteName(r, info)
	region, hasRegion := deploymentRegion(ctx)

	var diagnostic *core.Diagnostic
	if !hasRegion && !transformutils.IsValidationContext(ctx) {
		diagnostic = &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/api %q: no \"aws.region\" in the deploy configuration; the spec.id API Gateway "+
					"ARN has been approximated with an empty region segment",
				r.Name,
			),
			Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
		}
	}

	value, err := shared.SubstitutionBlueprintValue(fmt.Sprintf(
		"arn:aws:apigateway:%s::/apis/${resources.%s.spec.apiId}", region, primary,
	))
	if err != nil {
		return nil, &core.Diagnostic{
			Level:   core.DiagnosticLevelWarning,
			Message: fmt.Sprintf("celerity/api %q: failed to synthesise spec.id ARN: %s", r.Name, err.Error()),
			Range:   shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
		}
	}

	return map[string]*schema.Value{idValueName(primary): value}, diagnostic
}

func corsConfigNode(spec *core.MappingNode) *core.MappingNode {
	node, ok := pluginutils.GetValueByPath("$.cors", spec)
	if !ok || node == nil {
		return nil
	}

	// String shorthand: "*" allows all origins.
	if node.Scalar != nil {
		if core.StringValue(node) == "*" {
			return core.MappingNodeFields(
				"allowOrigins", core.MappingNodeItems(core.MappingNodeFromString("*")),
			)
		}
		return nil
	}

	cors := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	// The abstract cors.maxAge is an integer, matching the provider
	// corsConfiguration.maxAge integer, so it passes straight through.
	for _, field := range []string{"allowCredentials", "allowOrigins", "allowMethods", "allowHeaders", "exposeHeaders", "maxAge"} {
		if v, ok := pluginutils.GetValueByPath("$."+field, node); ok {
			cors.Fields[field] = v
		}
	}
	if len(cors.Fields) == 0 {
		return nil
	}
	return cors
}

// Converts a guard's tokenSource into the authorizer identitySource array, mapping
// $.* paths onto API Gateway $request.* expressions and selecting the HTTP source
// when tokenSource is a per-protocol array.
func identitySourceNode(tokenSource *core.MappingNode) *core.MappingNode {
	source, ok := selectHTTPTokenSource(tokenSource)
	if !ok {
		return nil
	}
	expr := identitySourceExpr(source)
	if expr == "" {
		return nil
	}
	return core.MappingNodeItems(core.MappingNodeFromString(expr))
}

func selectHTTPTokenSource(tokenSource *core.MappingNode) (string, bool) {
	if tokenSource == nil {
		return "", false
	}
	if tokenSource.Scalar != nil {
		return core.StringValue(tokenSource), true
	}
	for _, item := range tokenSource.Items {
		if core.StringValue(specNode(item, "$.protocol")) == protocolHTTP {
			return core.StringValue(specNode(item, "$.source")), true
		}
	}
	if len(tokenSource.Items) > 0 {
		return core.StringValue(specNode(tokenSource.Items[0], "$.source")), true
	}
	return "", false
}

func identitySourceExpr(tokenSource string) string {
	switch {
	case strings.HasPrefix(tokenSource, "$.headers."):
		return "$request.header." + strings.TrimPrefix(tokenSource, "$.headers.")
	case strings.HasPrefix(tokenSource, "$.query."):
		return "$request.querystring." + strings.TrimPrefix(tokenSource, "$.query.")
	case strings.HasPrefix(tokenSource, "$.cookies."):
		return "$request.cookie." + strings.TrimPrefix(tokenSource, "$.cookies.")
	default:
		return tokenSource
	}
}

func parseProtocols(spec *core.MappingNode) protocolInfo {
	info := protocolInfo{wsRouteKey: defaultWSRouteKey}
	node, ok := pluginutils.GetValueByPath("$.protocols", spec)
	if !ok || node == nil {
		return info
	}

	for _, item := range node.Items {
		if item.Scalar != nil {
			switch core.StringValue(item) {
			case protocolHTTP:
				info.hasHTTP = true
			case protocolWebSocket:
				info.hasWS = true
			}
			continue
		}
		wsCfg, ok := pluginutils.GetValueByPath("$.websocketConfig", item)
		if !ok || wsCfg == nil {
			continue
		}
		info.hasWS = true
		if rk := core.StringValue(specNode(wsCfg, "$.routeKey")); rk != "" {
			info.wsRouteKey = rk
		}
	}
	return info
}

// Raises scoped warnings when a websocketConfig declares connection-level
// authorization (authStrategy/authGuard). WebSocket connection authorization is
// not wired on aws-serverless, and the "connect" strategy is not supported by
// serverless WebSocket APIs (only "authMessage" is).
func websocketAuthWarnings(r *api.ResolvedAPI) []*core.Diagnostic {
	protocols, ok := pluginutils.GetValueByPath("$.protocols", r.Resource.Spec)
	if !ok || protocols == nil {
		return nil
	}

	var diagnostics []*core.Diagnostic
	for _, item := range protocols.Items {
		wsCfg, ok := pluginutils.GetValueByPath("$.websocketConfig", item)
		if !ok || wsCfg == nil {
			continue
		}
		strategyNode := specNode(wsCfg, "$.authStrategy")
		strategy := core.StringValue(strategyNode)
		guardNode, hasGuard := pluginutils.GetValueByPath("$.authGuard", wsCfg)
		if strategy == "" && !hasGuard {
			continue
		}
		// Anchor to whichever of the two fields the user actually wrote, so the
		// warning lands on the ignored configuration rather than the resource.
		authRange := shared.DiagnosticRange(
			shared.NodeMeta(strategyNode),
			shared.NodeMeta(guardNode),
			shared.NodeMeta(wsCfg),
			shared.ResourceMeta(r.Resource),
		)
		if strategy == "connect" {
			diagnostics = append(diagnostics, &core.Diagnostic{
				Level: core.DiagnosticLevelWarning,
				Message: fmt.Sprintf(
					"celerity/api %q websocketConfig sets authStrategy \"connect\", which is not supported by "+
						"serverless WebSocket APIs (only \"authMessage\" is supported on aws-serverless); the "+
						"WebSocket connection authorization has been ignored",
					r.Name,
				),
				Range: authRange,
			})
			continue
		}
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/api %q declares WebSocket connection authorization (websocketConfig "+
					"authStrategy/authGuard), but WebSocket connection authorization is not wired on "+
					"aws-serverless; the configuration has been ignored",
				r.Name,
			),
			Range: authRange,
		})
	}
	return diagnostics
}

func presentProtocols(info protocolInfo) []string {
	protocols := []string{}
	if info.hasHTTP {
		protocols = append(protocols, protocolHTTP)
	}
	if info.hasWS {
		protocols = append(protocols, protocolWebSocket)
	}
	return protocols
}

func protocolTypeValue(protocol string) string {
	if protocol == protocolWebSocket {
		return "WEBSOCKET"
	}
	return "HTTP"
}

func apiMetadata(r *api.ResolvedAPI) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/api",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
	if r.Resource.Metadata != nil {
		meta.Labels = r.Resource.Metadata.Labels
	}
	return meta
}

// Builds the metadata for a supporting resource emitted for an
// API. The abstract resource name has to be the API's own name and nothing
// else, it is the correlation key back to the abstract resource, so tools
// group the concrete resources by it. Anything describing the individual
// resource belongs in the display name, which is free-form.
func infraMetadata(apiName, displayName string) *schema.Metadata {
	return &schema.Metadata{
		DisplayName: pluginutils.StringToSubstitutions(displayName),
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: apiName,
				AbstractResourceType: "celerity/api",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
}

func deploymentRegion(ctx transform.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.TransformerConfigVariable("aws.region")
	if !ok || v == nil || v.StringValue == nil || *v.StringValue == "" {
		return "", false
	}
	return *v.StringValue, true
}

func specNode(node *core.MappingNode, path string) *core.MappingNode {
	value, _ := pluginutils.GetValueByPath(path, node)
	return value
}

func normalizeMappingKey(path string) string {
	return strings.Trim(path, "/")
}

// The concrete API that carries the spec.id / spec.baseUrl outputs: the HTTP API
// when present, otherwise the WebSocket API.
func primaryConcreteName(r *api.ResolvedAPI, info protocolInfo) string {
	if info.hasWS && !info.hasHTTP {
		return apiResourceName(r.Name, protocolWebSocket)
	}
	return apiResourceName(r.Name, protocolHTTP)
}

func authorizerTargetAPIResName(apiName string, info protocolInfo) string {
	if info.hasHTTP {
		return apiResourceName(apiName, protocolHTTP)
	}
	return apiResourceName(apiName, protocolWebSocket)
}

func apiResourceName(apiName, protocol string) string {
	return fmt.Sprintf("%s_%s_api", apiName, protocol)
}

func stageResourceName(apiName, protocol string) string {
	return fmt.Sprintf("%s_%s_stage", apiName, protocol)
}

// The concrete authorizer name, shared with the handler side which references it
// via spec.authorizerId; the two conventions must stay in sync.
func authorizerResourceName(apiName, guardName string) string {
	return fmt.Sprintf("%s_%s_authorizer", apiName, guardName)
}

func domainResourceName(apiName string) string {
	return fmt.Sprintf("%s_domain", apiName)
}

func apiMappingResourceName(apiName, protocol string, index int) string {
	return fmt.Sprintf("%s_%s_api_mapping_%d", apiName, protocol, index)
}

func idValueName(primaryConcrete string) string {
	return fmt.Sprintf("%s_id_arn", primaryConcrete)
}
