//go:build unit

package handler

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testHandlerName = "myHandler"

func Test_Resolve_applies_schema_defaults_for_unset_optional_fields(t *testing.T) {
	// runtime is required after inheritance, so a minimal valid handler still
	// sets it; the remaining optional fields fall back to schema defaults.
	resolved := resolveForTest(
		t,
		baseHandler("runtime", core.MappingNodeFromString("nodejs24.x")),
		emptyGraph(),
		nil,
	)

	assert.Equal(t, 512, specInt(resolved, "memory"))
	assert.Equal(t, 30, specInt(resolved, "timeout"))
	assert.False(t, specBool(resolved, "tracingEnabled"))
	assert.Equal(t, "nodejs24.x", specString(resolved, "runtime"))
	assert.NotContains(t, resolved.Resource.Spec.Fields, "codeLocation")
}

func Test_Resolve_returns_fatal_error_when_runtime_unset(t *testing.T) {
	err := resolveErr(t, baseHandler(), emptyGraph(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty runtime")
}

func Test_Resolve_inherits_spec_from_linked_handler_config(t *testing.T) {
	configName := "sharedCfg"
	configSpec := core.MappingNodeFields(
		"memory", core.MappingNodeFromInt(1024),
		"runtime", core.MappingNodeFromString("nodejs24.x"),
	)
	blueprint := blueprintWithResources(map[string]*schema.Resource{
		configName: handlerConfigResource(configSpec),
	})

	resolved := resolveForTest(t, baseHandler(), graphWithHandlerConfig(configName), blueprint)

	assert.Equal(t, 1024, specInt(resolved, "memory"))
	assert.Equal(t, "nodejs24.x", specString(resolved, "runtime"))
	assert.Equal(t, 30, specInt(resolved, "timeout")) // default still applies
	assert.False(t, specBool(resolved, "tracingEnabled"))
}

func Test_Resolve_inherits_spec_from_shared_handler_config(t *testing.T) {
	sharedSpec := core.MappingNodeFields(
		"timeout", core.MappingNodeFromInt(60),
		"runtime", core.MappingNodeFromString("python3.13.x"),
	)

	resolved := resolveForTest(t, baseHandler(), emptyGraph(), blueprintWithSharedConfig(sharedSpec))

	assert.Equal(t, 60, specInt(resolved, "timeout"))
	assert.Equal(t, "python3.13.x", specString(resolved, "runtime"))
	assert.Equal(t, 512, specInt(resolved, "memory")) // default still applies
}

func Test_Resolve_handler_spec_wins_over_config_and_shared(t *testing.T) {
	configName := "sharedCfg"
	blueprint := blueprintWithResources(map[string]*schema.Resource{
		configName: handlerConfigResource(
			core.MappingNodeFields("memory", core.MappingNodeFromInt(1024)),
		),
	})
	blueprint.Metadata = core.MappingNodeFields(
		"sharedHandlerConfig",
		core.MappingNodeFields("memory", core.MappingNodeFromInt(2048)),
	)
	handler := baseHandler(
		"memory", core.MappingNodeFromInt(256),
		"runtime", core.MappingNodeFromString("nodejs24.x"),
	)

	resolved := resolveForTest(t, handler, graphWithHandlerConfig(configName), blueprint)

	assert.Equal(t, 256, specInt(resolved, "memory"))
}

func Test_Resolve_handler_config_wins_over_shared(t *testing.T) {
	configName := "sharedCfg"
	blueprint := blueprintWithResources(map[string]*schema.Resource{
		configName: handlerConfigResource(
			core.MappingNodeFields(
				"memory", core.MappingNodeFromInt(1024),
				"runtime", core.MappingNodeFromString("nodejs24.x"),
			),
		),
	})
	blueprint.Metadata = core.MappingNodeFields(
		"sharedHandlerConfig",
		core.MappingNodeFields("memory", core.MappingNodeFromInt(2048)),
	)

	resolved := resolveForTest(t, baseHandler(), graphWithHandlerConfig(configName), blueprint)

	assert.Equal(t, 1024, specInt(resolved, "memory"))
}

func Test_Resolve_explicit_falsey_handler_value_beats_lower_layer(t *testing.T) {
	configName := "sharedCfg"
	blueprint := blueprintWithResources(map[string]*schema.Resource{
		configName: handlerConfigResource(core.MappingNodeFields(
			"tracingEnabled", core.MappingNodeFromBool(true),
			"memory", core.MappingNodeFromInt(1024),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		)),
	})
	handler := baseHandler(
		"tracingEnabled", core.MappingNodeFromBool(false),
		"memory", core.MappingNodeFromInt(0),
	)

	resolved := resolveForTest(t, handler, graphWithHandlerConfig(configName), blueprint)

	assert.False(t, specBool(resolved, "tracingEnabled"))
	assert.Equal(t, 0, specInt(resolved, "memory"))
}

func Test_Resolve_merges_environment_variables_with_handler_winning(t *testing.T) {
	configName := "sharedCfg"
	blueprint := blueprintWithResources(map[string]*schema.Resource{
		configName: handlerConfigResource(core.MappingNodeFields(
			"environmentVariables", core.MappingNodeFromStringMap(map[string]string{
				"A": "cfg",
				"B": "cfg",
			}),
		)),
	})
	blueprint.Metadata = core.MappingNodeFields(
		"sharedHandlerConfig",
		core.MappingNodeFields(
			"environmentVariables", core.MappingNodeFromStringMap(map[string]string{
				"C": "shared",
				"D": "shared",
			}),
		),
	)
	handler := baseHandler(
		"runtime", core.MappingNodeFromString("nodejs24.x"),
		"environmentVariables", core.MappingNodeFromStringMap(map[string]string{
			"A": "handler",
			"C": "handler",
		}),
	)

	resolved := resolveForTest(t, handler, graphWithHandlerConfig(configName), blueprint)

	env := resolved.Resource.Spec.Fields["environmentVariables"].Fields
	assert.Equal(t, "handler", core.StringValue(env["A"])) // handler wins over config
	assert.Equal(t, "cfg", core.StringValue(env["B"]))
	assert.Equal(t, "handler", core.StringValue(env["C"])) // handler wins over shared
	assert.Equal(t, "shared", core.StringValue(env["D"]))
}

func Test_Resolve_returns_fatal_error_when_spec_is_nil(t *testing.T) {
	resource := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: nil,
	}

	err := resolveErr(t, resource, emptyGraph(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty runtime")
}

func Test_Resource_schema_declares_defaults_and_required_fields(t *testing.T) {
	attributes := Resource(&shared.Dependencies{}).Schema.Attributes

	assert.Equal(t, 512, core.IntValue(attributes["memory"].Default))
	assert.Equal(t, 30, core.IntValue(attributes["timeout"].Default))
	assert.False(t, core.BoolValue(attributes["tracingEnabled"].Default))
	assert.Nil(t, attributes["runtime"].Default)
	assert.Nil(t, attributes["codeLocation"].Default)
	assert.ElementsMatch(
		t,
		[]string{"handlerName", "handler"},
		Resource(&shared.Dependencies{}).Schema.Required,
	)
}

func Test_Resolve_derives_http_event_source_when_api_is_linked(t *testing.T) {
	resolved := resolveForTest(
		t,
		annotatedHandler(AnnotationKeyHTTPHandler),
		graphWithInbound("celerity/api", "myApi"),
		nil,
	)

	assert.Equal(t, EventSourceHTTP, resolved.EventSource)
}

func Test_Resolve_http_handler_without_linked_api_is_fatal(t *testing.T) {
	err := resolveErr(t, annotatedHandler(AnnotationKeyHTTPHandler), emptyGraph(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), AnnotationKeyHTTPHandler)
	assert.Contains(t, err.Error(), "celerity/api")
}

func Test_Resolve_derives_websocket_event_source_when_api_is_linked(t *testing.T) {
	resolved := resolveForTest(
		t,
		annotatedHandler(AnnotationKeyWebSocketHandler),
		graphWithInbound("celerity/api", "myApi"),
		nil,
	)

	assert.Equal(t, EventSourceWebSocket, resolved.EventSource)
}

func Test_Resolve_websocket_handler_without_linked_api_is_fatal(t *testing.T) {
	err := resolveErr(t, annotatedHandler(AnnotationKeyWebSocketHandler), emptyGraph(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "celerity/api")
}

func Test_Resolve_derives_consumer_event_source_when_consumer_is_linked(t *testing.T) {
	resolved := resolveForTest(
		t,
		annotatedHandler(AnnotationKeyConsumerHandler),
		graphWithInbound("celerity/consumer", "myConsumer"),
		nil,
	)

	assert.Equal(t, EventSourceConsumer, resolved.EventSource)
}

func Test_Resolve_consumer_handler_without_linked_consumer_is_fatal(t *testing.T) {
	err := resolveErr(t, annotatedHandler(AnnotationKeyConsumerHandler), emptyGraph(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "celerity/consumer")
}

func Test_Resolve_derives_schedule_event_source_when_schedule_is_linked(t *testing.T) {
	resolved := resolveForTest(
		t,
		annotatedHandler(AnnotationKeyScheduleHandler),
		graphWithInbound("celerity/schedule", "mySchedule"),
		nil,
	)

	assert.Equal(t, EventSourceSchedule, resolved.EventSource)
}

func Test_Resolve_schedule_handler_without_linked_schedule_is_fatal(t *testing.T) {
	err := resolveErr(t, annotatedHandler(AnnotationKeyScheduleHandler), emptyGraph(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "celerity/schedule")
}

func Test_Resolve_defaults_to_custom_event_source_without_annotations(t *testing.T) {
	resolved := resolveForTest(
		t,
		baseHandler("runtime", core.MappingNodeFromString("nodejs24.x")),
		emptyGraph(),
		nil,
	)

	assert.Equal(t, EventSourceCustom, resolved.EventSource)
}

func resolveForTest(
	t *testing.T,
	resource *schema.Resource,
	graph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) *ResolvedHandler {
	t.Helper()
	def := Resource(&shared.Dependencies{})
	resolved, err := def.Resolve(context.Background(), nil, testHandlerName, resource, graph, blueprint)
	require.NoError(t, err)
	handler, ok := resolved.(*ResolvedHandler)
	require.True(t, ok)
	return handler
}

func resolveErr(
	t *testing.T,
	resource *schema.Resource,
	graph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) error {
	t.Helper()
	def := Resource(&shared.Dependencies{})
	_, err := def.Resolve(context.Background(), nil, testHandlerName, resource, graph, blueprint)
	return err
}

func baseHandler(extra ...any) *schema.Resource {
	fields := append([]any{
		"handlerName", core.MappingNodeFromString(testHandlerName),
		"handler", core.MappingNodeFromString("save_order"),
	}, extra...)
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(fields...),
	}
}

func handlerConfigResource(spec *core.MappingNode) *schema.Resource {
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handlerConfig"},
		Spec: spec,
	}
}

func blueprintWithResources(resources map[string]*schema.Resource) *schema.Blueprint {
	return &schema.Blueprint{
		Resources: &schema.ResourceMap{Values: resources},
	}
}

func blueprintWithSharedConfig(spec *core.MappingNode) *schema.Blueprint {
	return &schema.Blueprint{
		Metadata: core.MappingNodeFields("sharedHandlerConfig", spec),
	}
}

func emptyGraph() linktypes.DeclaredLinkGraph {
	return &fakeLinkGraph{}
}

func graphWithHandlerConfig(configName string) linktypes.DeclaredLinkGraph {
	return &fakeLinkGraph{
		from: map[string][]*linktypes.ResolvedLink{
			testHandlerName: {
				{
					Source:     testHandlerName,
					Target:     configName,
					TargetType: "celerity/handlerConfig",
				},
			},
		},
	}
}

// Builds a minimal valid handler (with a runtime so the
// empty-runtime check passes) carrying the given event-source annotations.
func annotatedHandler(annotationKeys ...string) *schema.Resource {
	resource := baseHandler("runtime", core.MappingNodeFromString("nodejs24.x"))
	values := map[string]*substitutions.StringOrSubstitutions{}
	for _, key := range annotationKeys {
		values[key] = pluginutils.StringToSubstitutions("true")
	}
	resource.Metadata = &schema.Metadata{
		Annotations: &schema.StringOrSubstitutionsMap{Values: values},
	}
	return resource
}

// Produces a link graph with a single inbound edge into the
// handler from a source of the given type (an API, consumer or schedule).
func graphWithInbound(sourceType, sourceName string) linktypes.DeclaredLinkGraph {
	return &fakeLinkGraph{
		to: map[string][]*linktypes.ResolvedLink{
			testHandlerName: {
				{
					Source:     sourceName,
					Target:     testHandlerName,
					SourceType: sourceType,
				},
			},
		},
	}
}

func specInt(resolved *ResolvedHandler, key string) int {
	return core.IntValue(resolved.Resource.Spec.Fields[key])
}

func specBool(resolved *ResolvedHandler, key string) bool {
	return core.BoolValue(resolved.Resource.Spec.Fields[key])
}

func specString(resolved *ResolvedHandler, key string) string {
	return core.StringValue(resolved.Resource.Spec.Fields[key])
}

// A minimal DeclaredLinkGraph for driving the resolver via
// Resource().Resolve without building a full blueprint link analysis.
type fakeLinkGraph struct {
	from map[string][]*linktypes.ResolvedLink
	to   map[string][]*linktypes.ResolvedLink
}

func (g *fakeLinkGraph) Edges() []*linktypes.ResolvedLink { return nil }

func (g *fakeLinkGraph) EdgesFrom(name string) []*linktypes.ResolvedLink { return g.from[name] }

func (g *fakeLinkGraph) EdgesTo(name string) []*linktypes.ResolvedLink { return g.to[name] }

func (g *fakeLinkGraph) Resource(string) (*schema.Resource, linktypes.ResourceClass, bool) {
	return nil, "", false
}
