//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
)

// The AWS resource kinds this target knows how to find and remove.
const (
	kindLambdaFunction  = "lambda function"
	kindIAMRole         = "iam role"
	kindSQSQueue        = "sqs queue"
	kindSNSTopic        = "sns topic"
	kindDynamoDBTable   = "dynamodb table"
	kindS3Bucket        = "s3 bucket"
	kindAPI             = "apigatewayv2 api"
	kindEventBridgeRule = "eventbridge rule"
	kindSSMParameter    = "ssm parameter"
	kindSecret          = "secretsmanager secret"
	kindLogGroup        = "cloudwatch log group"
)

func init() {
	registerTargetSweeper(&awsSweeper{})
}

// The AWS deploy target. Clients are built on first use rather than at
// registration, so a run that never touches AWS never loads its config.
type awsSweeper struct {
	clients *sweepClients
	// Set when config loading failed, so the failure is reported once as a
	// problem rather than retried on every pass.
	configErr error
}

func (a *awsSweeper) Name() string {
	return "aws"
}

// AWS_REGION is what the harness itself requires before it will deploy, so its
// absence means this run never created anything here.
func (a *awsSweeper) Available() bool {
	return os.Getenv("AWS_REGION") != ""
}

func (a *awsSweeper) Find(
	ctx context.Context,
	scopes []string,
) ([]leakedResource, []string) {
	clients, err := a.clientsFor(ctx)
	if err != nil {
		// Fail closed: a sweep that cannot run must not pass silently.
		return nil, []string{fmt.Sprintf("load AWS config: %v", err)}
	}

	return clients.sweepOnce(scopes), clients.errs
}

func (a *awsSweeper) Delete(ctx context.Context, resource leakedResource) error {
	clients, err := a.clientsFor(ctx)
	if err != nil {
		return err
	}

	if err := clients.deleteLeak(resource); err != nil && !alreadyGone(err) {
		return err
	}

	return nil
}

// Lambda creates a log group as a side effect of a function running rather than
// because the blueprint declared one, so nothing in teardown owns it and its
// survival is not a teardown bug. They still accumulate indefinitely.
func (a *awsSweeper) Tidy(ctx context.Context, scopes []string) int {
	clients, err := a.clientsFor(ctx)
	if err != nil {
		return 0
	}

	return clients.tidyLogGroups(scopes)
}

func (a *awsSweeper) clientsFor(ctx context.Context) (*sweepClients, error) {
	if a.configErr != nil {
		return nil, a.configErr
	}
	if a.clients != nil {
		return a.clients, nil
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		a.configErr = err
		return nil, err
	}
	a.clients = newSweepClients(ctx, cfg)

	return a.clients, nil
}

type sweepClients struct {
	ctx      context.Context
	lambda   *lambda.Client
	iam      *iam.Client
	sqs      *sqs.Client
	sns      *sns.Client
	dynamodb *dynamodb.Client
	s3       *s3.Client
	apis     *apigatewayv2.Client
	events   *eventbridge.Client
	ssm      *ssm.Client
	secrets  *secretsmanager.Client
	logs     *cloudwatchlogs.Client

	// Problems from the pass in progress, reset by each sweepOnce.
	errs []string
}

func newSweepClients(ctx context.Context, cfg aws.Config) *sweepClients {
	return &sweepClients{
		ctx:      ctx,
		lambda:   lambda.NewFromConfig(cfg),
		iam:      iam.NewFromConfig(cfg),
		sqs:      sqs.NewFromConfig(cfg),
		sns:      sns.NewFromConfig(cfg),
		dynamodb: dynamodb.NewFromConfig(cfg),
		s3:       s3.NewFromConfig(cfg),
		apis:     apigatewayv2.NewFromConfig(cfg),
		events:   eventbridge.NewFromConfig(cfg),
		ssm:      ssm.NewFromConfig(cfg),
		secrets:  secretsmanager.NewFromConfig(cfg),
		logs:     cloudwatchlogs.NewFromConfig(cfg),
	}
}

func (c *sweepClients) sweepOnce(scopes []string) []leakedResource {
	c.errs = nil

	var leaks []leakedResource
	leaks = append(leaks, c.leakedLambdaFunctions(scopes)...)
	leaks = append(leaks, c.leakedIAMRoles(scopes)...)
	leaks = append(leaks, c.leakedQueues(scopes)...)
	leaks = append(leaks, c.leakedTopics(scopes)...)
	leaks = append(leaks, c.leakedTables(scopes)...)
	leaks = append(leaks, c.leakedBuckets(scopes)...)
	leaks = append(leaks, c.leakedAPIs(scopes)...)
	leaks = append(leaks, c.leakedRules(scopes)...)
	leaks = append(leaks, c.leakedSSMParameters(scopes)...)
	leaks = append(leaks, c.leakedSecrets(scopes)...)
	return leaks
}

// Removes a resource this run deployed. Ordering within a kind is handled here;
// dependencies between kinds are handled by the sweep re-running until settled.
func (c *sweepClients) deleteLeak(leak leakedResource) error {
	handle := leak.deleteHandle()

	switch leak.kind {
	case kindLambdaFunction:
		_, err := c.lambda.DeleteFunction(c.ctx, &lambda.DeleteFunctionInput{
			FunctionName: aws.String(handle),
		})
		return err
	case kindIAMRole:
		return c.deleteRole(handle)
	case kindSQSQueue:
		_, err := c.sqs.DeleteQueue(c.ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(handle)})
		return err
	case kindSNSTopic:
		_, err := c.sns.DeleteTopic(c.ctx, &sns.DeleteTopicInput{TopicArn: aws.String(handle)})
		return err
	case kindDynamoDBTable:
		_, err := c.dynamodb.DeleteTable(c.ctx, &dynamodb.DeleteTableInput{
			TableName: aws.String(handle),
		})
		return err
	case kindS3Bucket:
		return c.deleteBucket(handle)
	case kindAPI:
		_, err := c.apis.DeleteApi(c.ctx, &apigatewayv2.DeleteApiInput{ApiId: aws.String(handle)})
		return err
	case kindEventBridgeRule:
		return c.deleteRule(handle)
	case kindSSMParameter:
		_, err := c.ssm.DeleteParameter(c.ctx, &ssm.DeleteParameterInput{
			Name: aws.String(handle),
		})
		return err
	case kindSecret:
		_, err := c.secrets.DeleteSecret(c.ctx, &secretsmanager.DeleteSecretInput{
			SecretId: aws.String(handle),
			// No recovery window: a leaked test secret left pending deletion still
			// occupies its name, so the next run collides with it.
			ForceDeleteWithoutRecovery: aws.Bool(true),
		})
		return err
	}

	return fmt.Errorf("no delete implemented for %q", leak.kind)
}

// A role cannot be deleted while anything is attached to it.
func (c *sweepClients) deleteRole(roleName string) error {
	attached, err := c.iam.ListAttachedRolePolicies(c.ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil && !alreadyGone(err) {
		return err
	}
	if attached != nil {
		for _, policy := range attached.AttachedPolicies {
			_, err := c.iam.DetachRolePolicy(c.ctx, &iam.DetachRolePolicyInput{
				RoleName:  aws.String(roleName),
				PolicyArn: policy.PolicyArn,
			})
			if err != nil && !alreadyGone(err) {
				return err
			}
		}
	}

	inline, err := c.iam.ListRolePolicies(c.ctx, &iam.ListRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil && !alreadyGone(err) {
		return err
	}
	if inline != nil {
		for _, policyName := range inline.PolicyNames {
			_, err := c.iam.DeleteRolePolicy(c.ctx, &iam.DeleteRolePolicyInput{
				RoleName:   aws.String(roleName),
				PolicyName: aws.String(policyName),
			})
			if err != nil && !alreadyGone(err) {
				return err
			}
		}
	}

	_, err = c.iam.DeleteRole(c.ctx, &iam.DeleteRoleInput{RoleName: aws.String(roleName)})

	return err
}

// A bucket must be empty before it can be deleted, including every non-current
// version and delete marker when versioning was ever enabled.
func (c *sweepClients) deleteBucket(bucket string) error {
	var keyMarker, versionMarker *string
	for {
		listed, err := c.s3.ListObjectVersions(c.ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(bucket),
			KeyMarker:       keyMarker,
			VersionIdMarker: versionMarker,
		})
		if err != nil {
			if alreadyGone(err) {
				break
			}
			return err
		}

		objects := []s3types.ObjectIdentifier{}
		for _, version := range listed.Versions {
			objects = append(objects, s3types.ObjectIdentifier{
				Key: version.Key, VersionId: version.VersionId,
			})
		}
		for _, marker := range listed.DeleteMarkers {
			objects = append(objects, s3types.ObjectIdentifier{
				Key: marker.Key, VersionId: marker.VersionId,
			})
		}

		if len(objects) > 0 {
			_, err = c.s3.DeleteObjects(c.ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &s3types.Delete{Objects: objects, Quiet: aws.Bool(true)},
			})
			if err != nil && !alreadyGone(err) {
				return err
			}
		}

		if !aws.ToBool(listed.IsTruncated) {
			break
		}
		keyMarker = listed.NextKeyMarker
		versionMarker = listed.NextVersionIdMarker
	}

	_, err := c.s3.DeleteBucket(c.ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})

	return err
}

// A rule cannot be deleted while it still has targets.
func (c *sweepClients) deleteRule(ruleName string) error {
	targets, err := c.events.ListTargetsByRule(c.ctx, &eventbridge.ListTargetsByRuleInput{
		Rule: aws.String(ruleName),
	})
	if err != nil && !alreadyGone(err) {
		return err
	}
	if targets != nil && len(targets.Targets) > 0 {
		ids := []string{}
		for _, target := range targets.Targets {
			ids = append(ids, aws.ToString(target.Id))
		}
		_, err = c.events.RemoveTargets(c.ctx, &eventbridge.RemoveTargetsInput{
			Rule: aws.String(ruleName),
			Ids:  ids,
		})
		if err != nil && !alreadyGone(err) {
			return err
		}
	}

	_, err = c.events.DeleteRule(c.ctx, &eventbridge.DeleteRuleInput{Name: aws.String(ruleName)})

	return err
}

// Removes the log groups Lambda created for this run's functions.
//
// Not treated as leaks: nothing in the blueprint declares them, so no teardown
// step failed by leaving them behind. They are removed because they otherwise
// accumulate in the account forever, one per function per run.
func (c *sweepClients) tidyLogGroups(scopes []string) int {
	removed := 0
	var token *string
	for {
		out, err := c.logs.DescribeLogGroups(c.ctx, &cloudwatchlogs.DescribeLogGroupsInput{
			NextToken: token,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "leak sweep: describe log groups: %v\n", err)
			return removed
		}
		for _, group := range out.LogGroups {
			name := aws.ToString(group.LogGroupName)
			if !nameInScopes(name, scopes) {
				continue
			}
			_, err := c.logs.DeleteLogGroup(c.ctx, &cloudwatchlogs.DeleteLogGroupInput{
				LogGroupName: aws.String(name),
			})
			if err == nil || alreadyGone(err) {
				removed++
			}
		}
		if out.NextToken == nil {
			return removed
		}
		token = out.NextToken
	}
}

// Whether an error means the resource is already gone, which is the expected
// outcome when a delete races the destroy that was already removing it.
func alreadyGone(err error) bool {
	if err == nil {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchEntity", "ResourceNotFoundException", "NoSuchBucket",
			"NotFoundException", "ResourceNotFoundFault", "AWS.SimpleQueueService.NonExistentQueue",
			"ParameterNotFound", "NoSuchKey", "NotFound":
			return true
		}
	}

	return strings.Contains(err.Error(), "does not exist")
}

func nameInScopes(name string, scopes []string) bool {
	for _, scope := range scopes {
		if strings.Contains(name, scope) {
			return true
		}
	}
	return false
}

// legacyUnscopedShape matches the two historical name shapes emitted before
// physical naming was app-scoped. Nothing should ever deploy them again, so
// any occurrence is flagged regardless of run scope (regression guard).
func legacyUnscopedShape(name string) bool {
	return strings.Contains(name, "_topic_forwarder") ||
		strings.HasPrefix(name, "celerityLambdaExec_")
}

// Builds a leak, recording whether it can be attributed to this run and so
// whether the sweep may remove it.
func newLeak(kind string, id string, scoped bool) leakedResource {
	return leakedResource{kind: kind, id: id, scoped: scoped}
}

func (c *sweepClients) sweepError(operation string, err error) []leakedResource {
	c.errs = append(c.errs, fmt.Sprintf("sweep error: %s: %v", operation, err))
	return nil
}

func (c *sweepClients) leakedLambdaFunctions(scopes []string) []leakedResource {
	var leaks []leakedResource
	var marker *string
	for {
		out, err := c.lambda.ListFunctions(c.ctx, &lambda.ListFunctionsInput{Marker: marker})
		if err != nil {
			return c.sweepError("list lambda functions", err)
		}
		for _, fn := range out.Functions {
			name := aws.ToString(fn.FunctionName)
			scoped := nameInScopes(name, scopes)
			if scoped || legacyUnscopedShape(name) {
				leaks = append(leaks, newLeak(kindLambdaFunction, name, scoped))
			}
		}
		if out.NextMarker == nil {
			return leaks
		}
		marker = out.NextMarker
	}
}

func (c *sweepClients) leakedIAMRoles(scopes []string) []leakedResource {
	var leaks []leakedResource
	var marker *string
	for {
		out, err := c.iam.ListRoles(c.ctx, &iam.ListRolesInput{Marker: marker})
		if err != nil {
			return c.sweepError("list iam roles", err)
		}
		for _, role := range out.Roles {
			name := aws.ToString(role.RoleName)
			scoped := nameInScopes(name, scopes)
			if scoped || legacyUnscopedShape(name) {
				leaks = append(leaks, newLeak(kindIAMRole, name, scoped))
			}
		}
		if !out.IsTruncated {
			return leaks
		}
		marker = out.Marker
	}
}

func (c *sweepClients) leakedQueues(scopes []string) []leakedResource {
	var leaks []leakedResource
	for _, scope := range scopes {
		var token *string
		for {
			out, err := c.sqs.ListQueues(c.ctx, &sqs.ListQueuesInput{
				QueueNamePrefix: aws.String(scope),
				NextToken:       token,
			})
			if err != nil {
				return c.sweepError("list sqs queues", err)
			}
			for _, url := range out.QueueUrls {
				// Listed by scope prefix, so every result is attributable by construction.
				leaks = append(leaks, newLeak(kindSQSQueue, url, true))
			}
			if out.NextToken == nil {
				break
			}
			token = out.NextToken
		}
	}
	return leaks
}

func (c *sweepClients) leakedTopics(scopes []string) []leakedResource {
	var leaks []leakedResource
	var token *string
	for {
		out, err := c.sns.ListTopics(c.ctx, &sns.ListTopicsInput{NextToken: token})
		if err != nil {
			return c.sweepError("list sns topics", err)
		}
		for _, topic := range out.Topics {
			arn := aws.ToString(topic.TopicArn)
			if nameInScopes(arn, scopes) {
				leaks = append(leaks, newLeak(kindSNSTopic, arn, true))
			}
		}
		if out.NextToken == nil {
			return leaks
		}
		token = out.NextToken
	}
}

func (c *sweepClients) leakedTables(scopes []string) []leakedResource {
	var leaks []leakedResource
	var start *string
	for {
		out, err := c.dynamodb.ListTables(c.ctx, &dynamodb.ListTablesInput{
			ExclusiveStartTableName: start,
		})
		if err != nil {
			return c.sweepError("list dynamodb tables", err)
		}
		for _, name := range out.TableNames {
			if nameInScopes(name, scopes) {
				leaks = append(leaks, newLeak(kindDynamoDBTable, name, true))
			}
		}
		if out.LastEvaluatedTableName == nil {
			return leaks
		}
		start = out.LastEvaluatedTableName
	}
}

func (c *sweepClients) leakedBuckets(scopes []string) []leakedResource {
	out, err := c.s3.ListBuckets(c.ctx, &s3.ListBucketsInput{})
	if err != nil {
		return c.sweepError("list s3 buckets", err)
	}
	var leaks []leakedResource
	for _, bucket := range out.Buckets {
		name := aws.ToString(bucket.Name)
		if nameInScopes(name, scopes) {
			leaks = append(leaks, newLeak(kindS3Bucket, name, true))
		}
	}
	return leaks
}

func (c *sweepClients) leakedAPIs(scopes []string) []leakedResource {
	var leaks []leakedResource
	var token *string
	for {
		out, err := c.apis.GetApis(c.ctx, &apigatewayv2.GetApisInput{NextToken: token})
		if err != nil {
			return c.sweepError("list apigatewayv2 apis", err)
		}
		for _, api := range out.Items {
			name := aws.ToString(api.Name)
			if nameInScopes(name, scopes) {
				leak := newLeak(kindAPI, name, true)
				// Listed by name, deleted by id.
				leak.handle = aws.ToString(api.ApiId)
				leaks = append(leaks, leak)
			}
		}
		if out.NextToken == nil {
			return leaks
		}
		token = out.NextToken
	}
}

func (c *sweepClients) leakedRules(scopes []string) []leakedResource {
	var leaks []leakedResource
	for _, scope := range scopes {
		var token *string
		for {
			out, err := c.events.ListRules(c.ctx, &eventbridge.ListRulesInput{
				NamePrefix: aws.String(scope),
				NextToken:  token,
			})
			if err != nil {
				return c.sweepError("list eventbridge rules", err)
			}
			for _, rule := range out.Rules {
				leaks = append(leaks, newLeak(kindEventBridgeRule, aws.ToString(rule.Name), true))
			}
			if out.NextToken == nil {
				break
			}
			token = out.NextToken
		}
	}
	return leaks
}

// Flags any parameter under the /celerity tree whose path
// contains a registered app name (the internal resources config store and user
// parameter-store configs are all written under /celerity/<appName>/...).
func (c *sweepClients) leakedSSMParameters(scopes []string) []leakedResource {
	var leaks []leakedResource
	var token *string
	for {
		out, err := c.ssm.GetParametersByPath(c.ctx, &ssm.GetParametersByPathInput{
			Path:      aws.String("/celerity"),
			Recursive: aws.Bool(true),
			NextToken: token,
		})
		if err != nil {
			return c.sweepError("get ssm parameters under /celerity", err)
		}
		for _, param := range out.Parameters {
			name := aws.ToString(param.Name)
			if nameInScopes(name, scopes) {
				leaks = append(leaks, newLeak(kindSSMParameter, name, true))
			}
		}
		if out.NextToken == nil {
			return leaks
		}
		token = out.NextToken
	}
}

// Flags any Secrets Manager secret whose name contains a
// registered app name (secret-mode config stores are created under
// /celerity/<appName>/...). Secrets scheduled for deletion still appear in
// ListSecrets, so deleted-with-recovery-window secrets are excluded — only
// secrets with no pending deletion are leaks.
func (c *sweepClients) leakedSecrets(scopes []string) []leakedResource {
	var leaks []leakedResource
	var token *string
	for {
		out, err := c.secrets.ListSecrets(c.ctx, &secretsmanager.ListSecretsInput{
			NextToken: token,
		})
		if err != nil {
			return c.sweepError("list secretsmanager secrets", err)
		}
		for _, secret := range out.SecretList {
			name := aws.ToString(secret.Name)
			if secret.DeletedDate == nil && nameInScopes(name, scopes) {
				leaks = append(leaks, newLeak(kindSecret, name, true))
			}
		}
		if out.NextToken == nil {
			return leaks
		}
		token = out.NextToken
	}
}
