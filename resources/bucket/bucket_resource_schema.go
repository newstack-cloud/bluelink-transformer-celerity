package bucket

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func bucketResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "BucketDefinition",
	}
}
