package handler

import (
	"context"
	"fmt"
	"maps"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/config"
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
	// `spec.handler` is used as the ID at runtime so Celerity SDK implementations
	// can look up the handler to handle an event or request.
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
	codeLocationInfo, codeDiag, err := e.loadCodeLocationInfo(
		run,
		isValidationCtx,
		r.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load code location info: %w", err)
	}

	timeout, _ := pluginutils.GetValueByPath("$.timeout", r.Resource.Spec)

	userConfigStores, err := userConfigStoreEnv(r)
	if err != nil {
		return nil, err
	}
	envInput := &awslambda.EnvInput{
		Platform:               shared.PlatformAWS,
		DeployTarget:           shared.AWSServerless,
		HandlerID:              handlerID,
		EventSource:            string(r.EventSource),
		RoutingTag:             r.RoutingTag,
		HasRoutingTag:          r.HasRoutingTag,
		Tracing:                r.TracingEnabled,
		ResourceLinksStorePath: r.resourceLinksStorePath,
		UserConfigStores:       userConfigStores,
		UserEnv:                userEnvMap(r),
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

	layerDiag, err := e.wireLayerForHandler(run, name, isValidationCtx, spec)
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

	declareOutboundLinks(r, lambdaResource)

	// Carry the union of the handler's and every absorbed consumer's labels so
	// inbound source/api/subscription links resolve against the function.
	labelDiagnostics := applyLambdaLabels(r, lambdaResource)

	// Stamp VPC-placement and consumer event-source link annotations.
	diagnostics := stampTriggerAnnotations(r, lambdaResource)
	diagnostics = append(diagnostics, labelDiagnostics...)
	if codeDiag != nil {
		diagnostics = append(diagnostics, codeDiag)
	}
	if layerDiag != nil {
		diagnostics = append(diagnostics, layerDiag)
	}

	// Stamp the API Gateway route/auth link annotations for HTTP/WebSocket
	// handlers so the aws/apigatewayv2/api::function link creates the route.
	if err := stampAPIRouteAnnotations(r, lambdaResource); err != nil {
		return nil, err
	}

	// Stamp the ElastiCache authMode annotation for iam-mode caches so the
	// aws/lambda/function::aws/elasticache/replicationGroup link grants
	// elasticache:Connect.
	stampCacheAnnotations(r, lambdaResource)

	// Stamp the RDS authMode annotation for iam-mode SQL databases so the
	// aws/lambda/function::aws/rds/dbProxy (or ::aws/rds/dbCluster) link grants
	// the execution role rds-db:connect.
	stampSQLDatabaseAnnotations(r, run, lambdaResource)

	// Stamp the config-store link annotations for each linked user
	// celerity/config store so the aws/lambda/function::aws/ssm/parameterTree
	// (or ::aws/secretsmanager/secret) link grants read access and maintains the
	// CELERITY_CONFIG_<NS>_STORE_ID env var at deploy time.
	stampConfigStoreAnnotations(r, lambdaResource)

	resources := map[string]*schema.Resource{
		funcResourceName: lambdaResource,
	}

	// Absorbed schedules emit an aws/events/rule targeting the function.
	scheduleRules, scheduleDiags, err := emitScheduleRules(
		r,
		funcResourceName,
		shared.ResolveAppName(run),
	)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, scheduleDiags...)
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

// Walks each emitted trigger/fan-out/ESM resource's spec through the chained
// resource-property rewriter, as the handler's own Lambda spec is rewritten. This
// resolves any abstract ${resources.<x>...} reference an absorbed trigger carries
// into its concrete form — most importantly a consumer whose topic source is an
// in-blueprint celerity/topic, whose SNS subscription topicArn must point at the
// concrete <topic>_sns_topic rather than the abstract topic name (which no resource
// in the transformed blueprint owns).
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

// One CELERITY_CONFIG_<NS>_STORE_ID/_STORE_KIND pair per linked user
// celerity/config store.
// The STORE_ID carries the abstract ${resources.<config>.spec.id} reference the
// resource-property rewriter resolves to the emitted store's derived id value,
// the same rewrite applied to user-authored spec.id references. The internal
// resources namespace keeps its separate direct-literal treatment
// (EnvInput.ResourceLinksStorePath).
func userConfigStoreEnv(r *ResolvedHandler) ([]awslambda.UserConfigStore, error) {
	stores := make([]awslambda.UserConfigStore, 0, len(r.Configs))
	for _, linked := range r.Configs {
		if linked == nil || linked.Resource == nil {
			continue
		}

		wiring := config.StoreWiringFor(linked.Name, linked.Resource)
		storeIDRef := fmt.Sprintf("${resources.%s.spec.id}", linked.Name)
		if wiring.Kind == config.StoreKindSecretsManager {
			storeIDRef = fmt.Sprintf("${resources.%s.spec.id}", wiring.ConcreteResourceName)
		}
		storeID, err := shared.SubstitutionMappingNode(storeIDRef)
		if err != nil {
			return nil, err
		}
		stores = append(stores, awslambda.UserConfigStore{
			EnvNamespace: wiring.EnvNamespace,
			StoreID:      storeID,
			Kind:         wiring.Kind,
		})
	}
	return stores, nil
}

// Stamps the aws.lambda.ssm.<store>.* / aws.lambda.secretsmanager.<store>.*
// annotations for each linked user celerity/config store, keyed by the concrete
// store resource name (the provider resolves annotation keys from
// otherResourceInfo.ResourceName). accessLevel makes the link inject the scoped
// read grant into the execution role, and envVarName renames the link-injected
// store-identifier env var to the CELERITY_CONFIG_<NS>_STORE_ID name the SDK
// runtime contract requires, matching the transformer-stamped value.
func stampConfigStoreAnnotations(r *ResolvedHandler, lambda *schema.Resource) {
	for _, linked := range r.Configs {
		if linked == nil || linked.Resource == nil {
			continue
		}
		wiring := config.StoreWiringFor(linked.Name, linked.Resource)
		prefix := fmt.Sprintf(
			"aws.lambda.%s.%s",
			wiring.LinkAnnotationService,
			wiring.ConcreteResourceName,
		)
		setStringAnnotation(
			lambda.Metadata,
			fmt.Sprintf("%s.envVarName", prefix),
			awslambda.ConfigStoreIDEnvVarName(wiring.EnvNamespace),
		)
		setStringAnnotation(lambda.Metadata, fmt.Sprintf("%s.accessLevel", prefix), "read")
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
	handlerName string,
) (*codeLocationInfo, *core.Diagnostic, error) {
	if isValidationCtx {
		// During validation, a build manifest won't be provided or available,
		// so we return a placeholder code location.
		return placeholderCodeLocationInfo(), nil, nil
	}

	manifest, hasManifest := transformutils.Use[*build.Manifest](run)
	if !hasManifest || manifest.Lambda == nil {
		// Per the build-manifest fallback contract, a missing manifest does not fail the transform: the
		// function is emitted with the same stageable placeholder code/handler
		// values the validation-context path uses, a warning is recorded, and the
		// placeholders fail at deploy time by design. This keeps validation and
		// dry-run/plan working before "celerity build" has run.
		message := fmt.Sprintf(
			"no build manifest available for celerity/handler %q; the function is emitted with "+
				"placeholder code and entry-point values that cannot deploy. Run \"celerity build\" "+
				"before deploying (validation and a dry run/plan are still valid)",
			handlerName,
		)
		if loadErr, hasLoadErr := transformutils.Use[*shared.BuildManifestLoadError](run); hasLoadErr && loadErr.Cause != nil {
			message = fmt.Sprintf("%s: %s", message, loadErr.Cause)
		}
		return placeholderCodeLocationInfo(), &core.Diagnostic{
			Level:   core.DiagnosticLevelWarning,
			Message: message,
		}, nil
	}

	return &codeLocationInfo{
		handler: manifest.Lambda.EntryPoint,
		codeSpec: core.MappingNodeFields(
			"s3Bucket", core.MappingNodeFromString(manifest.Lambda.AppCode.S3Bucket),
			"s3Key", core.MappingNodeFromString(manifest.Lambda.AppCode.S3Key),
		),
	}, nil, nil
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
) (*core.Diagnostic, error) {
	if isValidationCtx {
		// During validation, a build manifest won't be provided or available,
		// so we skip wiring the layer for the handler.
		return nil, nil
	}

	manifest, hasManifest := transformutils.Use[*build.Manifest](run)
	if !hasManifest || manifest.Lambda == nil {
		// No build manifest: skip the layer rather than failing. The missing-code
		// warning from loadCodeLocationInfo already covers this handler.
		return nil, nil
	}

	hash, _ := awslambda.SelectLayerForHandler(
		core.StringValue(handlerName),
		manifest,
	)
	if hash != "" {
		layerRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.layerVersionArn}", lambdaLayerResourceName(hash)))
		if err != nil {
			return nil, err
		}
		targetSpec.Fields["layers"] = core.MappingNodeItems(layerRef)
	}

	return nil, nil
}

func lambdaFuncResourceName(handlerName string) string {
	return fmt.Sprintf("%s_lambda_func", handlerName)
}
