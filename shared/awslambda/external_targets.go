package awslambda

import (
	"sort"
	"strings"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

// externalTargetsPolicyDoc is one inline policy granting a handler access to
// each out-of-blueprint resource it uses, scoped to that resource's ARN.
//
// Nothing else grants these. A managed resource gets its statements from the
// provider link between the emitted function and the emitted resource; an
// external resource has no emitted counterpart, so without this the handler
// resolves the resource's address through the resources store and is then
// refused at the first call.
//
// Returns nil when there is nothing to grant.
func externalTargetsPolicyDoc(targets []ExternalGrant) *core.MappingNode {
	statements := []*core.MappingNode{}
	for _, target := range targets {
		if target.ARN == "" {
			continue
		}

		actions := externalTargetActions(target.Service, target.Access)
		if len(actions) == 0 {
			continue
		}

		statements = append(statements, core.MappingNodeFields(
			"effect", core.MappingNodeFromString("Allow"),
			"action", core.MappingNodeFromStringSlice(actions),
			"resource", externalTargetResourceNode(target),
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

// The action sets the provider's links grant for a managed resource, so that
// moving a resource out of the blueprint does not change what a handler may do
// with it. Each default matches the provider's default for its link.
func externalTargetActions(service, access string) []string {
	switch service {
	case ExternalTargetServiceS3:
		return s3TargetActions(access)
	case ExternalTargetServiceDynamoDB:
		return dynamoDBTargetActions(access)
	case ExternalTargetServiceSQS:
		return sqsTargetActions(access)
	case ExternalTargetServiceSNS:
		// No access levels to check, a handler linked to a topic publishes to it.
		return []string{"sns:Publish"}
	case ExternalTargetServiceSSM:
		return []string{
			"ssm:GetParameter",
			"ssm:GetParameters",
			"ssm:GetParametersByPath",
		}
	case ExternalTargetServiceSecret:
		return []string{
			"secretsmanager:GetSecretValue",
			"secretsmanager:DescribeSecret",
		}
	default:
		return nil
	}
}

func s3TargetActions(access string) []string {
	switch access {
	case "read":
		return []string{"s3:GetObject", "s3:ListBucket"}
	case "write":
		return []string{"s3:PutObject", "s3:DeleteObject"}
	default:
		return []string{"s3:GetObject", "s3:ListBucket", "s3:PutObject", "s3:DeleteObject"}
	}
}

func dynamoDBTargetActions(access string) []string {
	read := []string{
		"dynamodb:GetItem",
		"dynamodb:Query",
		"dynamodb:Scan",
		"dynamodb:BatchGetItem",
	}
	write := []string{
		"dynamodb:PutItem",
		"dynamodb:UpdateItem",
		"dynamodb:DeleteItem",
		"dynamodb:BatchWriteItem",
	}

	switch access {
	case "read":
		return read
	case "write":
		return write
	default:
		return append(read, write...)
	}
}

// The queue access levels are the provider's own vocabulary, which differs from
// the read/write one because sending to a queue and consuming from it are not
// opposites. Default send, as the provider's link defaults.
func sqsTargetActions(access string) []string {
	base := []string{"sqs:GetQueueUrl", "sqs:GetQueueAttributes"}

	switch access {
	case "receive":
		return append([]string{"sqs:ReceiveMessage", "sqs:DeleteMessage"}, base...)
	case "sendReceive":
		return append(
			[]string{"sqs:SendMessage", "sqs:ReceiveMessage", "sqs:DeleteMessage"}, base...,
		)
	default:
		return append([]string{"sqs:SendMessage"}, base...)
	}
}

// S3 is the one service whose actions span two ARNs: ListBucket is granted on
// the bucket and the object actions on its contents. Granting only the bucket
// ARN leaves every object call refused, which reads as a credentials problem
// rather than a policy one.
func externalTargetResourceNode(target ExternalGrant) *core.MappingNode {
	if target.Service != ExternalTargetServiceS3 {
		return policyResourceNode(target.ARN)
	}

	return core.MappingNodeItems(
		core.MappingNodeFromString(target.ARN),
		core.MappingNodeFromString(strings.TrimSuffix(target.ARN, "/")+"/*"),
	)
}

// SortExternalGrants orders grants so the seeded policy and the fingerprint it
// feeds are stable across runs.
func SortExternalGrants(grants []ExternalGrant) {
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Service != grants[j].Service {
			return grants[i].Service < grants[j].Service
		}
		if grants[i].ARN != grants[j].ARN {
			return grants[i].ARN < grants[j].ARN
		}

		return grants[i].Access < grants[j].Access
	})
}
