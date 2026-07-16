package datastore

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func datastoreResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "DatastoreDefinition",
		Description: "A managed NoSQL data store that handlers read from and write to, and consumers " +
			"process change streams from. On AWS this maps to a DynamoDB table.",
		Required: []string{"keys"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The unique name of the data store. If a name is not provided, a unique name " +
					"is generated based on the blueprint the data store is defined in. On AWS " +
					"this maps to the DynamoDB table name (create-only).",
			},
			"keys": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Description: "The primary and (optional) sort key that make up the data store's primary key.",
				Required:    []string{"partitionKey"},
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"partitionKey": {
						Type: provider.ResourceDefinitionsSchemaTypeString,
						Description: "The field that uniquely identifies each item. Maps to the DynamoDB " +
							"table's HASH key.",
					},
					"sortKey": {
						Type: provider.ResourceDefinitionsSchemaTypeString,
						Description: "The field used to sort items sharing a partition key. Only supported " +
							"by target environments with composite keys (such as DynamoDB, where it maps to " +
							"the RANGE key).",
					},
				},
			},
			"indexes": {
				Type:        provider.ResourceDefinitionsSchemaTypeArray,
				Description: "Secondary indexes for efficient querying on other field combinations.",
				Items: &provider.ResourceDefinitionsSchema{
					Type:     provider.ResourceDefinitionsSchemaTypeObject,
					Required: []string{"name", "fields"},
					Attributes: map[string]*provider.ResourceDefinitionsSchema{
						"name": {
							Type:        provider.ResourceDefinitionsSchemaTypeString,
							Description: "The name of the index.",
						},
						"fields": {
							Type: provider.ResourceDefinitionsSchemaTypeArray,
							Description: "The one or two fields the index covers. On AWS the first " +
								"field is the index HASH key and the second (if present) the RANGE key.",
							MinLength: 1,
							MaxLength: 2,
							Items: &provider.ResourceDefinitionsSchema{
								Type: provider.ResourceDefinitionsSchemaTypeString,
							},
						},
					},
				},
			},
			"timeToLive": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Description: "Configures automatic expiry of items based on a timestamp field.",
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"fieldName": {
						Type:        provider.ResourceDefinitionsSchemaTypeString,
						Description: "The item field holding the expiry timestamp (epoch seconds).",
					},
					"enabled": {
						Type:        provider.ResourceDefinitionsSchemaTypeBoolean,
						Description: "Whether time-to-live expiry is enabled.",
					},
				},
			},

			// Schema-management fields: consumed by the Celerity CLI's schema
			// tooling (type generation, drift detection), not deployed to the
			// concrete table. Accepted here but dropped by the aws-serverless emit.
			"schema": {
				Type:     provider.ResourceDefinitionsSchemaTypeObject,
				Nullable: true,
				Description: "An inline data store schema for Celerity's schema-management tooling. Not " +
					"deployed to the concrete table.",
			},
			"schemaPath": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The path to an external schema definition file for Celerity's schema-management " +
					"tooling. Not deployed to the concrete table.",
			},
			"scriptsPath": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The path to data store scripts for Celerity's schema-management tooling. Not " +
					"deployed to the concrete table.",
			},

			// Computed output.
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The ID of the created data store in the target environment. On AWS " +
					"this is the DynamoDB table ARN.",
			},
		},
	}
}
