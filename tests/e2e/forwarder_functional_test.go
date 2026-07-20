//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

const messageDeliveryTimeout = 2 * time.Minute

// TestForwarderDeliversQueueMessagesToTopic deploys the queue::topic fixture
// and functionally proves the transformer-authored inline forwarder lambda:
// a message sent to the deployed source SQS queue must arrive on an observer
// queue subscribed to the deployed SNS topic, which can only happen if the
// forwarder function polls the queue and republishes to the topic.
func TestForwarderDeliversQueueMessagesToTopic(t *testing.T) {
	t.Parallel()
	h := Setup(t)
	manifestPath := h.PrestageArtifacts(t)

	inst := h.Deploy(t, "queue_topic_forwarder.blueprint", manifestPath, nil)

	// The abstract spec.id exports are rewritten by the transformer's property
	// maps to the concrete SQS arn / SNS topicArn.
	topicARN := core.StringValue(inst.Export(t, "eventsTopicArn"))
	require.NotEmpty(t, topicARN, "expected the deployed topic ARN export")

	// The queue URL has no abstract property mapping, so it is read from the
	// concrete resource's deployed state instead of an export.
	queueURL := stringField(inst.ResourceSpec(t, "sourceQueue_sqs_queue"), "queueUrl")
	require.NotEmpty(t, queueURL, "expected the deployed source queue URL in state")

	fwdFunctionName := stringField(inst.ResourceSpec(t, "sourceQueue_topic_forwarder"), "functionName")
	require.NotEmpty(t, fwdFunctionName, "expected the forwarder lambda's function name in state")
	assertForwardTopicEnvVarResolved(t, h, fwdFunctionName, topicARN)
	assertAllFunctionEnvVarsResolved(t, h, fwdFunctionName)

	sqsClient := sqs.NewFromConfig(h.AWSConfig)
	snsClient := sns.NewFromConfig(h.AWSConfig)

	observerURL, observerARN := createObserverQueue(t, h, sqsClient, topicARN)
	subscribeObserverToTopic(t, h, snsClient, topicARN, observerARN)

	marker := "celerity-e2e-forwarder-" + uniqueSuffix()
	_, err := sqsClient.SendMessage(h.Ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(marker),
	})
	require.NoError(t, err, "send test message to the deployed source queue")

	waitFor(t, messageDeliveryTimeout, time.Second,
		"forwarded message to arrive on the observer queue",
		func() (bool, error) {
			return observerReceivedMarker(h, sqsClient, observerURL, marker)
		})
}

// Asserts the forwarder lambda carries at
// least one CELERITY_FORWARD_TOPIC_ARN_<n> env var resolved to the deployed
// topic's ARN (the routing table the inline forwarder publishes from).
func assertForwardTopicEnvVarResolved(
	t *testing.T,
	h *Harness,
	functionName string,
	topicARN string,
) {
	t.Helper()
	client := lambda.NewFromConfig(h.AWSConfig)
	waitFor(t, wiringAssertTimeout, 5*time.Second,
		fmt.Sprintf("a CELERITY_FORWARD_TOPIC_ARN_* env var on %s resolving to %s", functionName, topicARN),
		func() (bool, error) {
			vars, err := functionEnvVars(h, client, functionName)
			if err != nil {
				return false, err
			}
			for name, value := range vars {
				if strings.HasPrefix(name, "CELERITY_FORWARD_TOPIC_ARN") && value == topicARN {
					return true, nil
				}
			}
			return false, nil
		})
}

// Provisions a throwaway SQS queue (outside the blueprint)
// that the deployed topic is allowed to deliver into, so the test can observe
// what the forwarder publishes. Deleted via t.Cleanup.
func createObserverQueue(
	t *testing.T,
	h *Harness,
	client *sqs.Client,
	topicARN string,
) (queueURL string, queueARN string) {
	t.Helper()

	created, err := client.CreateQueue(h.Ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(h.Name("observer")),
	})
	require.NoError(t, err, "create observer queue")
	queueURL = aws.ToString(created.QueueUrl)

	t.Cleanup(func() {
		_, err := client.DeleteQueue(h.Ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(queueURL)})
		if err != nil {
			t.Errorf("cleanup: delete observer queue %s: %v", queueURL, err)
		}
	})

	attrs, err := client.GetQueueAttributes(h.Ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	require.NoError(t, err, "read observer queue ARN")
	queueARN = attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	require.NotEmpty(t, queueARN, "observer queue ARN")

	policy, err := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":    "Allow",
			"Principal": map[string]any{"Service": "sns.amazonaws.com"},
			"Action":    "sqs:SendMessage",
			"Resource":  queueARN,
			"Condition": map[string]any{
				"ArnEquals": map[string]any{"aws:SourceArn": topicARN},
			},
		}},
	})
	require.NoError(t, err, "marshal observer queue policy")

	_, err = client.SetQueueAttributes(h.Ctx, &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(queueURL),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNamePolicy): string(policy),
		},
	})
	require.NoError(t, err, "allow the deployed topic to send to the observer queue")
	return queueURL, queueARN
}

// Subscribes the observer SQS queue to the deployed topic.
func subscribeObserverToTopic(
	t *testing.T,
	h *Harness,
	client *sns.Client,
	topicARN string,
	queueARN string,
) {
	t.Helper()
	subscribed, err := client.Subscribe(h.Ctx, &sns.SubscribeInput{
		TopicArn: aws.String(topicARN),
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(queueARN),
		Attributes: map[string]string{
			"RawMessageDelivery": "true",
		},
		ReturnSubscriptionArn: true,
	})
	require.NoError(t, err, "subscribe observer queue to the deployed topic")

	subscriptionARN := aws.ToString(subscribed.SubscriptionArn)
	t.Cleanup(func() {
		_, err := client.Unsubscribe(h.Ctx, &sns.UnsubscribeInput{
			SubscriptionArn: aws.String(subscriptionARN),
		})
		if err != nil {
			t.Errorf("cleanup: unsubscribe observer queue: %v", err)
		}
	})
}

// observerReceivedMarker long-polls the observer queue and reports whether any
// received body carries the marker. Contains (not equals) keeps the assertion
// valid whether or not the forwarder republishes the body verbatim.
func observerReceivedMarker(
	h *Harness,
	client *sqs.Client,
	queueURL string,
	marker string,
) (bool, error) {
	received, err := client.ReceiveMessage(h.Ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     10,
	})
	if err != nil {
		return false, fmt.Errorf("receive from observer queue: %w", err)
	}
	for _, msg := range received.Messages {
		if strings.Contains(aws.ToString(msg.Body), marker) {
			return true, nil
		}
	}
	return false, nil
}
