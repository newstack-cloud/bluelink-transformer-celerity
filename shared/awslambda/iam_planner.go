package awslambda

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

const lambdaBasicExecutionPolicyARN = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"

func providerDomain() string {
	return "amazonaws.com"
}

// SeedRoleSpec builds the COMPLETE spec for a handler's execution role.
//
// There are no per-handler contributions to merge into it: the provider's links
// inject their own resource-scoped statements into the role at deploy time via
// the bluelink-link-access policy allocator. The transformer therefore emits the
// base role only, plus the one policy no link supplies (X-Ray).
//
// roleName is required. The links resolve a function's role to an aws/iam/role
// resource in the same blueprint by this name; a role without it fails every
// link at deploy.
func SeedRoleSpec(roleName string, plan *RolePlan) *core.MappingNode {
	spec := core.MappingNodeFields(
		"roleName", core.MappingNodeFromString(roleName),
		"assumeRolePolicyDocument", assumeRolePolicyDoc(),
		"managedPolicyArns", core.MappingNodeItems(
			core.MappingNodeFromString(lambdaBasicExecutionPolicyARN),
		),
	)

	// policies is a LIST of {policyName, policyDocument} objects on aws/iam/role,
	// not a map keyed by name. The allocator's bluelink-link-access policy
	// coexists in this list without conflict.
	if plan.Tracing {
		spec.Fields["policies"] = core.MappingNodeItems(
			core.MappingNodeFields(
				"policyName", core.MappingNodeFromString("celerity-xray"),
				"policyDocument", xrayPolicyDoc(),
			),
		)
	}

	return spec
}

func assumeRolePolicyDoc() *core.MappingNode {
	return core.MappingNodeFields(
		"version", core.MappingNodeFromString(policyDocVersion),
		"statement", core.MappingNodeItems(core.MappingNodeFields(
			"effect", core.MappingNodeFromString("Allow"),
			"principal", core.MappingNodeFields(
				"service", core.MappingNodeFromString("lambda."+providerDomain()),
			),
			"action", core.MappingNodeFromString("sts:AssumeRole"),
		)),
	)
}
