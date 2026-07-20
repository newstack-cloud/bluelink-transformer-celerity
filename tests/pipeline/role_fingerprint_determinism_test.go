//go:build unit

package pipeline

import (
	"slices"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/stretchr/testify/require"
)

// TestRoleFingerprintDeterminism transforms the same blueprint repeatedly and
// asserts the emitted execution-role resource names never change. The role
// fingerprint is the role-sharing key and part of the resource name, so any
// nondeterminism (map-iteration ordering feeding the plan) makes every
// restage of an unchanged blueprint plan a role replacement.
func TestRoleFingerprintDeterminism(t *testing.T) {
	h := Setup(t)
	params := h.Params(ManifestPath(), nil)

	fixtures := []string{"schedule_config.blueprint", "queue_consumer.blueprint"}
	for _, fixture := range fixtures {
		baseline := roleResourceNames(h.Transform(t, fixture, params))
		require.NotEmptyf(t, baseline, "no execution roles emitted for %s", fixture)
		for i := range 25 {
			names := roleResourceNames(h.Transform(t, fixture, params))
			require.Equalf(t, baseline, names,
				"role fingerprints for %s changed between transforms (iteration %d)",
				fixture, i)
		}
	}
}

func roleResourceNames(bp *schema.Blueprint) []string {
	names := []string{}
	if bp.Resources == nil {
		return names
	}
	for name := range bp.Resources.Values {
		if strings.HasPrefix(name, "celerityLambdaExec_") {
			names = append(names, name)
		}
	}
	// Sorted so a multi-role fixture cannot flake on map iteration order.
	slices.Sort(names)
	return names
}
