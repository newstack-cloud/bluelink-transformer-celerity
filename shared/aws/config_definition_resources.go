package aws

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

// AllTransformerConfigFields returns every AWS config field the transformer
// accepts. These are the account-level fields describing how to reach AWS, and the
// resource-scoped fields tuning what is emitted into it.
//
// A single entry point so the two sets cannot be wired up independently.
func AllTransformerConfigFields() map[string]*core.ConfigFieldDefinition {
	fields := map[string]*core.ConfigFieldDefinition{}
	maps.Copy(fields, TransformerConfigFields())
	maps.Copy(fields, ResourceConfigFields())

	return fields
}

// ResourceConfigFields returns the resource-scoped deploy-config fields: the keys
// that tune an individual emitted resource rather than describing how to reach the
// account.
//
// Every key an emit reads has to be declared here. This plugin does not set
// AllowAdditionalFields, so an undeclared key is an error diagnostic and the
// engine turns that into a 422; routing a key to this plugin does not make it
// valid.
func ResourceConfigFields() map[string]*core.ConfigFieldDefinition {
	fields := map[string]*core.ConfigFieldDefinition{}
	for _, field := range resourceConfigFields {
		maps.Copy(fields, field.expand())
	}

	return fields
}

type resourceConfigField struct {
	prefix string
	// Matched as [A-Za-z0-9\-_]+ whatever the name, but follows the resource
	// specs' vocabulary because it appears in the generated config documentation.
	placeholder string
	// Absorbs any path after the resource name, so "readReplicas.instanceClass"
	// is one suffix rather than a nested shape.
	suffix      string
	fieldType   core.ScalarType
	label       string
	description string
	allowed     []*core.ScalarValue
}

const (
	perResourceScope = "Applies to the named resource only, and takes precedence " +
		"over the equivalent key without a resource name."
	appWideScopeFormat = "Applies to every %s resource in the app that does not " +
		"set the equivalent per-resource key."
)

// ResolveDeployConfig resolves "<prefix>.<name>.<suffix>" and falls back to
// "<prefix>.<suffix>", so both have to be declared. The dynamic pattern requires a
// segment between prefix and suffix, so the per-resource declaration does not also
// cover the app-wide key.
func (f resourceConfigField) expand() map[string]*core.ConfigFieldDefinition {
	return map[string]*core.ConfigFieldDefinition{
		f.perResourceKey(): f.definition(perResourceScope),
		f.appWideKey(): f.definition(
			fmt.Sprintf(appWideScopeFormat, f.prefix),
		),
	}
}

func (f resourceConfigField) perResourceKey() string {
	return perResourceKey(f.prefix, f.placeholder, f.suffix)
}

func (f resourceConfigField) appWideKey() string {
	return globalKey(f.prefix, f.suffix)
}

func (f resourceConfigField) definition(scope string) *core.ConfigFieldDefinition {
	return &core.ConfigFieldDefinition{
		Type:          f.fieldType,
		Label:         f.label,
		Description:   fmt.Sprintf("%s. %s", strings.TrimRight(f.description, " ."), scope),
		AllowedValues: f.allowed,
	}
}

// Every resource-scoped key the aws-serverless emits read, and nothing else. The
// resource specifications in celerity-docs-v2 are the contract; the keys they
// specify that no emit reads are registered in
// docs/contract/resource-mapping-aws-serverless.md section A0.
var resourceConfigFields = slices.Concat(
	queueConfigFields,
	datastoreConfigFields,
	vpcConfigFields,
	databaseConfigFields,
	cacheConfigFields,
)

var queueConfigFields = []resourceConfigField{
	{
		prefix:      "aws.sqs",
		placeholder: "<queue>",
		suffix:      "messageRetentionPeriod",
		fieldType:   core.ScalarTypeInteger,
		label:       "Message Retention Period",
		description: "How long, in seconds, SQS keeps a message that has not been deleted.",
	},
	{
		prefix:      "aws.sqs",
		placeholder: "<queue>",
		suffix:      "maxMessageSize",
		fieldType:   core.ScalarTypeInteger,
		label:       "Maximum Message Size",
		description: "The largest message, in bytes, the queue accepts.",
	},
}

var datastoreConfigFields = []resourceConfigField{
	{
		prefix:      "aws.dynamodb",
		placeholder: "<datastoreName>",
		suffix:      "billingMode",
		fieldType:   core.ScalarTypeString,
		label:       "Billing Mode",
		description: "How the table is billed. Celerity emits PAY_PER_REQUEST when " +
			"unset, rather than inheriting DynamoDB's PROVISIONED default, which " +
			"would require capacity to be set as well.",
		allowed: []*core.ScalarValue{
			core.ScalarFromString("PAY_PER_REQUEST"),
			core.ScalarFromString("PROVISIONED"),
		},
	},
	{
		prefix:      "aws.dynamodb",
		placeholder: "<datastoreName>",
		suffix:      "readCapacityUnits",
		fieldType:   core.ScalarTypeInteger,
		label:       "Read Capacity Units",
		description: "Provisioned read capacity. Read only under PROVISIONED billing, " +
			"and inherited by every global secondary index.",
	},
	{
		prefix:      "aws.dynamodb",
		placeholder: "<datastoreName>",
		suffix:      "writeCapacityUnits",
		fieldType:   core.ScalarTypeInteger,
		label:       "Write Capacity Units",
		description: "Provisioned write capacity. Read only under PROVISIONED billing, " +
			"and inherited by every global secondary index.",
	},
	{
		prefix:      "aws.dynamodb",
		placeholder: "<datastoreName>",
		suffix:      "maxReadRequestUnits",
		fieldType:   core.ScalarTypeInteger,
		label:       "Maximum Read Request Units",
		description: "Ceiling on on-demand read units. Read only under PAY_PER_REQUEST billing.",
	},
	{
		prefix:      "aws.dynamodb",
		placeholder: "<datastoreName>",
		suffix:      "maxWriteRequestUnits",
		fieldType:   core.ScalarTypeInteger,
		label:       "Maximum Write Request Units",
		description: "Ceiling on on-demand write units. Read only under PAY_PER_REQUEST billing.",
	},
}

var vpcConfigFields = []resourceConfigField{
	{
		prefix:      "aws.vpc",
		placeholder: "<vpc>",
		suffix:      "cidrBlock",
		fieldType:   core.ScalarTypeString,
		label:       "CIDR Block",
		description: "The IPv4 range for a managed VPC, defaulting to 10.0.0.0/16. " +
			"This is the only way to move a managed VPC off that default, so it is " +
			"what an app coexisting with an existing estate needs in order not to " +
			"overlap it.",
	},
	{
		prefix:      "aws.vpc",
		placeholder: "<vpc>",
		suffix:      "enableDNSSupport",
		fieldType:   core.ScalarTypeBool,
		label:       "Enable DNS Support",
		description: "Whether the VPC resolves DNS through the Amazon-provided resolver.",
	},
	{
		prefix:      "aws.vpc",
		placeholder: "<vpc>",
		suffix:      "enableDNSHostnames",
		fieldType:   core.ScalarTypeBool,
		label:       "Enable DNS Hostnames",
		description: "Whether instances in the VPC receive public DNS hostnames.",
	},
}

// The aws.rds.* keys apply to a provisioned instance and the aws.aurora.* keys to
// an Aurora Serverless v2 cluster, which aws.aurora.<dbName>.enabled selects
// between.
var databaseConfigFields = []resourceConfigField{
	{
		prefix:      "aws.rds",
		placeholder: "<dbName>",
		suffix:      "instanceClass",
		fieldType:   core.ScalarTypeString,
		label:       "Instance Class",
		description: "The instance class for a provisioned database, defaulting to " +
			"db.t3.micro.",
	},
	{
		prefix:      "aws.rds",
		placeholder: "<dbName>",
		suffix:      "allocatedStorage",
		fieldType:   core.ScalarTypeInteger,
		label:       "Allocated Storage",
		description: "Storage for a provisioned database, in gibibytes, defaulting to 20.",
	},
	{
		prefix:      "aws.rds",
		placeholder: "<dbName>",
		suffix:      "readReplicas.instanceClass",
		fieldType:   core.ScalarTypeString,
		label:       "Read Replica Instance Class",
		description: "The instance class for the read replica of a provisioned " +
			"database, defaulting to db.t3.micro. Only read when the database " +
			"enables read replicas, and not on Aurora, where the reader is a " +
			"serverless instance in the cluster.",
	},
	{
		prefix:      "aws.aurora",
		placeholder: "<dbName>",
		suffix:      "enabled",
		fieldType:   core.ScalarTypeBool,
		label:       "Aurora Serverless v2 Enabled",
		description: "Whether the SQL database is emitted as an Aurora Serverless v2 " +
			"cluster rather than a provisioned instance.",
	},
	{
		prefix:      "aws.aurora",
		placeholder: "<dbName>",
		suffix:      "minACU",
		fieldType:   core.ScalarTypeFloat,
		label:       "Minimum Aurora Capacity Units",
		description: "The floor the cluster scales down to, in Aurora Capacity Units. " +
			"Capacity moves in 0.5 steps, so a whole number and a fraction are both valid.",
	},
	{
		prefix:      "aws.aurora",
		placeholder: "<dbName>",
		suffix:      "maxACU",
		fieldType:   core.ScalarTypeFloat,
		label:       "Maximum Aurora Capacity Units",
		description: "The ceiling the cluster scales up to, in Aurora Capacity Units. " +
			"Capacity moves in 0.5 steps, so a whole number and a fraction are both valid.",
	},
}

var cacheConfigFields = []resourceConfigField{
	{
		prefix:      "aws.elasticache",
		placeholder: "<cacheName>",
		suffix:      "nodeType",
		fieldType:   core.ScalarTypeString,
		label:       "Node Type",
		description: "The ElastiCache node instance type.",
	},
	{
		prefix:      "aws.elasticache",
		placeholder: "<cacheName>",
		suffix:      "numShards",
		fieldType:   core.ScalarTypeInteger,
		label:       "Number of Shards",
		description: "Shards (node groups) in the replication group. Only meaningful " +
			"in cluster mode.",
	},
	{
		prefix:      "aws.elasticache",
		placeholder: "<cacheName>",
		suffix:      "numReplicas",
		fieldType:   core.ScalarTypeInteger,
		label:       "Replicas Per Shard",
		description: "Read replicas per shard.",
	},
	{
		prefix:      "aws.elasticache",
		placeholder: "<cacheName>",
		suffix:      "automaticFailover",
		fieldType:   core.ScalarTypeBool,
		label:       "Automatic Failover",
		description: "Whether a replica is promoted automatically when the primary fails.",
	},
	{
		prefix:      "aws.elasticache",
		placeholder: "<cacheName>",
		suffix:      "atRestEncryption",
		fieldType:   core.ScalarTypeBool,
		label:       "Encryption At Rest",
		description: "Whether the replication group encrypts data at rest.",
	},
	{
		prefix:      "aws.elasticache",
		placeholder: "<cacheName>",
		suffix:      "snapshotRetentionDays",
		fieldType:   core.ScalarTypeInteger,
		label:       "Snapshot Retention Days",
		description: "Days automatic snapshots are kept. Zero disables automatic snapshots.",
	},
}
