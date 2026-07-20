//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	// Destroys complete before test cleanup returns, but AWS list APIs can
	// keep returning deleted resources briefly; the sweep re-polls until the
	// account looks clean or this deadline passes.
	leakSweepTimeout  = 3 * time.Minute
	leakSweepInterval = 10 * time.Second
)

// Each Setup registers its run-unique name scope (NamePrefix == AppName) here
// so the post-run leak sweep in TestMain knows which deployed names belong to
// this process.
var sweepRegistry = struct {
	mu     sync.Mutex
	scopes []string
}{}

func registerSweepScope(scope string) {
	sweepRegistry.mu.Lock()
	defer sweepRegistry.mu.Unlock()
	sweepRegistry.scopes = append(sweepRegistry.scopes, scope)
}

func registeredSweepScopes() []string {
	sweepRegistry.mu.Lock()
	defer sweepRegistry.mu.Unlock()
	scopes := make([]string, len(sweepRegistry.scopes))
	copy(scopes, sweepRegistry.scopes)
	return scopes
}

// Lists AWS resources whose names belong to any scope registered
// during the run (plus the never-prefixed legacy name shapes) after the suite
// has finished and destroys have run. Returns true when leaks (or persistent
// sweep failures) remain after the polling deadline, so TestMain can fail the
// run even when every test passed. A no-op when AWS_REGION is unset or no
// harness ever registered a scope (the suite skipped).
func runLeakSweep() bool {
	region := os.Getenv("AWS_REGION")
	scopes := registeredSweepScopes()
	if region == "" || len(scopes) == 0 {
		return false
	}

	ctx := context.Background()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		// Fail closed: a sweep that cannot run must not pass silently.
		fmt.Fprintf(os.Stderr, "leak sweep: load AWS config: %v\n", err)
		return true
	}

	leaks := newSweepClients(ctx, cfg).poll(scopes)
	if len(leaks) == 0 {
		fmt.Fprintln(os.Stderr, "leak sweep: no leaked resources found")
		return false
	}

	fmt.Fprintf(os.Stderr, "leak sweep: %d leaked resource(s) after %s:\n", len(leaks), leakSweepTimeout)
	for _, leak := range leaks {
		fmt.Fprintf(os.Stderr, "  - %s\n", leak)
	}
	return true
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
	}
}

func (c *sweepClients) poll(scopes []string) []string {
	deadline := time.Now().Add(leakSweepTimeout)
	for {
		leaks := c.sweepOnce(scopes)
		if len(leaks) == 0 || time.Now().After(deadline) {
			return leaks
		}
		time.Sleep(leakSweepInterval)
	}
}

func (c *sweepClients) sweepOnce(scopes []string) []string {
	var leaks []string
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

func sweepError(operation string, err error) []string {
	return []string{fmt.Sprintf("sweep error: %s: %v", operation, err)}
}

func (c *sweepClients) leakedLambdaFunctions(scopes []string) []string {
	var leaks []string
	var marker *string
	for {
		out, err := c.lambda.ListFunctions(c.ctx, &lambda.ListFunctionsInput{Marker: marker})
		if err != nil {
			return sweepError("list lambda functions", err)
		}
		for _, fn := range out.Functions {
			name := aws.ToString(fn.FunctionName)
			if nameInScopes(name, scopes) || legacyUnscopedShape(name) {
				leaks = append(leaks, "lambda function: "+name)
			}
		}
		if out.NextMarker == nil {
			return leaks
		}
		marker = out.NextMarker
	}
}

func (c *sweepClients) leakedIAMRoles(scopes []string) []string {
	var leaks []string
	var marker *string
	for {
		out, err := c.iam.ListRoles(c.ctx, &iam.ListRolesInput{Marker: marker})
		if err != nil {
			return sweepError("list iam roles", err)
		}
		for _, role := range out.Roles {
			name := aws.ToString(role.RoleName)
			if nameInScopes(name, scopes) || legacyUnscopedShape(name) {
				leaks = append(leaks, "iam role: "+name)
			}
		}
		if !out.IsTruncated {
			return leaks
		}
		marker = out.Marker
	}
}

func (c *sweepClients) leakedQueues(scopes []string) []string {
	var leaks []string
	for _, scope := range scopes {
		var token *string
		for {
			out, err := c.sqs.ListQueues(c.ctx, &sqs.ListQueuesInput{
				QueueNamePrefix: aws.String(scope),
				NextToken:       token,
			})
			if err != nil {
				return sweepError("list sqs queues", err)
			}
			for _, url := range out.QueueUrls {
				leaks = append(leaks, "sqs queue: "+url)
			}
			if out.NextToken == nil {
				break
			}
			token = out.NextToken
		}
	}
	return leaks
}

func (c *sweepClients) leakedTopics(scopes []string) []string {
	var leaks []string
	var token *string
	for {
		out, err := c.sns.ListTopics(c.ctx, &sns.ListTopicsInput{NextToken: token})
		if err != nil {
			return sweepError("list sns topics", err)
		}
		for _, topic := range out.Topics {
			arn := aws.ToString(topic.TopicArn)
			if nameInScopes(arn, scopes) {
				leaks = append(leaks, "sns topic: "+arn)
			}
		}
		if out.NextToken == nil {
			return leaks
		}
		token = out.NextToken
	}
}

func (c *sweepClients) leakedTables(scopes []string) []string {
	var leaks []string
	var start *string
	for {
		out, err := c.dynamodb.ListTables(c.ctx, &dynamodb.ListTablesInput{
			ExclusiveStartTableName: start,
		})
		if err != nil {
			return sweepError("list dynamodb tables", err)
		}
		for _, name := range out.TableNames {
			if nameInScopes(name, scopes) {
				leaks = append(leaks, "dynamodb table: "+name)
			}
		}
		if out.LastEvaluatedTableName == nil {
			return leaks
		}
		start = out.LastEvaluatedTableName
	}
}

func (c *sweepClients) leakedBuckets(scopes []string) []string {
	out, err := c.s3.ListBuckets(c.ctx, &s3.ListBucketsInput{})
	if err != nil {
		return sweepError("list s3 buckets", err)
	}
	var leaks []string
	for _, bucket := range out.Buckets {
		name := aws.ToString(bucket.Name)
		if nameInScopes(name, scopes) {
			leaks = append(leaks, "s3 bucket: "+name)
		}
	}
	return leaks
}

func (c *sweepClients) leakedAPIs(scopes []string) []string {
	var leaks []string
	var token *string
	for {
		out, err := c.apis.GetApis(c.ctx, &apigatewayv2.GetApisInput{NextToken: token})
		if err != nil {
			return sweepError("list apigatewayv2 apis", err)
		}
		for _, api := range out.Items {
			name := aws.ToString(api.Name)
			if nameInScopes(name, scopes) {
				leaks = append(leaks, "apigatewayv2 api: "+name)
			}
		}
		if out.NextToken == nil {
			return leaks
		}
		token = out.NextToken
	}
}

func (c *sweepClients) leakedRules(scopes []string) []string {
	var leaks []string
	for _, scope := range scopes {
		var token *string
		for {
			out, err := c.events.ListRules(c.ctx, &eventbridge.ListRulesInput{
				NamePrefix: aws.String(scope),
				NextToken:  token,
			})
			if err != nil {
				return sweepError("list eventbridge rules", err)
			}
			for _, rule := range out.Rules {
				leaks = append(leaks, "eventbridge rule: "+aws.ToString(rule.Name))
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
func (c *sweepClients) leakedSSMParameters(scopes []string) []string {
	var leaks []string
	var token *string
	for {
		out, err := c.ssm.GetParametersByPath(c.ctx, &ssm.GetParametersByPathInput{
			Path:      aws.String("/celerity"),
			Recursive: aws.Bool(true),
			NextToken: token,
		})
		if err != nil {
			return sweepError("get ssm parameters under /celerity", err)
		}
		for _, param := range out.Parameters {
			name := aws.ToString(param.Name)
			if nameInScopes(name, scopes) {
				leaks = append(leaks, "ssm parameter: "+name)
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
func (c *sweepClients) leakedSecrets(scopes []string) []string {
	var leaks []string
	var token *string
	for {
		out, err := c.secrets.ListSecrets(c.ctx, &secretsmanager.ListSecretsInput{
			NextToken: token,
		})
		if err != nil {
			return sweepError("list secretsmanager secrets", err)
		}
		for _, secret := range out.SecretList {
			name := aws.ToString(secret.Name)
			if secret.DeletedDate == nil && nameInScopes(name, scopes) {
				leaks = append(leaks, "secretsmanager secret: "+name)
			}
		}
		if out.NextToken == nil {
			return leaks
		}
		token = out.NextToken
	}
}
