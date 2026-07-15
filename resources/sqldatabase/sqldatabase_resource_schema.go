package sqldatabase

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func sqlDatabaseResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "SqlDatabaseDefinition",
	}
}
