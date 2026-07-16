package shared

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

// StringList retrieves a list of strings from the given
// mapping node value at the specified path.
func StringList(path string, value *core.MappingNode) []string {
	node, _ := pluginutils.GetValueByPath(path, value)
	return core.StringSliceValue(node)
}

// StringSet retrieves a set of strings from the given
// mapping node value at the specified path.
func StringSet(path string, value *core.MappingNode) map[string]struct{} {
	node, _ := pluginutils.GetValueByPath(path, value)
	strings := core.StringSliceValue(node)
	set := make(map[string]struct{}, len(strings))
	for _, s := range strings {
		set[s] = struct{}{}
	}
	return set
}

// LiteralStringBlueprintValue creates a schema.Value representing
// a literal string value for use in a blueprint.
func LiteralStringBlueprintValue(s string) *schema.Value {
	return &schema.Value{
		Type: &schema.ValueTypeWrapper{
			Value: schema.ValueTypeString,
		},
		Value: core.MappingNodeFromString(s),
	}
}

// LiteralBoolBlueprintValue creates a schema.Value representing
// a literal boolean value for use in a blueprint.
func LiteralBoolBlueprintValue(b bool) *schema.Value {
	return &schema.Value{
		Type: &schema.ValueTypeWrapper{
			Value: schema.ValueTypeBoolean,
		},
		Value: core.MappingNodeFromBool(b),
	}
}

// SubstitutionBlueprintValue creates a string-typed schema.Value whose value is
// a parsed ${...} substitution (rather than a literal), for use as a derived
// value that references another resource's property at deploy time.
func SubstitutionBlueprintValue(expr string) (*schema.Value, error) {
	node, err := SubstitutionMappingNode(expr)
	if err != nil {
		return nil, err
	}
	return &schema.Value{
		Type: &schema.ValueTypeWrapper{
			Value: schema.ValueTypeString,
		},
		Value: node,
	}, nil
}
