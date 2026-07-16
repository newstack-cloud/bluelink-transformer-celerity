package handler

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// EventSource represents the source of event triggers for a handler.
type EventSource string

const (
	EventSourceHTTP      EventSource = "http"
	EventSourceWebSocket EventSource = "websocket"
	EventSourceConsumer  EventSource = "consumer"
	EventSourceSchedule  EventSource = "schedule"
	EventSourceCustom    EventSource = "custom"
)

type ResolvedHandler struct {
	Name     string
	Resource *schema.Resource
	// Outbound queue links from this handler.
	Queues []*types.LinkedResource
	// Outbound topic links from this handler.
	Topics []*types.LinkedResource
	// Outbound datastore links from this handler.
	Datastores []*types.LinkedResource
	// Outbound bucket links from this handler.
	Buckets []*types.LinkedResource
	// Outbound cache links from this handler.
	Caches []*types.LinkedResource
	// Outbound config links from this handler.
	Configs []*types.LinkedResource
	// Outbound handler config link from this handler.
	HandlerConfig *types.LinkedResource
	// Outbound SQL database links from this handler.
	SQLDatabases []*types.LinkedResource
	// Absorbed inbound consumers linked to this handler.
	Consumers []*types.LinkedResource
	// Classified event-source bindings for each absorbed consumer, resolved from
	// the link graph and the consumers' specs.
	ConsumerBindings []*ConsumerBinding
	// Absorbed inbound schedules linked to this handler.
	Schedules []*types.LinkedResource
	// Absorbed inbound API link to this handler.
	APILink *types.LinkedResource
	// Network config for the handler, if any.
	VPC            *types.LinkedResource
	EventSource    EventSource
	RoutingTag     string
	HasRoutingTag  bool
	VPCSubnetType  string
	TracingEnabled bool
	// memoised AWS serverless role plan.
	rolePlan *awslambda.RolePlan
}

func (h *ResolvedHandler) ResourceName() string {
	return h.Name
}

func (h *ResolvedHandler) ResourceType() string {
	return "celerity/handler"
}

func (h *ResolvedHandler) awsRolePlan() *awslambda.RolePlan {
	if h.rolePlan == nil {
		p := buildAWSRolePlan(h)
		h.rolePlan = &p
	}

	return h.rolePlan
}

// Captures the handler's link set. Provider links inject their
// own IAM grants into whichever role a function references, so a role may only
// be shared between handlers with identical link sets, otherwise each would
// inherit the other's permissions.
func buildAWSRolePlan(r *ResolvedHandler) awslambda.RolePlan {
	links := []string{}
	add := func(linkType string, linked []*types.LinkedResource) {
		for _, resource := range linked {
			links = append(links, linkType+"::"+resource.Name)
		}
	}

	// Outbound links from the handler.
	add("celerity/queue", r.Queues)
	add("celerity/topic", r.Topics)
	add("celerity/datastore", r.Datastores)
	add("celerity/bucket", r.Buckets)
	add("celerity/cache", r.Caches)
	add("celerity/config", r.Configs)
	add("celerity/sqlDatabase", r.SQLDatabases)

	// Inbound event-source links absorbed by the handler.
	add("celerity/consumer", r.Consumers)
	add("celerity/schedule", r.Schedules)
	if r.APILink != nil {
		links = append(links, "celerity/api::"+r.APILink.Name)
	}

	// Sorted so the fingerprint is order-independent.
	slices.Sort(links)

	subnetType := ""
	if r.VPC != nil {
		subnetType = r.VPCSubnetType
	}

	return awslambda.RolePlan{
		Links:   links,
		Tracing: r.TracingEnabled,
		VPC:     subnetType,
		// External event sources (standalone ESMs) have no provider link to inject
		// source-read IAM, so the seed grants it. Folding them into the plan also
		// keeps the fingerprint (hence role sharing) sensitive to them.
		ExternalSources: externalRoleSources(r),
	}
}

func resolveHandler(
	_ context.Context,
	run *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	resolved := &ResolvedHandler{
		Name:     name,
		Resource: resource,
	}

	for _, edge := range linkGraph.EdgesFrom(name) {
		addOutboundLinkToResolvedHandler(resolved, edge, blueprint)
	}

	for _, edge := range linkGraph.EdgesTo(name) {
		addInboundLinkToResolvedHandler(resolved, edge, blueprint)
	}

	// Classify each absorbed consumer's event source now that all inbound consumer
	// links are collected; the emit reads these bindings to wire the triggers.
	resolveConsumerBindings(resolved, linkGraph, blueprint)

	err := resolveInheritedSpec(resolved, blueprint)
	if err != nil {
		return nil, err
	}

	if resolvedRuntime(resolved) == "" {
		return nil, fmt.Errorf(
			"celerity/handler %q resolves to an empty runtime; "+
				"set spec.runtime on the handler, a linked celerity/handlerConfig, or "+
				"metadata.sharedHandlerConfig",
			name,
		)
	}

	return resolved, nil
}

func resolvedRuntime(resolved *ResolvedHandler) string {
	if resolved.Resource == nil || resolved.Resource.Spec == nil {
		return ""
	}

	runtimeNode, ok := resolved.Resource.Spec.Fields["runtime"]
	if !ok || runtimeNode == nil || runtimeNode.Scalar == nil {
		return ""
	}

	return core.StringValue(runtimeNode)
}

func addOutboundLinkToResolvedHandler(
	target *ResolvedHandler,
	edge *linktypes.ResolvedLink,
	blueprint *schema.Blueprint,
) {
	switch edge.TargetType {
	case "celerity/queue":
		target.Queues = append(
			target.Queues,
			createTargetLinkedResource(edge, blueprint),
		)
	case "celerity/topic":
		target.Topics = append(
			target.Topics,
			createTargetLinkedResource(edge, blueprint),
		)
	case "celerity/datastore":
		target.Datastores = append(
			target.Datastores,
			createTargetLinkedResource(edge, blueprint),
		)
	case "celerity/bucket":
		target.Buckets = append(
			target.Buckets,
			createTargetLinkedResource(edge, blueprint),
		)
	case "celerity/cache":
		target.Caches = append(
			target.Caches,
			createTargetLinkedResource(edge, blueprint),
		)
	case "celerity/config":
		target.Configs = append(
			target.Configs,
			createTargetLinkedResource(edge, blueprint),
		)
	case "celerity/handlerConfig":
		target.HandlerConfig = createTargetLinkedResource(edge, blueprint)
	case "celerity/sqlDatabase":
		target.SQLDatabases = append(
			target.SQLDatabases,
			createTargetLinkedResource(edge, blueprint),
		)
	}
}

func addInboundLinkToResolvedHandler(
	target *ResolvedHandler,
	edge *linktypes.ResolvedLink,
	blueprint *schema.Blueprint,
) {
	switch edge.SourceType {
	case "celerity/consumer":
		target.Consumers = append(
			target.Consumers,
			createSourceLinkedResource(edge, blueprint),
		)
	case "celerity/schedule":
		target.Schedules = append(
			target.Schedules,
			createSourceLinkedResource(edge, blueprint),
		)
	case "celerity/vpc":
		target.VPC = createSourceLinkedResource(edge, blueprint)
	case "celerity/api":
		target.APILink = createSourceLinkedResource(edge, blueprint)
	}
}

func createTargetLinkedResource(
	edge *linktypes.ResolvedLink,
	blueprint *schema.Blueprint,
) *types.LinkedResource {
	return &types.LinkedResource{
		Name:     edge.Target,
		Resource: shared.GetResourceByName(blueprint, edge.Target),
		Edge:     edge,
	}
}

func createSourceLinkedResource(
	edge *linktypes.ResolvedLink,
	blueprint *schema.Blueprint,
) *types.LinkedResource {
	return &types.LinkedResource{
		Name:     edge.Source,
		Resource: shared.GetResourceByName(blueprint, edge.Source),
		Edge:     edge,
	}
}

const environmentVariablesField = "environmentVariables"

// The handler schema is built once so resolveInheritedSpec can read inheritance
// defaults from the resource schema, the single source of truth for default
// values (see handler_resource_schema.go).
var handlerSchema = handlerResourceSchema()

// Fills the handler's spec with values inherited from a
// linked `celerity/handlerConfig` resource, blueprint-level
// metadata.sharedHandlerConfig, and the schema defaults, applied per field from
// highest to lowest precedence:
//  1. Direct spec properties on the handler resource.
//  2. Properties from a linked `celerity/handlerConfig` resource, if any.
//  3. Properties from metadata.sharedHandlerConfig, if defined.
//  4. Schema defaults.
func resolveInheritedSpec(
	target *ResolvedHandler,
	blueprint *schema.Blueprint,
) error {
	handlerSpec := ensureSpecFields(target)
	fallbacks := inheritanceFallbacks(target, blueprint)

	inheritScalar(handlerSpec, "runtime", fallbacks, nil)
	inheritScalar(handlerSpec, "codeLocation", fallbacks, nil)
	inheritScalar(handlerSpec, "memory", fallbacks, schemaDefault("memory"))
	inheritScalar(handlerSpec, "timeout", fallbacks, schemaDefault("timeout"))
	inheritScalar(handlerSpec, "tracingEnabled", fallbacks, schemaDefault("tracingEnabled"))

	eventSource, err := deriveEventSource(target)
	if err != nil {
		return err
	}
	target.EventSource = eventSource

	routingTag, hasRoutingTag := deriveRoutingTag(target, eventSource)
	target.RoutingTag = routingTag
	target.HasRoutingTag = hasRoutingTag

	target.VPCSubnetType = deriveVPCSubnetType(target)

	target.TracingEnabled = core.BoolValue(handlerSpec.Fields["tracingEnabled"])

	if merged := mergeEnvironmentVariables(handlerSpec, fallbacks); merged != nil {
		handlerSpec.Fields[environmentVariablesField] = merged
	}

	return nil
}

func ensureSpecFields(target *ResolvedHandler) *core.MappingNode {
	spec := target.Resource.Spec
	if spec == nil {
		spec = &core.MappingNode{}
		target.Resource.Spec = spec
	}
	if spec.Fields == nil {
		spec.Fields = map[string]*core.MappingNode{}
	}
	return spec
}

func inheritanceFallbacks(
	target *ResolvedHandler,
	blueprint *schema.Blueprint,
) []*core.MappingNode {
	fallbacks := []*core.MappingNode{}
	if cfg := handlerConfigSpec(target); cfg != nil {
		fallbacks = append(fallbacks, cfg)
	}
	if sharedCfg := sharedHandlerConfig(blueprint); sharedCfg != nil {
		fallbacks = append(fallbacks, sharedCfg)
	}
	return fallbacks
}

func handlerConfigSpec(target *ResolvedHandler) *core.MappingNode {
	if target.HandlerConfig == nil || target.HandlerConfig.Resource == nil {
		return nil
	}
	return target.HandlerConfig.Resource.Spec
}

func sharedHandlerConfig(blueprint *schema.Blueprint) *core.MappingNode {
	if blueprint == nil || blueprint.Metadata == nil {
		return nil
	}
	value, ok := pluginutils.GetValueByPath("$.sharedHandlerConfig", blueprint.Metadata)
	if !ok {
		return nil
	}
	return value
}

func inheritScalar(
	target *core.MappingNode,
	key string,
	fallbacks []*core.MappingNode,
	def *core.MappingNode,
) {
	if _, ok := target.Fields[key]; ok {
		return
	}
	for _, src := range fallbacks {
		if value, ok := src.Fields[key]; ok && value != nil {
			target.Fields[key] = value
			return
		}
	}
	if def != nil {
		target.Fields[key] = def
	}
}

func schemaDefault(key string) *core.MappingNode {
	attr, ok := handlerSchema.Attributes[key]
	if !ok {
		return nil
	}
	return attr.Default
}

func mergeEnvironmentVariables(
	handlerSpec *core.MappingNode,
	fallbacks []*core.MappingNode,
) *core.MappingNode {
	merged := map[string]*core.MappingNode{}
	for i := len(fallbacks) - 1; i >= 0; i-- {
		addEnvironmentVariables(merged, fallbacks[i])
	}
	addEnvironmentVariables(merged, handlerSpec)
	if len(merged) == 0 {
		return nil
	}
	return &core.MappingNode{Fields: merged}
}

func addEnvironmentVariables(into map[string]*core.MappingNode, source *core.MappingNode) {
	envVars, ok := source.Fields[environmentVariablesField]
	if !ok || envVars == nil {
		return
	}
	maps.Copy(into, envVars.Fields)
}

func deriveEventSource(resolvedHandler *ResolvedHandler) (EventSource, error) {
	_, isHTTP := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyHTTPHandler,
		"",
	)
	if isHTTP {
		return deriveHTTPEventSource(resolvedHandler)
	}

	_, isWebSocket := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyWebSocketHandler,
		"",
	)
	if isWebSocket {
		return deriveWebSocketEventSource(resolvedHandler)
	}

	_, isConsumer := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyConsumerHandler,
		"",
	)
	if isConsumer {
		return deriveConsumerEventSource(resolvedHandler)
	}

	_, isSchedule := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyScheduleHandler,
		"",
	)
	if isSchedule {
		return deriveScheduleEventSource(resolvedHandler)
	}

	return EventSourceCustom, nil
}

func deriveHTTPEventSource(resolvedHandler *ResolvedHandler) (EventSource, error) {
	return deriveAPIEventSource(
		resolvedHandler,
		AnnotationKeyHTTPHandler,
		EventSourceHTTP,
	)
}

func deriveWebSocketEventSource(resolvedHandler *ResolvedHandler) (EventSource, error) {
	return deriveAPIEventSource(
		resolvedHandler,
		AnnotationKeyWebSocketHandler,
		EventSourceWebSocket,
	)
}

func deriveAPIEventSource(
	resolvedHandler *ResolvedHandler,
	annotationKey string,
	eventSource EventSource,
) (EventSource, error) {
	if resolvedHandler.APILink != nil {
		return eventSource, nil
	}

	return EventSource(""), eventSourceErr(
		resolvedHandler.Name,
		annotationKey,
		"a linked celerity/api",
	)
}

func deriveConsumerEventSource(resolvedHandler *ResolvedHandler) (EventSource, error) {
	if len(resolvedHandler.Consumers) > 0 {
		return EventSourceConsumer, nil
	}

	return EventSource(""), eventSourceErr(
		resolvedHandler.Name,
		AnnotationKeyConsumerHandler,
		"at least one linked celerity/consumer",
	)
}

func deriveScheduleEventSource(resolvedHandler *ResolvedHandler) (EventSource, error) {
	if len(resolvedHandler.Schedules) > 0 {
		return EventSourceSchedule, nil
	}

	return EventSource(""), eventSourceErr(
		resolvedHandler.Name,
		AnnotationKeyScheduleHandler,
		"at least one linked celerity/schedule",
	)
}

func eventSourceErr(handlerName, annotationKey, requires string) error {
	return fmt.Errorf(
		"celerity/handler %q sets the %s annotation but is missing %s",
		handlerName, annotationKey, requires,
	)
}

func deriveRoutingTag(resolvedHandler *ResolvedHandler, eventSource EventSource) (string, bool) {
	if eventSource != EventSourceConsumer {
		return "", false
	}

	route, ok := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyConsumerRoute,
		"",
	)
	if !ok {
		return "", false
	}

	return core.StringValue(route), true
}

func deriveVPCSubnetType(resolvedHandler *ResolvedHandler) string {
	subnetType, ok := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyVPCSubnetType,
		"",
	)
	if !ok {
		return SubnetTypePrivate // Default to private subnets if not specified.
	}

	return core.StringValue(subnetType)
}
