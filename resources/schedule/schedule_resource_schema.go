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
		},
		Required: []string{"schedule"},
	}
}
