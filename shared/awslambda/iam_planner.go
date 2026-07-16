package awslambda

import (
	"strings"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
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
	if policies := inlinePolicies(plan); len(policies) > 0 {
		spec.Fields["policies"] = core.MappingNodeItems(policies...)
	}

	return spec
}

// Builds the inline policy list the transformer owns: the X-Ray policy (no
// provider link grants it) and, for each external event source that has no
// provider link, a scoped source-read policy statement.
func inlinePolicies(plan *RolePlan) []*core.MappingNode {
	policies := []*core.MappingNode{}
	if plan.Tracing {
		policies = append(policies, policyEntry("celerity-xray", xrayPolicyDoc()))
	}
	if doc := externalSourcesPolicyDoc(plan.ExternalSources); doc != nil {
		policies = append(policies, policyEntry("celerity-external-event-sources", doc))
	}
	return policies
}

func policyEntry(name string, document *core.MappingNode) *core.MappingNode {
	return core.MappingNodeFields(
		"policyName", core.MappingNodeFromString(name),
		"policyDocument", document,
	)
}

// Builds a single policy document with one statement per external event source,
// each granting that service's source-read actions scoped to the specific
// external ARN. Returns nil when there are no grantable sources.
func externalSourcesPolicyDoc(sources []ExternalEventSource) *core.MappingNode {
	statements := []*core.MappingNode{}
	for _, source := range sources {
		actions := externalSourceActions(source.Service)
		if len(actions) == 0 || source.ARN == "" {
			continue
		}
		statements = append(statements, core.MappingNodeFields(
			"effect", core.MappingNodeFromString("Allow"),
			"action", core.MappingNodeFromStringSlice(actions),
			"resource", policyResourceNode(source.ARN),
		))
	}
	if len(statements) == 0 {
		return nil
	}
	return core.MappingNodeFields(
		"version", core.MappingNodeFromString(policyDocVersion),
		"statement", core.MappingNodeItems(statements...),
	)
}

func externalSourceActions(service string) []string {
	switch service {
	case ExternalSourceServiceSQS:
		return []string{
			"sqs:ReceiveMessage",
			"sqs:DeleteMessage",
			"sqs:GetQueueAttributes",
		}
	case ExternalSourceServiceDynamoDBStream:
		return []string{
			"dynamodb:GetRecords",
			"dynamodb:GetShardIterator",
			"dynamodb:DescribeStream",
			"dynamodb:ListStreams",
		}
	case ExternalSourceServiceKinesisStream:
		return []string{
			"kinesis:GetRecords",
			"kinesis:GetShardIterator",
			"kinesis:DescribeStream",
			"kinesis:ListStreams",
		}
	default:
		return nil
	}
}

// Builds the policy statement's resource node from an ARN string. A plain
// literal ARN becomes a scalar node; an ARN carrying a ${...} substitution
// (a deploy-time-resolved external source) is parsed so it stays a real
// reference the deploy engine resolves.
func policyResourceNode(arn string) *core.MappingNode {
	if !strings.Contains(arn, "${") {
		return core.MappingNodeFromString(arn)
	}
	parsed, err := substitutions.ParseSubstitutionValues("", arn, nil, false, true, 0)
	if err != nil || len(parsed) == 0 {
		return core.MappingNodeFromString(arn)
	}
	return &core.MappingNode{
		StringWithSubstitutions: &substitutions.StringOrSubstitutions{
			Values: parsed,
		},
	}
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
