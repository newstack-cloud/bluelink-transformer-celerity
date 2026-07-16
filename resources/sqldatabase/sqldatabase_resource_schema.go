package sqldatabase

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
)

func sqlDatabaseResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "SqlDatabaseDefinition",
		Description: "A managed relational (SQL) database that handlers connect to. Requires VPC " +
			"placement. On AWS this maps to an RDS instance (or Aurora cluster); on aws-serverless a " +
			"standalone instance is fronted by an RDS Proxy for Lambda connection pooling.",
		Required: []string{"engine"},
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"engine": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The database engine. Only \"postgres\" is supported in v0; \"mysql\" is " +
					"planned for v1. (Aurora Serverless v2 is selected via the aws.aurora.<db>.enabled " +
					"deploy config, not this field.)",
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("postgres"),
				},
			},
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The unique name of the database. On AWS this maps to the RDS " +
					"instance/cluster identifier and initial database name.",
			},
			"schemaPath": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "Path to the SQL schema definition for Celerity's schema-management tooling. " +
					"Not deployed to the concrete database.",
			},
			"migrationsPath": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "Path to SQL migration files for Celerity's schema-management tooling. Not " +
					"deployed to the concrete database.",
			},
			"readReplicas": {
				Type: provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "Whether to provision read replicas for horizontal read scaling, exposing the " +
					"\"readHost\" output. On Aurora Serverless v2 \"readHost\" is the load-balanced cluster " +
					"reader endpoint; on standalone RDS it resolves to a single read-replica instance endpoint.",
				Default: core.MappingNodeFromBool(false),
			},
			"authMode": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "How handlers authenticate to the database at runtime. \"password\" uses " +
					"credentials managed in the secret store; \"iam\" uses platform IAM identity authentication.",
				Default: core.MappingNodeFromString("password"),
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("password"),
					core.MappingNodeFromString("iam"),
				},
			},

			// Computed outputs. host and readHost are endpoint-dependent (proxy vs
			// instance/cluster writer; Aurora cluster reader vs reader instance), so
			// they are surfaced as emit-time derived values; id, port and
			// databaseName are simple renames/literals.
			"id": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Computed:    true,
				Description: "The ID of the database in the target environment (the RDS instance ARN).",
			},
			"host": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The primary (write) endpoint handlers connect to: the RDS Proxy endpoint when a " +
					"proxy is provisioned, otherwise the instance (or Aurora cluster writer) endpoint.",
			},
			"readHost": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The reader endpoint, present only when readReplicas is enabled. Aurora: the " +
					"cluster reader endpoint; standalone RDS: the read-replica instance endpoint.",
			},
			"port": {
				Type:        provider.ResourceDefinitionsSchemaTypeInteger,
				Computed:    true,
				Description: "The port handlers connect to (5432 for PostgreSQL).",
			},
			"databaseName": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Computed:    true,
				Description: "The name of the database.",
			},
		},
	}
}
