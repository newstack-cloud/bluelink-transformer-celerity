//go:build unit

package pipeline

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// Every diagnostic the transform emits must carry a source range.
//
// Source positions cross the plugin boundary, so a diagnostic without a range
// is reported against the start of the document by consumers rather than the
// element it is about. That is silent; the diagnostic still appears, just
// pointing at the wrong place. This guards the whole set rather than the
// handful of paths individual tests happen to exercise.
func TestTransformDiagnosticsCarryRanges(t *testing.T) {
	fixtures := []string{
		"composite_app.blueprint",
		"api_two_handlers.blueprint",
		"protocol_scoped_apis.blueprint",
		"queue_consumer.blueprint",
		"queue_topic_forwarder.blueprint",
		"data_family.blueprint",
		"schedule_config.blueprint",
		"handler_inheritance.blueprint",
	}

	checked := 0
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			h := Setup(t)
			params := pipelineParams(h, ManifestPath(), awsRegionTransformerConfig(), nil)

			result, err := h.Validate(t, fixture, params)
			require.NoError(t, err, "validation of %q:\n%s", fixture, FormatLoadError(err))
			require.NotNil(t, result)

			for _, diagnostic := range result.Diagnostics {
				assertDiagnosticPositioned(t, fixture, diagnostic)
			}
			checked += len(result.Diagnostics)
		})
	}

	// A run where no fixture produced a diagnostic would make this vacuous.
	require.Positive(t, checked, "fixtures should produce diagnostics to assert on")
}

func assertDiagnosticPositioned(t *testing.T, fixture string, diagnostic *core.Diagnostic) {
	t.Helper()

	require.NotNil(
		t, diagnostic.Range,
		"%s: diagnostic has no range and would be reported at the start of the document: %s",
		fixture, diagnostic.Message,
	)
	require.NotNil(
		t, diagnostic.Range.Start,
		"%s: diagnostic range has no start position: %s", fixture, diagnostic.Message,
	)
	require.Positive(
		t, diagnostic.Range.Start.Line,
		"%s: diagnostic range starts at line %d, which is not a real source line: %s",
		fixture, diagnostic.Range.Start.Line, diagnostic.Message,
	)
}
