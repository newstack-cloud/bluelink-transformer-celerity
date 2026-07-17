package links

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// handlerEventSourceMarkers are the mutually-exclusive event-source annotations a
// handler may carry. A handler serves exactly one event source; carrying more than
// one would emit conflicting triggers.
var handlerEventSourceMarkers = []struct {
	key   string
	label string
}{
	{handler.AnnotationKeyHTTPHandler, "an HTTP handler"},
	{handler.AnnotationKeyWebSocketHandler, "a WebSocket handler"},
	{handler.AnnotationKeyScheduleHandler, "a schedule handler"},
	{handler.AnnotationKeyConsumerHandler, "a consumer handler"},
}

// handlerEventSourceKeys lists the annotation keys of the mutually-exclusive
// markers, derived from handlerEventSourceMarkers so the diagnostic never drifts
// from the set actually checked.
func handlerEventSourceKeys() string {
	keys := make([]string, len(handlerEventSourceMarkers))
	for i, marker := range handlerEventSourceMarkers {
		keys[i] = marker.key
	}
	return strings.Join(keys, ", ")
}

// validateHandlerTargetSingleEventSource resolves the handler at the link target
// and errors if it carries more than one event-source marker (for example both a
// schedule and an HTTP handler). It is shared by the api/schedule/consumer ->
// handler links so a conflict is caught regardless of which event source links in.
func validateHandlerTargetSingleEventSource(
	_ context.Context,
	input *transformerv1.AbstractLinkValidateInput,
) (*transformerv1.AbstractLinkValidateOutput, error) {
	handlerRes, _, ok := input.LinkGraph.Resource(input.Edge.Target)
	if !ok || handlerRes == nil {
		return &transformerv1.AbstractLinkValidateOutput{}, nil
	}
	if diag := handlerEventSourceConflict(handlerRes); diag != nil {
		return &transformerv1.AbstractLinkValidateOutput{Diagnostics: []*core.Diagnostic{diag}}, nil
	}
	return &transformerv1.AbstractLinkValidateOutput{}, nil
}

// handlerEventSourceConflict returns an error diagnostic when a handler carries
// more than one mutually-exclusive event-source marker, naming the ones in
// conflict. Returns nil when zero or one marker is set.
func handlerEventSourceConflict(handlerRes *schema.Resource) *core.Diagnostic {
	var present []string
	for _, marker := range handlerEventSourceMarkers {
		if _, ok := transformutils.GetAnnotation(handlerRes, marker.key, ""); ok {
			present = append(present, marker.label)
		}
	}
	if len(present) <= 1 {
		return nil
	}
	sort.Strings(present)
	return &core.Diagnostic{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"a celerity/handler serves a single event source, but this handler is marked as %s; set "+
				"only one of the %s annotations",
			strings.Join(present, ", "), handlerEventSourceKeys(),
		),
	}
}
