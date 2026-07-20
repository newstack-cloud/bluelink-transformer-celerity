//go:build unit

package pipeline

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Proves the harness wiring itself: a single-handler blueprint travels the whole
// container pipeline and the change set plans the concrete resources the
// transformer emitted (lambda function + shared execution role).
func TestPipelineHarnessWiring(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "single_handler.blueprint")

	names := slices.Collect(maps.Keys(result.Changes.NewResources))
	require.Contains(t, names, "saveOrder_lambda_func",
		"expected the handler's lambda function in the planned changes")
	require.True(
		t,
		slices.ContainsFunc(names, func(n string) bool {
			return strings.HasPrefix(n, "celerityLambdaExec_")
		}),
		"expected a shared execution role in the planned changes; got: %v", names,
	)
}
