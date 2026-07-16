package cache

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
)

func cacheResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "CacheDefinition",
		Description: "A managed in-memory cache (Redis/Valkey) that handlers connect to. Requires VPC " +
			"placement. On aws-serverless this maps to an ElastiCache replication group.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The unique name of the cache. On aws-serverless this maps to the ElastiCache " +
					"replication group id.",
			},
			"clusterMode": {
				Type: provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "Whether to deploy the cache as a cluster with multiple shards and replicas " +
					"(higher availability and throughput) or as a single instance. The SDK cache abstraction " +
					"handles the connection difference transparently.",
				Default: core.MappingNodeFromBool(false),
			},
			"engineVersion": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The Valkey engine version to deploy. Valkey is Redis OSS-compatible. " +
					"Defaults to \"8.2\" when omitted.",
			},
			"authMode": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "How handlers authenticate to the cache at runtime. \"password\" uses an " +
					"auto-generated AUTH token stored in the secret store; \"iam\" uses platform IAM identity " +
					"authentication with short-lived tokens.",
				Default: core.MappingNodeFromString("password"),
				AllowedValues: []*core.MappingNode{
					core.MappingNodeFromString("password"),
					core.MappingNodeFromString("iam"),
				},
			},

			// Computed outputs. host is a topology-dependent endpoint, so it is
			// surfaced as an emit-time derived value; id and port are simple renames
			// onto the replication group.
			"id": {
				Type:        provider.ResourceDefinitionsSchemaTypeString,
				Computed:    true,
				Description: "The ID of the cache in the target environment (the ElastiCache replication group id).",
			},
			"host": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The primary endpoint handlers connect to. In cluster mode this is the " +
					"ElastiCache configuration endpoint; otherwise it is the primary endpoint.",
			},
			"port": {
				Type:        provider.ResourceDefinitionsSchemaTypeInteger,
				Computed:    true,
				Description: "The port handlers connect to (6379 for Valkey).",
			},
		},
	}
}
