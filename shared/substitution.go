package shared

import (
	"strings"

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

// HasStringValue reports whether a string-valued field was supplied, counting a
// field whose value is a substitution as supplied.
//
// A validation check written as core.StringValue(node) == "" reads an
// unresolved substitution as an empty string, because the value lives in
// StringWithSubstitutions and is not resolved until deploy time. Fields the
// author did set, such as issuer = variables.oidcIssuer, are then reported as
// missing and the blueprint is rejected before it can ever be deployed.
func HasStringValue(node *core.MappingNode) bool {
	if node == nil {
		return false
	}

	if IsSubstitution(node) {
		return true
	}

	if node.StringWithSubstitutions != nil {
		for _, value := range node.StringWithSubstitutions.Values {
			if value.StringValue != nil && strings.TrimSpace(*value.StringValue) != "" {
				return true
			}
		}
		return false
	}

	return strings.TrimSpace(core.StringValue(node)) != ""
}

// DerivedName extends a base name with a literal suffix, keeping a reference in
// the base intact.
//
// Sibling resources are named after the resource they belong to (a cache's
// subnet group is "<cache>-cache-subnets"). Building those with fmt.Sprintf
// resolves the base through core.StringValue first, so a referenced name
// collapses to a suffix with nothing in front of it.
func DerivedName(base *core.MappingNode, suffix string) *core.MappingNode {
	if base == nil {
		return nil
	}

	if base.StringWithSubstitutions != nil {
		return AppendLiteral(base.StringWithSubstitutions, suffix)
	}

	return core.MappingNodeFromString(core.StringValue(base) + suffix)
}

// IsSubstitution reports whether a field's value contains a ${...} reference,
// which the deploy engine does not resolve until deploy time.
//
// A node can hold substitution values that are all literal segments, so the
// presence of StringWithSubstitutions alone does not mean the value is deferred.
func IsSubstitution(node *core.MappingNode) bool {
	if node == nil || node.StringWithSubstitutions == nil {
		return false
	}

	for _, value := range node.StringWithSubstitutions.Values {
		if value.SubstitutionValue != nil {
			return true
		}
	}

	return false
}

// AppendLiteral returns a new node with a literal segment appended to the
// substitution string, leaving the abstract spec node untouched.
func AppendLiteral(s *substitutions.StringOrSubstitutions, literal string) *core.MappingNode {
	values := make([]*substitutions.StringOrSubstitution, 0, len(s.Values)+1)
	values = append(values, s.Values...)
	values = append(values, &substitutions.StringOrSubstitution{StringValue: &literal})
	return &core.MappingNode{
		StringWithSubstitutions: &substitutions.StringOrSubstitutions{
			Values:     values,
			SourceMeta: s.SourceMeta,
		},
	}
}

// EndsWithLiteral reports whether a substitution string ends in a literal
// segment with the given suffix. False for one ending in a reference, whose
// value is not known until the deploy engine resolves it.
func EndsWithLiteral(s *substitutions.StringOrSubstitutions, literal string) bool {
	if len(s.Values) == 0 {
		return false
	}
	last := s.Values[len(s.Values)-1]

	return last.StringValue != nil && strings.HasSuffix(*last.StringValue, literal)
}

// PhysicalNameWithSuffix maps an authored spec.name onto a deployed identifier
// that must carry a suffix, such as the ".fifo" an ordered queue or topic needs.
//
// A substitution-valued name is passed through with the suffix appended rather
// than stringified, so the deploy engine resolves it instead of the name
// silently becoming "". Returns nil when no name is set, so the deployed name
// auto-generates.
func PhysicalNameWithSuffix(
	nameNode *core.MappingNode,
	required bool,
	suffix string,
) *core.MappingNode {
	if nameNode == nil {
		return nil
	}

	if nameNode.StringWithSubstitutions != nil {
		if !required || EndsWithLiteral(nameNode.StringWithSubstitutions, suffix) {
			return nameNode
		}

		return AppendLiteral(nameNode.StringWithSubstitutions, suffix)
	}

	name := core.StringValue(nameNode)
	if name == "" {
		return nil
	}

	if required && !strings.HasSuffix(name, suffix) {
		name += suffix
	}

	return core.MappingNodeFromString(name)
}
