package shared

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
)

// SubstitutionMappingNode parses an expression that may contain ${...} references
// (e.g. "${resources.celerityLambdaExec_uniqueId.spec.arn}") into a MappingNode
// carrying real substitutions, so the deploy engine resolves it at deploy time.
// pluginutils.StringToSubstitutions can't be used here: it wraps the whole string
// as a single literal and never parses ${...}.
func SubstitutionMappingNode(expr string) (*core.MappingNode, error) {
	parsed, err := substitutions.ParseSubstitutionValues("", expr, nil, false, true, 0)
	if err != nil {
		return nil, err
	}
	return &core.MappingNode{
		StringWithSubstitutions: &substitutions.StringOrSubstitutions{
			Values: parsed,
		},
	}, nil
}
