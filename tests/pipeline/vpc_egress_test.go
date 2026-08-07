//go:build unit

package pipeline

import (
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// The provider annotation the transformer stamps a resolved reach onto.
const egressAnnotationKey = "aws.flexvpc.lambda.egress"

func TestPipelineVPCEgressAnnotationReachesTheFunction(t *testing.T) {
	h := Setup(t)
	params := pipelineParams(h, ManifestPath(), awsRegionTransformerConfig(), nil)
	transformed := h.Transform(t, "vpc_egress.blueprint", params)

	reaching := transformed.Resources.Values["egressHandler_lambda_func"]
	require.NotNil(t, reaching, "expected the placed handler's lambda")
	require.Equal(
		t,
		"10.20.0.0/16",
		renderStringSubstitutions(reaching.Metadata.Annotations.Values[egressAnnotationKey]),
		"the declared destination list must reach the function verbatim",
	)

	quiet := transformed.Resources.Values["quietHandler_lambda_func"]
	require.NotNil(t, quiet, "expected the second handler's lambda")
	require.NotContains(
		t,
		quiet.Metadata.Annotations.Values,
		egressAnnotationKey,
		"a handler that declares no egress must not carry the annotation at all",
	)

	// Both are still placed; only the reach differs between them.
	require.Equal(t, "private", renderStringSubstitutions(
		reaching.Metadata.Annotations.Values["aws.flexvpc.lambda.subnetType"]))
	require.Equal(t, "private", renderStringSubstitutions(
		quiet.Metadata.Annotations.Values["aws.flexvpc.lambda.subnetType"]))
}

func TestPipelineVPCEgressUnsatisfiableIsReported(t *testing.T) {
	h := Setup(t)
	params := pipelineParams(h, ManifestPath(), awsRegionTransformerConfig(), nil)
	result, err := h.Validate(t, "vpc_egress_unsatisfiable.blueprint", params)
	require.NoError(t, err, "the transform itself should succeed:\n%s", FormatLoadError(err))
	require.NotNil(t, result)

	errorDiags := diagnosticsAtLevel(result.Diagnostics, core.DiagnosticLevelError)
	require.NotEmpty(
		t,
		errorDiags,
		"expected an error diagnostic for a reach the topology cannot deliver, got: %s",
		renderDiagnostics(result.Diagnostics),
	)

	require.True(
		t,
		anyDiagnosticContains(errorDiags, "provides no outbound path"),
		"the diagnostic should say what the network cannot do, got: %s",
		renderDiagnostics(errorDiags),
	)
	require.True(
		t,
		anyDiagnosticContains(errorDiags, "reachingHandler"),
		"the diagnostic should name the handler it is about, got: %s",
		renderDiagnostics(errorDiags),
	)

	// Diagnostics without a range are anchored to the start of the document by
	// every consumer, which is the quiet failure the range-coverage test exists to
	// prevent. Assert it on a real one rather than trusting the source scan alone.
	for _, diagnostic := range errorDiags {
		require.NotNil(
			t,
			diagnostic.Range,
			"a diagnostic without a range points at the wrong place in the document",
		)
	}
}

func diagnosticsAtLevel(
	diagnostics []*core.Diagnostic,
	level core.DiagnosticLevel,
) []*core.Diagnostic {
	matched := []*core.Diagnostic{}
	for _, diagnostic := range diagnostics {
		if diagnostic.Level == level {
			matched = append(matched, diagnostic)
		}
	}

	return matched
}

func anyDiagnosticContains(diagnostics []*core.Diagnostic, substr string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, substr) {
			return true
		}
	}

	return false
}

func renderDiagnostics(diagnostics []*core.Diagnostic) string {
	if len(diagnostics) == 0 {
		return "(none)"
	}

	rendered := []string{}
	for _, diagnostic := range diagnostics {
		rendered = append(rendered, diagnostic.Message)
	}

	return strings.Join(rendered, " | ")
}
