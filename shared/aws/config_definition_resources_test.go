//go:build unit

package aws

import (
	"slices"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/suite"
)

type ResourceConfigFieldsTestSuite struct {
	suite.Suite
}

// Listed explicitly rather than derived from the declaration table: a list built
// with the same expression the code under test uses cannot catch a wrong key.
func documentedResourceKeys() []string {
	return []string{
		"aws.sqs.orders.messageRetentionPeriod",
		"aws.sqs.messageRetentionPeriod",
		"aws.sqs.orders.maxMessageSize",
		"aws.sqs.maxMessageSize",
		"aws.dynamodb.orders.billingMode",
		"aws.dynamodb.billingMode",
		"aws.dynamodb.orders.readCapacityUnits",
		"aws.dynamodb.orders.writeCapacityUnits",
		"aws.dynamodb.orders.maxReadRequestUnits",
		"aws.dynamodb.orders.maxWriteRequestUnits",
		"aws.vpc.appVpc.cidrBlock",
		"aws.vpc.cidrBlock",
		"aws.vpc.appVpc.enableDNSSupport",
		"aws.vpc.appVpc.enableDNSHostnames",
		"aws.rds.appDb.instanceClass",
		"aws.rds.instanceClass",
		"aws.rds.appDb.allocatedStorage",
		"aws.rds.appDb.readReplicas.instanceClass",
		"aws.rds.readReplicas.instanceClass",
		"aws.aurora.appDb.enabled",
		"aws.aurora.appDb.minACU",
		"aws.aurora.minACU",
		"aws.aurora.appDb.maxACU",
		"aws.elasticache.appCache.nodeType",
		"aws.elasticache.nodeType",
		"aws.elasticache.appCache.numShards",
		"aws.elasticache.appCache.numReplicas",
		"aws.elasticache.appCache.automaticFailover",
		"aws.elasticache.appCache.atRestEncryption",
		"aws.elasticache.appCache.snapshotRetentionDays",
	}
}

// A value of the declared type for each key, so a type mismatch cannot be what
// fails when the assertion is about the key being recognised.
func valuesForDocumentedKeys() map[string]*core.ScalarValue {
	intKeys := []string{
		"messageRetentionPeriod", "maxMessageSize", "readCapacityUnits",
		"writeCapacityUnits", "maxReadRequestUnits", "maxWriteRequestUnits",
		"allocatedStorage", "numShards", "numReplicas", "snapshotRetentionDays",
	}
	boolKeys := []string{
		"enableDNSSupport", "enableDNSHostnames", "enabled",
		"automaticFailover", "atRestEncryption",
	}
	floatKeys := []string{"minACU", "maxACU"}

	values := map[string]*core.ScalarValue{}
	for _, key := range documentedResourceKeys() {
		switch {
		case endsWithAny(key, intKeys):
			values[key] = core.ScalarFromInt(1)
		case endsWithAny(key, boolKeys):
			values[key] = core.ScalarFromBool(true)
		case endsWithAny(key, floatKeys):
			values[key] = core.ScalarFromFloat(0.5)
		case strings.HasSuffix(key, ".billingMode"):
			values[key] = core.ScalarFromString("PAY_PER_REQUEST")
		default:
			values[key] = core.ScalarFromString("a-value")
		}
	}

	return values
}

func endsWithAny(key string, suffixes []string) bool {
	return slices.ContainsFunc(suffixes, func(suffix string) bool {
		return strings.HasSuffix(key, "."+suffix)
	})
}

// Anchoring a dynamic field pattern to the whole config key is a blueprint
// library behaviour that arrives after v0.54.0, which this module pins. Probed
// rather than version-pinned, so the assertions start running on the dependency
// bump rather than waiting for someone to remember to unskip them.
func coreAnchorsDynamicFieldPatterns() bool {
	diagnostics, err := core.ValidateConfigDefinition(
		"probe",
		"transformer",
		map[string]*core.ScalarValue{"a.b.suffixed": core.ScalarFromString("x")},
		&core.ConfigDefinition{
			Fields: map[string]*core.ConfigFieldDefinition{
				"a.<name>.suffix": {Type: core.ScalarTypeString, Label: "Suffix"},
			},
		},
	)

	return err == nil && hasErrorDiagnostic(diagnostics)
}

func hasErrorDiagnostic(diagnostics []*core.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Level == core.DiagnosticLevelError {
			return true
		}
	}

	return false
}

func (s *ResourceConfigFieldsTestSuite) errorsFor(
	config map[string]*core.ScalarValue,
) []string {
	diagnostics, err := core.ValidateConfigDefinition(
		"celerity",
		"transformer",
		config,
		&core.ConfigDefinition{Fields: AllTransformerConfigFields()},
	)
	s.Require().NoError(err)

	messages := []string{}
	for _, diagnostic := range diagnostics {
		if diagnostic.Level == core.DiagnosticLevelError {
			messages = append(messages, diagnostic.Message)
		}
	}

	return messages
}

// Validated as one config map, the way a deploy presents it, so a key that
// collides with another declaration shows up here rather than passing alone.
func (s *ResourceConfigFieldsTestSuite) Test_accepts_every_documented_key_together() {
	s.Empty(s.errorsFor(valuesForDocumentedKeys()))
}

func (s *ResourceConfigFieldsTestSuite) Test_accepts_each_documented_key_alone() {
	values := valuesForDocumentedKeys()
	for _, key := range documentedResourceKeys() {
		s.Run(key, func() {
			s.Empty(s.errorsFor(map[string]*core.ScalarValue{key: values[key]}))
		})
	}
}

// Capacity has to be a float field, since Aurora scales in 0.5 steps. A whole
// number authored for it is converted to a float by the engine before the
// transformer sees it, which is asserted in the blueprint library.
func (s *ResourceConfigFieldsTestSuite) Test_capacity_units_are_declared_as_floats() {
	for _, key := range []string{
		"aws.aurora.<dbName>.minACU",
		"aws.aurora.<dbName>.maxACU",
		"aws.aurora.minACU",
		"aws.aurora.maxACU",
	} {
		s.Run(key, func() {
			field, declared := ResourceConfigFields()[key]
			s.Require().True(declared)
			s.Equal(core.ScalarTypeFloat, field.Type)
		})
	}

	s.Empty(s.errorsFor(map[string]*core.ScalarValue{
		"aws.aurora.appDb.minACU": core.ScalarFromFloat(0.5),
	}))
}

// A truncated key is rejected either way; a plural and a separator substitution
// slip past an unanchored pattern whose dots are wildcards.
var needsAnchoredPatterns = map[string]bool{
	"aws.vpc.appVpc.cidrBlocks": true,
	"aws_vpc_appVpc_cidrBlock":  true,
}

// The declaration is only worth having if it still rejects what no emit reads.
func (s *ResourceConfigFieldsTestSuite) Test_rejects_undeclared_keys() {
	for _, key := range []string{
		// A truncation, a plural, and a separator substitution. The plural and the
		// separator cases need the pattern anchored and its dots escaped.
		"aws.vpc.appVpc.cidrBlok",
		"aws.vpc.appVpc.cidrBlocks",
		"aws_vpc_appVpc_cidrBlock",
		// Documented in the resource specs but read by no emit, so still an error.
		"aws.elasticache.appCache.evictionPolicy",
		"aws.dynamodb.orders.replicaRegions",
		"aws.rds.appDb.multiAz",
	} {
		s.Run(key, func() {
			if needsAnchoredPatterns[key] && !coreAnchorsDynamicFieldPatterns() {
				s.T().Skip("needs dynamic field patterns anchored and their dots escaped")
			}
			s.NotEmpty(
				s.errorsFor(map[string]*core.ScalarValue{key: core.ScalarFromString("x")}),
				"%q must be reported, not silently defaulted",
				key,
			)
		})
	}
}

func (s *ResourceConfigFieldsTestSuite) Test_rejects_a_billing_mode_outside_the_allowed_set() {
	s.NotEmpty(s.errorsFor(map[string]*core.ScalarValue{
		"aws.dynamodb.orders.billingMode": core.ScalarFromString("ON_DEMAND"),
	}), "PAY_PER_REQUEST is the name DynamoDB uses")
}

func TestResourceConfigFieldsTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceConfigFieldsTestSuite))
}
