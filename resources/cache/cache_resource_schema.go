package cache

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func cacheResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "CacheDefinition",
	}
}
