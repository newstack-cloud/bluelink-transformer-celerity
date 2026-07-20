//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/stretchr/testify/require"
)

const (
	esmActiveTimeout    = 5 * time.Minute
	wiringAssertTimeout = 2 * time.Minute
)

// TestDeployEventWiring deploys the events fixture (handler::datastore,
// bucket::queue + bucket::topic notifications, a queue::queue dead-letter
// link, and a queue::consumer + datastore::consumer fan-in to one worker
// handler) and asserts the real AWS event wiring the transformer authored:
//  1. the worker's SQS event source mapping reaches the Enabled state,
//  2. the S3 bucket carries BOTH queue and topic notification configurations
//     (the whole-config-replace coexistence is the point),
//  3. the internal resources config store landed as SSM parameters under
//     /celerity/<appName>/resources,
//  4. the source queue's RedrivePolicy references the dead-letter queue's ARN,
//  5. the datastore's DynamoDB stream is enabled and a stream event source
//     mapping on the worker reaches Enabled,
//  6. every deployed function's env vars are fully resolved.
//
// The worker handler (processUpload) deliberately absorbs BOTH consumers
// (queue + datastore stream): the event-source exclusivity validation in
// links/handler_event_source_validation.go only rejects multiple event-source
// MARKER annotations on a handler, and "celerity.handler.consumer" is a single
// marker regardless of how many consumers link in; the emit machinery supports
// per-binding wiring (handler_consumer_binding.go). The two consumers carry
// distinct label KEYS (consumer= vs streamConsumer=) so the emitted function's
// label union keeps both source links resolvable.
//
// All the wiring is eventually consistent after deploy, so each assertion
// polls with an explicit deadline.
func TestDeployEventWiring(t *testing.T) {
	t.Parallel()
	h := Setup(t)
	manifestPath := h.PrestageArtifacts(t)

	inst := h.Deploy(t, "events.blueprint", manifestPath, nil)

	assertConsumerEventSourceMappingActive(t, h, inst)
	assertBucketNotificationConfigured(t, h, inst)
	assertResourcesStoreParametersExist(t, h)
	assertSourceQueueRedrivesToDLQ(t, h, inst)
	assertDatastoreStreamConsumerWired(t, h, inst)

	mainFunctionName := stringField(inst.ResourceSpec(t, "mainHandler_lambda_func"), "functionName")
	require.NotEmpty(t, mainFunctionName, "expected the main handler's function name in state")
	assertAllFunctionEnvVarsResolved(t, h, mainFunctionName, workerFunctionName(t, inst))
}

func workerFunctionName(t *testing.T, inst *DeployedInstance) string {
	t.Helper()
	functionName := stringField(inst.ResourceSpec(t, "processUpload_lambda_func"), "functionName")
	require.NotEmpty(t, functionName, "expected the worker handler's function name in state")
	return functionName
}

func assertConsumerEventSourceMappingActive(t *testing.T, h *Harness, inst *DeployedInstance) {
	t.Helper()
	functionName := workerFunctionName(t, inst)

	client := lambda.NewFromConfig(h.AWSConfig)
	waitFor(t, esmActiveTimeout, 5*time.Second,
		fmt.Sprintf("SQS event source mapping on %s to become Enabled", functionName),
		func() (bool, error) {
			mappings, err := client.ListEventSourceMappings(h.Ctx, &lambda.ListEventSourceMappingsInput{
				FunctionName: aws.String(functionName),
			})
			if err != nil {
				return false, fmt.Errorf("list event source mappings: %w", err)
			}
			// Creating/Enabling resolve to Enabled shortly after deploy, so
			// anything not yet Enabled is a retry rather than a failure. The
			// worker now also carries a DynamoDB stream mapping (asserted
			// separately), so only SQS-sourced mappings count here.
			for _, mapping := range mappings.EventSourceMappings {
				if strings.HasPrefix(aws.ToString(mapping.EventSourceArn), "arn:aws:sqs:") &&
					aws.ToString(mapping.State) == "Enabled" {
					return true, nil
				}
			}
			return false, nil
		})
}

func assertBucketNotificationConfigured(t *testing.T, h *Harness, inst *DeployedInstance) {
	t.Helper()
	bucketSpec := inst.ResourceSpec(t, "uploadsBucket_s3_bucket")
	bucketName := stringField(bucketSpec, "bucketName")
	if bucketName == "" {
		// Fall back to deriving the name from the bucket ARN
		// (arn:aws:s3:::<name>) when state only carries the ARN.
		bucketName = strings.TrimPrefix(stringField(bucketSpec, "arn"), "arn:aws:s3:::")
	}
	require.NotEmpty(t, bucketName, "expected the deployed bucket name in state")

	client := s3.NewFromConfig(h.AWSConfig)
	waitFor(t, wiringAssertTimeout, 5*time.Second,
		fmt.Sprintf("queue AND topic notification configurations on bucket %s", bucketName),
		func() (bool, error) {
			config, err := client.GetBucketNotificationConfiguration(
				h.Ctx,
				&s3.GetBucketNotificationConfigurationInput{Bucket: aws.String(bucketName)},
			)
			if err != nil {
				return false, fmt.Errorf("get bucket notification configuration: %w", err)
			}
			// S3 notification config is replaced whole per PUT, so both link
			// types coexisting proves the provider links merge rather than
			// clobber each other. The fixture scopes the topic to "deleted"
			// events because S3 rejects two configurations sharing an event
			// type with overlapping filters ("Configurations overlap"), the
			// queue keeps the default created events.
			return len(config.QueueConfigurations) > 0 && len(config.TopicConfigurations) > 0, nil
		})
}

func assertResourcesStoreParametersExist(t *testing.T, h *Harness) {
	t.Helper()
	storePath := fmt.Sprintf("/celerity/%s/resources", h.AppName)

	client := ssm.NewFromConfig(h.AWSConfig)
	waitFor(t, wiringAssertTimeout, 5*time.Second,
		fmt.Sprintf("resources store parameters under %s", storePath),
		func() (bool, error) {
			params, err := client.GetParametersByPath(h.Ctx, &ssm.GetParametersByPathInput{
				Path:      aws.String(storePath),
				Recursive: aws.Bool(true),
			})
			if err != nil {
				return false, fmt.Errorf("get parameters by path %s: %w", storePath, err)
			}
			return len(params.Parameters) > 0, nil
		})
}

// Asserts the queue::queue dead-letter link
// (uploadsQueue selects uploadsDLQ by label, with the
// celerity.queue.deadLetterMaxAttempts annotation) landed as a RedrivePolicy
// on the deployed source queue referencing the DLQ's ARN.
func assertSourceQueueRedrivesToDLQ(t *testing.T, h *Harness, inst *DeployedInstance) {
	t.Helper()
	queueURL := stringField(inst.ResourceSpec(t, "uploadsQueue_sqs_queue"), "queueUrl")
	require.NotEmpty(t, queueURL, "expected the deployed source queue URL in state")
	dlqARN := stringField(inst.ResourceSpec(t, "uploadsDLQ_sqs_queue"), "arn")
	require.NotEmpty(t, dlqARN, "expected the deployed dead-letter queue ARN in state")

	client := sqs.NewFromConfig(h.AWSConfig)
	waitFor(t, wiringAssertTimeout, 5*time.Second,
		fmt.Sprintf("redrive policy on source queue referencing DLQ %s", dlqARN),
		func() (bool, error) {
			attrs, err := client.GetQueueAttributes(h.Ctx, &sqs.GetQueueAttributesInput{
				QueueUrl: aws.String(queueURL),
				AttributeNames: []sqstypes.QueueAttributeName{
					sqstypes.QueueAttributeNameRedrivePolicy,
				},
			})
			if err != nil {
				return false, fmt.Errorf("get source queue attributes: %w", err)
			}
			redrivePolicy := attrs.Attributes[string(sqstypes.QueueAttributeNameRedrivePolicy)]
			return strings.Contains(redrivePolicy, dlqARN), nil
		})
}

// Asserts the datastore::consumer link:
// the deployed DynamoDB table's stream is enabled (the provider table::function
// link turns it on) and the worker function carries a stream event source
// mapping that reaches Enabled.
func assertDatastoreStreamConsumerWired(t *testing.T, h *Harness, inst *DeployedInstance) {
	t.Helper()
	tableName := deployedTableName(t, inst)
	functionName := workerFunctionName(t, inst)

	ddbClient := dynamodb.NewFromConfig(h.AWSConfig)
	streamARN := ""
	waitFor(t, esmActiveTimeout, 5*time.Second,
		fmt.Sprintf("DynamoDB stream to be enabled on table %s", tableName),
		func() (bool, error) {
			out, err := ddbClient.DescribeTable(h.Ctx, &dynamodb.DescribeTableInput{
				TableName: aws.String(tableName),
			})
			if err != nil {
				return false, fmt.Errorf("describe table %s: %w", tableName, err)
			}
			spec := out.Table.StreamSpecification
			if spec == nil || !aws.ToBool(spec.StreamEnabled) {
				return false, nil
			}
			streamARN = aws.ToString(out.Table.LatestStreamArn)
			return streamARN != "", nil
		})

	lambdaClient := lambda.NewFromConfig(h.AWSConfig)
	waitFor(t, esmActiveTimeout, 5*time.Second,
		fmt.Sprintf("stream event source mapping on %s for %s to become Enabled", functionName, streamARN),
		func() (bool, error) {
			mappings, err := lambdaClient.ListEventSourceMappings(h.Ctx, &lambda.ListEventSourceMappingsInput{
				FunctionName:   aws.String(functionName),
				EventSourceArn: aws.String(streamARN),
			})
			if err != nil {
				return false, fmt.Errorf("list stream event source mappings: %w", err)
			}
			for _, mapping := range mappings.EventSourceMappings {
				if aws.ToString(mapping.State) == "Enabled" {
					return true, nil
				}
			}
			return false, nil
		})
}

// Reads the deployed DynamoDB table's name from state,
// falling back to deriving it from the table ARN
// (arn:aws:dynamodb:<region>:<acct>:table/<name>) when the fixture leaves the
// abstract name unset and only the ARN is recorded.
func deployedTableName(t *testing.T, inst *DeployedInstance) string {
	t.Helper()
	tableSpec := inst.ResourceSpec(t, "eventsTable_dynamodb_table")
	tableName := stringField(tableSpec, "tableName")
	if tableName == "" {
		arn := stringField(tableSpec, "arn")
		if _, after, found := strings.Cut(arn, ":table/"); found {
			tableName, _, _ = strings.Cut(after, "/")
		}
	}
	require.NotEmpty(t, tableName, "expected the deployed DynamoDB table name (or ARN) in state")
	return tableName
}
