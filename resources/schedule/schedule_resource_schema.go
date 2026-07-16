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

// Models the optional spec.input field, a static JSON value delivered to the
// schedule handler on every trigger (spec type object | string | number | boolean
// | array | null). The provider schema has no free-form "any" type, so it is
// modelled as the most permissive representation the framework allows: a union of
// every scalar plus recursively-permissive array and map (object) members.
func scheduleInputSchema() *provider.ResourceDefinitionsSchema {
	// anyJSON is self-referential so nested arrays and objects accept arbitrary
	// JSON to any depth (bounded by the framework's traversal depth limit).
	anyJSON := &provider.ResourceDefinitionsSchema{}
	*anyJSON = provider.ResourceDefinitionsSchema{
		Type:     provider.ResourceDefinitionsSchemaTypeUnion,
		Nullable: true,
		Description: "A static JSON value that is delivered to the schedule handler on every " +
			"trigger. This can be used to pass configuration data, task identifiers, or any " +
			"context the handler needs to determine what work to perform. The value may be an " +
			"object, string, number, boolean, array or null. On aws-serverless it is delivered " +
			"in the message body to the handler via the EventBridge rule target input.",
		OneOf: []*provider.ResourceDefinitionsSchema{
			{Type: provider.ResourceDefinitionsSchemaTypeString},
			{Type: provider.ResourceDefinitionsSchemaTypeInteger},
			{Type: provider.ResourceDefinitionsSchemaTypeFloat},
			{Type: provider.ResourceDefinitionsSchemaTypeBoolean},
			{Type: provider.ResourceDefinitionsSchemaTypeArray, Items: anyJSON},
			{Type: provider.ResourceDefinitionsSchemaTypeMap, MapValues: anyJSON},
		},
	}
	return anyJSON
}
