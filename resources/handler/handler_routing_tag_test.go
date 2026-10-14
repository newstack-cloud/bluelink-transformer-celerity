//go:build unit

package handler

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/stretchr/testify/assert"
)

// The tag reaches the function as CELERITY_HANDLER_TAG and is the only route
// from a consumer or schedule event to the method that handles it. A class
// handler is registered in the SDK under a key the extractor emits as
// celerity.handler.tag, and nothing else at runtime identifies which method an
// event belongs to.
func handlerWithAnnotationValues(pairs ...string) *ResolvedHandler {
	values := map[string]*substitutions.StringOrSubstitutions{}
	for i := 0; i+1 < len(pairs); i += 2 {
		values[pairs[i]] = pluginutils.StringToSubstitutions(pairs[i+1])
	}
	return &ResolvedHandler{
		Name: testHandlerName,
		Resource: &schema.Resource{
			Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
			Spec: core.MappingNodeFields(
				"handlerName", core.MappingNodeFromString(testHandlerName),
			),
			Metadata: &schema.Metadata{
				Annotations: &schema.StringOrSubstitutionsMap{Values: values},
			},
		},
	}
}

// A schedule event carries only the rule that fired. Before this, no tag was
// stamped on a scheduled function at all, so the runtime fell back to matching
// on the rule ARN, which is not a key any handler is registered under: the
// function deployed, the rule fired and the handler never ran, reported as an
// ordinary result rather than a failure.
func Test_schedule_handler_is_given_the_registry_tag(t *testing.T) {
	tag, ok := deriveRoutingTag(
		handlerWithAnnotationValues(
			AnnotationKeyHandlerTag, "statsSchedule::scheduledAggregate",
			AnnotationKeyScheduleHandler, "true",
		),
		EventSourceSchedule,
	)

	assert.True(t, ok, "a schedule handler must be stamped with a tag")
	assert.Equal(t, "statsSchedule::scheduledAggregate", tag)
}

// The consumer route is the payload field the runtime routes on, not a registry
// key, so it names no handler. Where both are present the registry key wins.
func Test_registry_tag_wins_over_the_consumer_route(t *testing.T) {
	tag, ok := deriveRoutingTag(
		handlerWithAnnotationValues(
			AnnotationKeyHandlerTag, "userEventsConsumer::handleUserCreated",
			AnnotationKeyConsumerRoute, "user.created",
		),
		EventSourceConsumer,
	)

	assert.True(t, ok)
	assert.Equal(t, "userEventsConsumer::handleUserCreated", tag)
}

// Handlers extracted before the tag annotation existed, and blueprint-first
// handlers that declare a route by hand, keep the behaviour they had.
func Test_consumer_route_is_still_used_when_no_tag_is_present(t *testing.T) {
	tag, ok := deriveRoutingTag(
		handlerWithAnnotationValues(AnnotationKeyConsumerRoute, "user.created"),
		EventSourceConsumer,
	)

	assert.True(t, ok)
	assert.Equal(t, "user.created", tag)
}

// An HTTP or WebSocket event carries its own route, and the adapter matches on
// that, so stamping a tag would only give dispatch a second answer to disagree
// with.
func Test_no_tag_for_event_sources_that_route_on_the_event(t *testing.T) {
	for _, eventSource := range []EventSource{EventSourceHTTP, EventSourceWebSocket} {
		_, ok := deriveRoutingTag(
			handlerWithAnnotationValues(AnnotationKeyHandlerTag, "someTag"),
			eventSource,
		)

		assert.False(t, ok, "%s should not be stamped with a routing tag", eventSource)
	}
}

func Test_no_tag_when_the_annotation_is_absent(t *testing.T) {
	_, ok := deriveRoutingTag(handlerWithAnnotationValues(), EventSourceSchedule)

	assert.False(t, ok)
}
