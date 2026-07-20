package schedule

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func scheduleResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "ScheduleDefinition",
		Description: "A scheduled rule to trigger handlers at a specific time or interval " +
			"based on a schedule.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"schedule": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "A cron or rate expression that defines the schedule for the rule to " +
					"trigger handlers. The expected format follows the Amazon EventBridge cron and " +
					"rate expression syntax; this is converted into the appropriate format for the " +
					"target environment at build/deploy time.",
			},
			"input": scheduleInputSchema(),
		},
		Required: []string{"schedule"},
	}
}

// The maximum number of nested array/object container levels accepted in the
// schedule input value. The schema graph must stay acyclic because it is
// serialised to protobuf (a tree) when the plugin serves its definitions,
// a self-referential schema overflows the stack in the plugin-framework
// converter so unbounded nesting cannot be expressed and the union is
// unrolled to this fixed depth instead.
const scheduleInputMaxNestingDepth = 5

// Models the optional spec.input field, a static JSON value delivered to the
// schedule handler on every trigger (spec type object | string | number | boolean
// | array | null). The provider schema has no free-form "any" type, so it is
// modelled as the most permissive representation the framework allows: a union of
// every scalar plus array and map (object) members nested up to a fixed depth.
func scheduleInputSchema() *provider.ResourceDefinitionsSchema {
	schema := jsonValueSchema(scheduleInputMaxNestingDepth)
	schema.Description = "A static JSON value that is delivered to the schedule handler on every " +
		"trigger. This can be used to pass configuration data, task identifiers, or any " +
		"context the handler needs to determine what work to perform. The value may be an " +
		"object, string, number, boolean, array or null, with arrays and objects nested up " +
		"to 5 levels deep."
	return schema
}

func jsonValueSchema(depth int) *provider.ResourceDefinitionsSchema {
	members := []*provider.ResourceDefinitionsSchema{
		{Type: provider.ResourceDefinitionsSchemaTypeString},
		{Type: provider.ResourceDefinitionsSchemaTypeInteger},
		{Type: provider.ResourceDefinitionsSchemaTypeFloat},
		{Type: provider.ResourceDefinitionsSchemaTypeBoolean},
	}
	if depth > 1 {
		nested := jsonValueSchema(depth - 1)
		members = append(
			members,
			&provider.ResourceDefinitionsSchema{
				Type:  provider.ResourceDefinitionsSchemaTypeArray,
				Items: nested,
			},
			&provider.ResourceDefinitionsSchema{
				Type:      provider.ResourceDefinitionsSchemaTypeMap,
				MapValues: nested,
			},
		)
	}
	return &provider.ResourceDefinitionsSchema{
		Type:        provider.ResourceDefinitionsSchemaTypeUnion,
		Nullable:    true,
		Description: "A JSON value: a string, number, boolean, null, or a nested array or object.",
		OneOf:       members,
	}
}
