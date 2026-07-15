package api

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func apiResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "ApiDefinition",
	}
}
