package awslambda

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

// The IAM policy language version. Note the provider's
// policy-document schema uses lowercase keys (version/statement/effect/...)
// while the values keep their canonical casing ("Allow", "2012-10-17").
const policyDocVersion = "2012-10-17"

// RolePlan decides which handlers may share an execution role.
//
// The provider's links inject their own resource-scoped IAM statements into
// whichever role a function references, so the transformer never computes policy
// content for linked resources. A role may
// therefore only be shared between handlers whose link sets are identical,
// otherwise each handler would inherit the other's grants.
type RolePlan struct {
	// Links is the sorted set of "<linkType>::<targetResourceName>" entries for
	// every link the handler declares, inbound and outbound.
	Links []string `json:"links,omitempty"`
	// Tracing adds the celerity-xray policy; no provider link grants X-Ray.
	Tracing bool `json:"tracing"`
	// VPC is the subnet type when the handler is VPC-attached, empty otherwise.
	VPC string `json:"vpc,omitempty"`
	// ExternalSources are the out-of-blueprint event sources (external SQS queues
	// and DynamoDB/Kinesis streams) an absorbed consumer polls via a standalone
	// event source mapping. No provider link injects their source-read IAM, so the
	// seed grants it; they are part of the fingerprint so a handler with external
	// sources never shares a role with one that lacks them.
	ExternalSources []ExternalEventSource `json:"externalSources,omitempty"`
	// ResourceLinksStorePath is the SSM Parameter Store path prefix of the internal
	// resources namespace config store. When set, the seed grants the role read
	// access to that path (no provider link does); it is part of the fingerprint so a
	// handler that reads the store never shares a role with one that does not.
	ResourceLinksStorePath string `json:"resourceLinksStorePath,omitempty"`
}

// ExternalEventSource identifies one out-of-blueprint event source that the
// execution role must be granted read access to. Both fields are part of the
// role fingerprint.
type ExternalEventSource struct {
	// Service selects the action set granted (see ExternalSourceService* constants).
	Service string `json:"service"`
	// ARN is the specific external source ARN the statement is scoped to. It may be
	// a literal ARN or a ${...} substitution rendered to its canonical string.
	ARN string `json:"arn"`
}

// External event source service identifiers, selecting the source-read action set
// seeded onto the execution role for a standalone event source mapping.
const (
	ExternalSourceServiceSQS            = "sqs"
	ExternalSourceServiceDynamoDBStream = "dynamodb-stream"
	ExternalSourceServiceKinesisStream  = "kinesis-stream"
)

// Fingerprint is the role-sharing key: identical plans → identical fingerprint
// → one shared role. Stable JSON + SHA-256, first 8 hex characters.
func (p *RolePlan) Fingerprint() string {
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

func xrayPolicyDoc() *core.MappingNode {
	return core.MappingNodeFields(
		"version", core.MappingNodeFromString(policyDocVersion),
		"statement", core.MappingNodeItems(core.MappingNodeFields(
			"effect", core.MappingNodeFromString("Allow"),
			"action", core.MappingNodeFromStringSlice(
				[]string{"xray:PutTraceSegments", "xray:PutTelemetryRecords"},
			),
			"resource", core.MappingNodeFromString("*"),
		)),
	)
}
