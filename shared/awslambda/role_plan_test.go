//go:build unit

package awslambda

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/suite"
)

type RolePlanTestSuite struct {
	suite.Suite
}

func (s *RolePlanTestSuite) Test_role_plan_generates_deterministic_fingerprint() {
	rolePlanA := &RolePlan{
		Tracing: true,
	}
	rolePlanB := &RolePlan{
		Tracing: true,
	}
	aFingerprint := rolePlanA.Fingerprint()
	bFingerprint := rolePlanB.Fingerprint()
	s.Equal(
		aFingerprint,
		bFingerprint,
		"Expected identical role plans to have the same fingerprint",
	)
	s.Len(aFingerprint, 8, "Expected fingerprint to be 8 hex characters")
}

func (s *RolePlanTestSuite) Test_role_plan_fingerprint_changes_when_tracing_changes() {
	rolePlanA := &RolePlan{
		Tracing: true,
	}
	rolePlanB := &RolePlan{
		Tracing: false,
	}
	aFingerprint := rolePlanA.Fingerprint()
	bFingerprint := rolePlanB.Fingerprint()
	s.NotEqual(
		aFingerprint,
		bFingerprint,
		"Expected role plans with different tracing settings to have different fingerprints",
	)
}

func (s *RolePlanTestSuite) Test_role_plan_fingerprint_changes_when_link_set_changes() {
	// Provider links inject IAM grants into the referenced role, so handlers with
	// different link sets must not share one.
	withQueue := &RolePlan{Links: []string{"celerity/queue::orders"}}
	withOtherQueue := &RolePlan{Links: []string{"celerity/queue::payments"}}

	s.NotEqual(
		withQueue.Fingerprint(),
		withOtherQueue.Fingerprint(),
		"Expected handlers linked to different queues to get different roles",
	)
	s.Equal(
		withQueue.Fingerprint(),
		(&RolePlan{Links: []string{"celerity/queue::orders"}}).Fingerprint(),
		"Expected identical link sets to share a role",
	)
}

func (s *RolePlanTestSuite) Test_seed_role_spec_is_the_complete_base_role() {
	spec := SeedRoleSpec("celerityLambdaExec_abc12345", &RolePlan{})

	// roleName is required: links resolve the function's role by it.
	s.Equal("celerityLambdaExec_abc12345", core.StringValue(spec.Fields["roleName"]))

	// The provider's policy-document schema uses lowercase keys.
	doc := spec.Fields["assumeRolePolicyDocument"].Fields
	s.Equal("2012-10-17", core.StringValue(doc["version"]))
	statement := doc["statement"].Items[0].Fields
	s.Equal("Allow", core.StringValue(statement["effect"]))
	s.Equal("sts:AssumeRole", core.StringValue(statement["action"]))
	s.Equal(
		"lambda.amazonaws.com",
		core.StringValue(statement["principal"].Fields["service"]),
	)

	s.Equal(
		[]string{lambdaBasicExecutionPolicyARN},
		core.StringSliceValue(spec.Fields["managedPolicyArns"]),
	)

	// No links grant these, and tracing is off, so no inline policies.
	s.NotContains(spec.Fields, "policies")
}

func (s *RolePlanTestSuite) Test_seed_role_spec_adds_xray_policy_as_a_list_entry() {
	spec := SeedRoleSpec("celerityLambdaExec_abc12345", &RolePlan{Tracing: true})

	// aws/iam/role.policies is a LIST of {policyName, policyDocument}, not a map.
	policies := spec.Fields["policies"].Items
	s.Require().Len(policies, 1)
	s.Equal("celerity-xray", core.StringValue(policies[0].Fields["policyName"]))

	doc := policies[0].Fields["policyDocument"].Fields
	s.Equal("2012-10-17", core.StringValue(doc["version"]))
	statement := doc["statement"].Items[0].Fields
	s.Equal("Allow", core.StringValue(statement["effect"]))
	s.Equal("*", core.StringValue(statement["resource"]))
	s.Equal(
		[]string{"xray:PutTraceSegments", "xray:PutTelemetryRecords"},
		core.StringSliceValue(statement["action"]),
	)
}

func TestRolePlanTestSuite(t *testing.T) {
	suite.Run(t, new(RolePlanTestSuite))
}
