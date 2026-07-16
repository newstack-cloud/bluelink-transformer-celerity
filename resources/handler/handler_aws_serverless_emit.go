package handler

import (
	"context"
	"fmt"
	"maps"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

type awsServerlessEmitter struct {
	buildManifestLoader build.ManifestLoader
}

func newAWSServerlessEmitter(deps *shared.Dependencies) *awsServerlessEmitter {
	return &awsServerlessEmitter{
		buildManifestLoader: deps.BuildManifestLoader,
	}
}

func (e *awsServerlessEmitter) emit(
	ctx context.Context,
	run *transformutils.Run,
	r *ResolvedHandler,
	resPropRewriter transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	name, _ := pluginutils.GetValueByPath("$.handlerName", r.Resource.Spec)
	// `spec.handler` is used as the ID at runtime to allow Celerity SDK
	// implementations to correctly look up the correct handler to handle
	// an event or request.
	handlerID, _ := pluginutils.GetValueByPath("$.handler", r.Resource.Spec)
	celerityRuntime, _ := pluginutils.GetValueByPath("$.runtime", r.Resource.Spec)
	runtime, hasRuntime := getTargetRuntime(
		core.StringValue(celerityRuntime),
		shared.AWSServerless,
	)
	if !hasRuntime {
		return &transformutils.EmitResult{
			Diagnostics: []*core.Diagnostic{
				{
					Level: core.DiagnosticLevelError,
					Message: fmt.Sprintf(
						"unsupported runtime '%s' for deploy target '%s'",
						core.StringValue(celerityRuntime),
						shared.AWSServerless,
					),
					Range: core.DiagnosticRangeFromSourceMeta(celerityRuntime.SourceMeta, nil),
				},
			},
		}, nil
	}

	isValidationCtx := transformutils.IsValidationContext(run.TransformContext)
	memory, _ := pluginutils.GetValueByPath("$.memory", r.Resource.Spec)
	codeLocationInfo, err := e.loadCodeLocationInfo(
		run,
		isValidationCtx,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load code location info: %w", err)
	}

	timeout, _ := pluginutils.GetValueByPath("$.timeout", r.Resource.Spec)

	envInput := &awslambda.EnvInput{
		Platform:      shared.PlatformAWS,
		DeployTarget:  shared.AWSServerless,
		HandlerID:     handlerID,
		EventSource:   string(r.EventSource),
		RoutingTag:    r.RoutingTag,
		HasRoutingTag: r.HasRoutingTag,
		Tracing:       r.TracingEnabled,
		UserEnv:       userEnvMap(r),
	}
	spec := core.MappingNodeFields(
		"functionName", name,
		"handler", core.MappingNodeFromString(codeLocationInfo.handler),
		"runtime", core.MappingNodeFromString(runtime),
		"code", codeLocationInfo.codeSpec,
		"memorySize", memory,
		"timeout", timeout,
		"environment", core.MappingNodeFields(
			"variables",
			awslambda.BuildEnvironmentVariables(envInput),
		),
		"tags", sharedaws.SpecTagsFromResourceMetadata(
			r.Resource.Metadata,
		),
	)

	if r.TracingEnabled {
		spec.Fields["tracingConfig"] = core.MappingNodeFields(
			"mode",
			core.MappingNodeFromString("Active"),
		)
	}
	// vpcConfig is intentionally NOT emitted: the aws/flex/vpc::aws/lambda/function
	// link populates it from the VPC's resolved subnets/security group. The
	// transformer only stamps the subnet-type placement annotation (see
	// stampTriggerAnnotations).

	plan := r.awsRolePlan()
	fingerprint := plan.Fingerprint()

	roleRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf(
			"${resources.%s.spec.arn}",
			iamRoleResourceName(fingerprint),
		),
	)
	if err != nil {
		return nil, err
	}
	spec.Fields["role"] = roleRef

	err = e.wireLayerForHandler(run, name, isValidationCtx, spec)
	if err != nil {
		return nil, err
	}

	rewrittenSpec := subwalk.WalkMappingNode(
		spec,
		transformutils.RewriteResourcePropertyRefs(resPropRewriter),
	)

	lambdaResource := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{
			Value: "aws/lambda/function",
		},
		Spec: rewrittenSpec,
		Metadata: &schema.Metadata{
			Annotations: transformutils.TransformerBaseAnnotations(
				&transformutils.TransformerBaseAnnotationsInput{
					AbstractResourceName: r.Name,
					AbstractResourceType: "celerity/handler",
					ResourceCategory:     transformutils.ResourceCategoryCodeHosting,
				},
			),
		},
	}
	funcResourceName := lambdaFuncResourceName(r.Name)

	// Preserve the handler's linkSelector onto the Lambda so the provider's
	// outbound links (queue, topic, datastore, bucket, config) resolve against
	// the concrete resources by label.
	declareOutboundLinks(r, lambdaResource)

	// Carry the union of the handler's and every absorbed consumer's labels so
	// inbound source/api/subscription links resolve against the function.
	applyLambdaLabels(r, lambdaResource)

	// Stamp VPC-placement and consumer event-source link annotations.
	diagnostics := stampTriggerAnnotations(r, lambdaResource)

	// Stamp the API Gateway route/auth link annotations for HTTP/WebSocket
	// handlers so the aws/apigatewayv2/api::function link creates the route.
	if err := stampAPIRouteAnnotations(r, lambdaResource); err != nil {
		return nil, err
	}

	// Stamp the ElastiCache authMode annotation for iam-mode caches so the
	// aws/lambda/function::aws/elasticache/replicationGroup link grants
	// elasticache:Connect.
	stampCacheAnnotations(r, lambdaResource)

	resources := map[string]*schema.Resource{
		funcResourceName: lambdaResource,
	}

	// Absorbed schedules emit an aws/events/rule targeting the function.
	scheduleRules, err := emitScheduleRules(r, funcResourceName)
	if err != nil {
		return nil, err
	}
	rewriteTriggerSpecs(scheduleRules, resPropRewriter)
	maps.Copy(resources, scheduleRules)

	// Celerity-topic consumers emit an aws/sns/subscription delivering to the function.
	subscriptions, err := emitConsumerSubscriptions(r, funcResourceName)
	if err != nil {
		return nil, err
	}
	rewriteTriggerSpecs(subscriptions, resPropRewriter)
	maps.Copy(resources, subscriptions)

	derivedValues := e.createDerivedValues(funcResourceName, handlerID, celerityRuntime, r)

	result := &transformutils.EmitResult{
		Resources:     resources,
		DerivedValues: derivedValues,
		// No shared-parent contributions: the IAM role seed is complete (provider
		// links inject per-link statements), and the layer seed carries its full
		// compatibleRuntimes union.
		SharedParentContributions: map[string]*core.MappingNode{},
		Diagnostics:               diagnostics,
	}

	return result, nil
}

// rewriteTriggerSpecs walks each emitted trigger/fan-out/ESM resource's spec
// through the chained resource-property rewriter, exactly as the handler's own
// Lambda spec is rewritten. This resolves any abstract ${resources.<x>...}
// reference an absorbed trigger carries into its concrete form — most importantly
// a consumer whose topic source is an in-blueprint celerity/topic, whose SNS
// subscription topicArn must point at the concrete <topic>_sns_topic rather than
// the abstract topic name (which no resource in the transformed blueprint owns).
func rewriteTriggerSpecs(
	resources map[string]*schema.Resource,
	resPropRewriter transformutils.ResourcePropertyRewriter,
) {
	visitor := transformutils.RewriteResourcePropertyRefs(resPropRewriter)
	for _, resource := range resources {
		if resource == nil || resource.Spec == nil {
			continue
		}
		resource.Spec = subwalk.WalkMappingNode(resource.Spec, visitor)
	}
}

func userEnvMap(r *ResolvedHandler) map[string]*core.MappingNode {
	vars, _ := pluginutils.GetValueByPath("$.environmentVariables", r.Resource.Spec)
	if vars == nil {
		return nil
	}

	return vars.Fields
}

func (e *awsServerlessEmitter) createDerivedValues(
	funcResourceName string,
	handlerID *core.MappingNode,
	celerityRuntime *core.MappingNode,
	r *ResolvedHandler,
) map[string]*schema.Value {
	codeLocation, _ := pluginutils.GetValueByPath("$.codeLocation", r.Resource.Spec)

	codeLocationKey := fmt.Sprintf("%s_code_location", funcResourceName)
	runtimeKey := fmt.Sprintf("%s_celerity_runtime", funcResourceName)
	handlerIDKey := fmt.Sprintf("%s_handler_id", funcResourceName)
	tracingEnabledKey := fmt.Sprintf("%s_tracing_enabled", funcResourceName)

	return map[string]*schema.Value{
		// Code location key may be an empty string.
		codeLocationKey:   shared.LiteralStringBlueprintValue(core.StringValue(codeLocation)),
		runtimeKey:        shared.LiteralStringBlueprintValue(core.StringValue(celerityRuntime)),
		handlerIDKey:      shared.LiteralStringBlueprintValue(core.StringValue(handlerID)),
		tracingEnabledKey: shared.LiteralBoolBlueprintValue(r.TracingEnabled),
	}
}

type codeLocationInfo struct {
	codeSpec *core.MappingNode
	handler  string
}

func (e *awsServerlessEmitter) loadCodeLocationInfo(
	run *transformutils.Run,
	isValidationCtx bool,
) (*codeLocationInfo, error) {
	if isValidationCtx {
		// During validation, a build manifest won't be provided or available,
		// so we return a placeholder code location.
		return placeholderCodeLocationInfo(), nil
	}

	manifest, hasManifest := transformutils.Use[*build.Manifest](run)
	if !hasManifest {
		return nil, fmt.Errorf("no build manifest available in context")
	}

	return &codeLocationInfo{
		handler: manifest.Lambda.EntryPoint,
		codeSpec: core.MappingNodeFields(
			"s3Bucket", core.MappingNodeFromString(manifest.Lambda.AppCode.S3Bucket),
			"s3Key", core.MappingNodeFromString(manifest.Lambda.AppCode.S3Key),
		),
	}, nil
}

func placeholderCodeLocationInfo() *codeLocationInfo {
	return &codeLocationInfo{
		codeSpec: core.MappingNodeFields(
			"s3Bucket", core.MappingNodeFromString("placeholder-bucket"),
			"s3Key", core.MappingNodeFromString("placeholder-key"),
		),
		handler: "placeholder.handler",
	}
}

func (e *awsServerlessEmitter) wireLayerForHandler(
	run *transformutils.Run,
	handlerName *core.MappingNode,
	isValidationCtx bool,
	targetSpec *core.MappingNode,
) error {
	if isValidationCtx {
		// During validation, a build manifest won't be provided or available,
		// so we skip wiring the layer for the handler.
		return nil
	}

	manifest, hasManifest := transformutils.Use[*build.Manifest](run)
	if !hasManifest {
		return fmt.Errorf("no build manifest available in context")
	}

	hash, _ := awslambda.SelectLayerForHandler(
		core.StringValue(handlerName),
		manifest,
	)
	if hash != "" {
		layerRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.layerVersionArn}", lambdaLayerResourceName(hash)))
		if err != nil {
			return err
		}
		targetSpec.Fields["layers"] = core.MappingNodeItems(layerRef)
	}

	return nil
}

func lambdaFuncResourceName(handlerName string) string {
	return fmt.Sprintf("%s_lambda_func", handlerName)
}
