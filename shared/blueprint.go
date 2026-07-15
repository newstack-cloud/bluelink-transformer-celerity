package shared

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// GetResourceByName retrieves a resource from the given blueprint by name.
func GetResourceByName(blueprint *schema.Blueprint, name string) *schema.Resource {
	if blueprint == nil || blueprint.Resources == nil {
		return nil
	}

	resource, ok := blueprint.Resources.Values[name]
	if !ok {
		return nil
	}

	return resource
}
