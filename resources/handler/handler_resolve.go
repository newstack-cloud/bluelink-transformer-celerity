package handler

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	VPC *types.LinkedResource
	// The application component the VPC placement was inherited from, empty when
	// the handler is linked to the VPC directly or is not VPC-attached.
	InheritedVPCFrom string
	EventSource      EventSource
	RoutingTag       string
	HasRoutingTag    bool
	// RoutingKey is the message-body field the handler's consumer routes on, set
	// only when this handler serves the whole consumer and so has to route the
	// batch itself. Empty for a handler that serves one route.
	RoutingKey    string
	VPCSubnetType string
	// What the handler may reach outside the VPC, empty when the author set no
	// egress annotation anywhere in the cascade. Empty is passed through as absent
	// rather than as a value, leaving the deploy target to resolve the reach from
	// the network's topology.
	VPCEgress      string
	TracingEnabled bool
	// Subnet tier declared by the component the placement was inherited from,
	// overridden by the handler's own subnet-type annotation.
	inheritedVPCSubnetType string
	// Outbound reach declared by the component the placement was inherited from,
	// overridden by the handler's own egress annotation.
	inheritedVPCEgress string
	// Labels the VPC's selector matched the component on, carried onto the
	// emitted function so the concrete VPC-to-compute link selects it as well.
	inheritedVPCSelectorLabels map[string]string
	// memoised AWS serverless role plan.
	rolePlan *awslambda.RolePlan
	// resourceLinksStorePath is the internal resources config store's SSM path prefix,
	// set when the handler links to a backing resource resolved through the store
	// (queue/topic/datastore/bucket). Drives the store env vars and the role's
	// store-read grant; empty when the handler has no such links.
	resourceLinksStorePath string
}

// hasResourceLinks reports whether the handler links to any backing resource
// whose physical id is resolved at runtime through the internal resources config
// store, which decides both the store env vars and the store-read grant.
//
// This must stay in step with the entries the store actually writes (see the
// transformer package's addHandlerStoreEntries): a handler left out here but
// written into the store gets no namespace and fails at cold start on
// `Config namespace "resources" not registered`, which is how every cache-backed
// handler was broken.
func hasResourceLinks(r *ResolvedHandler) bool {
	return len(r.Queues)+
		len(r.Topics)+
		len(r.Datastores)+
		len(r.Buckets)+
		len(r.Caches)+
		len(r.SQLDatabases) > 0
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
		Links:                  links,
		Tracing:                r.TracingEnabled,
		VPC:                    subnetType,
		ResourceLinksStorePath: r.resourceLinksStorePath,
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

	// Placement can be declared on the application components the handler is
	// bound to rather than on the handler itself, so it is resolved once all
	// inbound component links are known.
	if err := resolveInheritedVPCPlacement(resolved, linkGraph, blueprint); err != nil {
		return nil, err
	}

	// Once backing links are known, record the internal resources config store path
	// so the role plan grants store-read and the emit sets the store env vars.
	if hasResourceLinks(resolved) {
		resolved.resourceLinksStorePath = shared.ResourceLinksStorePath(run)
	}

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
// defaults from the resource schema, the single source of truth for default values.
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
	target.RoutingKey = deriveRoutingKey(target, routingTag, hasRoutingTag)

	target.VPCSubnetType = deriveVPCSubnetType(target)
	target.VPCEgress = deriveVPCEgress(target)

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

// Returns the name of the message-body field the function reads
// to pick a handler per message, taken from the owning consumer's
// spec.routingKey. The emitter stamps it as CELERITY_ROUTING_KEY, and omits the
// variable entirely when this is empty.
//
// Non-empty only for a function serving a whole consumer, identified by its
// routing tag naming the consumer rather than one of its handlers, this is the shape
// the merge step produces when it collapses a routed consumer's handlers into a
// single resource. That is the only case with anything left to decide at
// runtime: a function serving one route was already addressed by its tag, and
// routing it again could only send the message somewhere its tag did not say.
//
// Empty in the three cases that are not that: a handler with no routing tag, a
// schedule (tagged, but it has no consumer bindings to match), and a function
// whose tag names one handler of a consumer.
//
// Defaults to "event" when the owning consumer set no routingKey, matching the
// specification, so a consumer that declared handler routes still routes rather
// than passing every message to the fallback handler.
func deriveRoutingKey(
	resolvedHandler *ResolvedHandler,
	routingTag string,
	hasRoutingTag bool,
) string {
	if !hasRoutingTag {
		return ""
	}

	for _, binding := range resolvedHandler.ConsumerBindings {
		if binding.ConsumerName != routingTag {
			continue
		}

		node, ok := pluginutils.GetValueByPath("$.routingKey", consumerSpec(binding))
		if ok && node != nil && core.StringValue(node) != "" {
			return core.StringValue(node)
		}
		return defaultConsumerRoutingKey
	}

	return ""
}

// Returns the tag a deployed function needs to find its own
// handler at runtime, and whether it has one.
//
// The SDK emits the registry key it registered under as celerity.handler.tag,
// and that is what a consumer or schedule function is stamped with. Deriving the
// key here instead would mean this side guessing at the composition rule the SDK
// applies, in every language it supports.
//
// The consumer route is kept as a fallback for handlers extracted before the tag
// annotation existed and for blueprint-first handlers that declare a route by
// hand. It only ever applied to consumers: a schedule handler was given no tag at
// all, so the runtime fell back to matching on the rule that fired, which is not
// a key anything is registered under, and the handler never ran.
func deriveRoutingTag(resolvedHandler *ResolvedHandler, eventSource EventSource) (string, bool) {
	if eventSource != EventSourceConsumer && eventSource != EventSourceSchedule {
		return "", false
	}

	tag, ok := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyHandlerTag,
		"",
	)
	if ok && core.StringValue(tag) != "" {
		return core.StringValue(tag), true
	}

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
	if ok {
		return core.StringValue(subnetType)
	}

	if resolvedHandler.inheritedVPCSubnetType != "" {
		return resolvedHandler.inheritedVPCSubnetType
	}

	return SubnetTypePrivate
}

// Resolves the handler's outbound reach from its own annotation, falling back to
// the one cascaded from the component the placement came from.
//
// Unlike the subnet tier there is no default here, deliberately. Unset means the
// deploy target resolves the reach from the network's topology, which is the only
// place that knows whether an outbound path exists at all; defaulting it here
// would either promise reach the network cannot deliver or withdraw reach the
// handler had before it was placed. Returning empty is how "the author said
// nothing" reaches the emitter intact.
func deriveVPCEgress(resolvedHandler *ResolvedHandler) string {
	egress, ok := transformutils.GetAnnotation(
		resolvedHandler.Resource,
		AnnotationKeyVPCEgress,
		"",
	)
	if ok {
		return strings.TrimSpace(core.StringValue(egress))
	}

	return strings.TrimSpace(resolvedHandler.inheritedVPCEgress)
}
