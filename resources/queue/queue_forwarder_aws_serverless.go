package queue

import (
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// forwardTopicEnvVar is the environment variable the intermediary forwarder
	// reads the target topic ARN from. The aws/lambda/function::aws/sns/topic link
	// injects it under this name (set via the envVarName annotation below).
	forwardTopicEnvVar = "CELERITY_FORWARD_TOPIC_ARN"

	// forwarderRuntime is the AWS Lambda runtime the intermediary forwarder runs
	// under. The forwarder is language-agnostic infrastructure glue, so a single
	// runtime is used regardless of the application's language; nodejs24.x ships the
	// AWS SDK v3, so the inline handler needs no bundled dependencies.
	forwarderRuntime = "nodejs24.x"

	forwarderMemorySize = 128
	forwarderTimeout    = 30
)

// forwarderSource is the inline handler the intermediary forwarder runs: it
// republishes each SQS record's body to the target SNS topic. The topic ARN is
// read from the env var the function::sns link injects. FIFO group ordering is
// preserved by passing MessageGroupId through, using the SQS message id as the
// deduplication id.
//
// This is emitted inline via aws/lambda/function.code.zipFile because there is no
// build-manifest artifact for intermediary functions on aws-serverless. Inline
// code is write-only in the provider, so a change to this source only redeploys
// after the code object is refreshed.
const forwarderSource = `const { SNSClient, PublishCommand } = require("@aws-sdk/client-sns");
const sns = new SNSClient({});
const TopicArn = process.env.CELERITY_FORWARD_TOPIC_ARN;
exports.handler = async (event) => {
  const records = event.Records || [];
  await Promise.all(records.map((record) => {
    const params = { TopicArn, Message: record.body };
    const groupId = record.attributes && record.attributes.MessageGroupId;
    if (groupId) {
      params.MessageGroupId = groupId;
      params.MessageDeduplicationId = record.messageId;
    }
    return sns.send(new PublishCommand(params));
  }));
};
`

// forwardLabelKey is the synthetic label that binds the source queue's link
// selector to the intermediary forwarder function, activating the
// aws/sqs/queue::aws/lambda/function poll link (which creates the event source
// mapping and injects the SQS-receive IAM). It is namespaced per queue->topic edge
// so a queue forwarding to several topics keeps one distinct wiring label each.
func forwardLabelKey(queueName, topicName string) string {
	return fmt.Sprintf("celerity.queue.forward.%s.%s", queueName, topicName)
}

func forwarderFunctionName(queueName, topicName string) string {
	return fmt.Sprintf("%s_to_%s_forwarder", queueName, topicName)
}

func forwarderRoleName(queueName, topicName string) string {
	return fmt.Sprintf("%s_to_%s_forwarder_role", queueName, topicName)
}

// buildTopicForwarder emits the intermediary function + execution role for one
// queue->topic edge. The function is triggered by the source queue (via the
// synthetic forward label added to the queue's link selector) and publishes to the
// topic (via its own link selector matching the topic's labels). Both provider
// links inject the IAM the forwarder needs, so only a base execution role is
// emitted.
func buildTopicForwarder(queueName string, edge *TopicForwardEdge) (map[string]*schema.Resource, error) {
	funcName := forwarderFunctionName(queueName, edge.TopicName)
	roleName := forwarderRoleName(queueName, edge.TopicName)

	roleArnRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", roleName))
	if err != nil {
		return nil, err
	}

	funcMeta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: queueName,
				AbstractResourceType: "celerity/queue",
				ResourceCategory:     transformutils.ResourceCategoryCodeHosting,
			},
		),
		Labels: &schema.StringMap{Values: map[string]string{
			forwardLabelKey(queueName, edge.TopicName): "true",
		}},
	}
	// Rename the topic-ARN env var the function::sns link injects so the inline
	// handler can read a fixed, known name.
	envVarNameKey := fmt.Sprintf("aws.lambda.sns.%s.envVarName", topic.ConcreteResourceName(edge.TopicName))
	funcMeta.Annotations.Values[envVarNameKey] = pluginutils.StringToSubstitutions(forwardTopicEnvVar)

	funcRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/lambda/function"},
		Spec: core.MappingNodeFields(
			"functionName", core.MappingNodeFromString(funcName),
			"handler", core.MappingNodeFromString("index.handler"),
			"runtime", core.MappingNodeFromString(forwarderRuntime),
			"code", core.MappingNodeFields("zipFile", core.MappingNodeFromString(forwarderSource)),
			"memorySize", core.MappingNodeFromInt(forwarderMemorySize),
			"timeout", core.MappingNodeFromInt(forwarderTimeout),
			"role", roleArnRef,
		),
		Metadata: funcMeta,
		// The forwarder's own link selector matches the topic's labels, activating
		// the aws/lambda/function::aws/sns/topic link (publish grant + topic ARN env).
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: edge.SelectorLabels},
		},
	}

	roleRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/iam/role"},
		Spec: awslambda.SeedRoleSpec(roleName, &awslambda.RolePlan{}),
		Metadata: &schema.Metadata{
			Annotations: transformutils.TransformerBaseAnnotations(
				&transformutils.TransformerBaseAnnotationsInput{
					AbstractResourceName: queueName,
					AbstractResourceType: "celerity/queue",
					ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
				},
			),
		},
	}

	return map[string]*schema.Resource{
		funcName: funcRes,
		roleName: roleRes,
	}, nil
}

// queueLinkSelectorWithForwards returns the source queue's link selector augmented
// with a synthetic forward label per topic-forwarding edge. Bluelink label
// matching is per-label (each label forms an independent selector group), so
// adding the forward label creates a match to the forwarder function without
// disturbing the existing topic match (which is inert — aws/sqs/queue declares no
// link to aws/sns/topic). Returns the selector unchanged when there are no forwards.
func queueLinkSelectorWithForwards(r *ResolvedQueue) *schema.LinkSelector {
	if len(r.TopicForwards) == 0 {
		return r.Resource.LinkSelector
	}
	labels := map[string]string{}
	if r.Resource.LinkSelector != nil && r.Resource.LinkSelector.ByLabel != nil {
		for key, value := range r.Resource.LinkSelector.ByLabel.Values {
			labels[key] = value
		}
	}
	for _, edge := range r.TopicForwards {
		labels[forwardLabelKey(r.Name, edge.TopicName)] = "true"
	}
	return &schema.LinkSelector{ByLabel: &schema.StringMap{Values: labels}}
}
