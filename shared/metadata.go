package shared

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// AWSSpecTagsFromResourceMetadata converts the labels from the resource metadata
// into a mapping node of tags for inclusion in the emitted AWS resource spec.
func AWSSpecTagsFromResourceMetadata(metadata *schema.Metadata) *core.MappingNode {
	if metadata == nil ||
		metadata.Labels == nil ||
		len(metadata.Labels.Values) == 0 {
		return nil
	}

	tags := core.MappingNodeItems()
	for key, value := range metadata.Labels.Values {
		tags.Items = append(
			tags.Items,
			core.MappingNodeFields(
				"key", core.MappingNodeFromString(key),
				"value", core.MappingNodeFromString(value),
			),
		)
	}

	return tags
}

// AWSMapTagsFromResourceMetadata converts the labels from the resource metadata
// into a map-shaped tags mapping node (key -> value) for AWS resources whose
// tags field is a string map rather than a list of {key, value} objects
// (for example aws/ssm/parameter). Returns nil when there are no labels.
func AWSMapTagsFromResourceMetadata(metadata *schema.Metadata) *core.MappingNode {
	if metadata == nil ||
		metadata.Labels == nil ||
		len(metadata.Labels.Values) == 0 {
		return nil
	}

	tags := core.MappingNodeFields()
	for key, value := range metadata.Labels.Values {
		tags.Fields[key] = core.MappingNodeFromString(value)
	}

	return tags
}

// ResolveAppName retrieves the app name from the transform context, if available.
func ResolveAppName(run *transformutils.Run) string {
	if run == nil || run.TransformContext == nil {
		return ""
	}

	ctxVar, _ := run.TransformContext.ContextVariable(AppNameContextVarKey)
	appName := core.StringValueFromScalar(ctxVar)
	return appName
}
