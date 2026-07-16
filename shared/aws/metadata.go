package aws

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// SpecTagsFromResourceMetadata converts the labels from the resource metadata
// into a list of {key, value} tag objects for inclusion in an emitted AWS
// resource spec (the shape used by most AWS resources). Returns nil when there
// are no labels.
func SpecTagsFromResourceMetadata(metadata *schema.Metadata) *core.MappingNode {
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

// MapTagsFromResourceMetadata converts the labels from the resource metadata
// into a map-shaped tags mapping node (key -> value) for AWS resources whose
// tags field is a string map rather than a list of {key, value} objects
// (for example aws/ssm/parameter). Returns nil when there are no labels.
func MapTagsFromResourceMetadata(metadata *schema.Metadata) *core.MappingNode {
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
