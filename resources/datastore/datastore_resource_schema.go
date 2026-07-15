package datastore

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func datastoreResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "DatastoreDefinition",
	}
}
