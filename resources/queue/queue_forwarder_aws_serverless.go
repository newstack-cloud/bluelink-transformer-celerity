package queue

import (
	"fmt"
	"maps"
	"sort"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// forwardTopicEnvVarPrefix is the prefix of the environment variables the
	// intermediary forwarder reads target topic ARNs from. One variable per target
	// topic (<prefix>_<index>) is injected by the aws/lambda/function::aws/sns/topic
	// link via the per-topic envVarName annotation; the inline handler publishes each
	// message to every variable with this prefix.
	forwardTopicEnvVarPrefix = "CELERITY_FORWARD_TOPIC_ARN"

	// forwarderRuntime is the AWS Lambda runtime the intermediary forwarder runs
	// under. The forwarder is language-agnostic infrastructure glue, so a single
	// runtime is used regardless of the application's language; nodejs24.x ships the
	// AWS SDK v3, so the inline handler needs no bundled dependencies.
	forwarderRuntime = "nodejs24.x"

	forwarderMemorySize = 128
	forwarderTimeout    = 30

	// forwarderReportBatchItemFailuresAnnotation is the aws/sqs/queue::aws/lambda/function
	// poll link annotation (AppliesTo the function) that enables the
	// ReportBatchItemFailures event source mapping function response type. The
	// forwarder always returns batchItemFailures (see forwarderSource), so this is
	// stamped unconditionally rather than being derived from consumer config.
	forwarderReportBatchItemFailuresAnnotation = "aws.sqs.lambda.reportBatchItemFailures"
)

// forwarderSource is the inline handler the intermediary forwarder runs: it
// republishes each SQS record's body to every target SNS topic. Target topic ARNs
// are discovered from the CELERITY_FORWARD_TOPIC_ARN* env vars the function::sns
// links inject. A single forwarder fans a message out to all topics (one SQS event
// source mapping — SQS is competing-consumers, so one forwarder per queue, not one
// per topic). FIFO group ordering is preserved by passing MessageGroupId through,
// using the SQS message id as the deduplication id.
//
// This is emitted inline via aws/lambda/function.code.zipFile because there is no
// build-manifest artifact for intermediary functions on aws-serverless. Inline code
// is write-only in the provider, so a change to this source only redeploys after
// the code object is refreshed.
const forwarderSource = `const { SNSClient, PublishCommand } = require("@aws-sdk/client-sns");
const sns = new SNSClient({});
const topicArns = Object.keys(process.env)
  .filter((key) => key.startsWith("CELERITY_FORWARD_TOPIC_ARN"))
  .map((key) => process.env[key])
  .filter(Boolean);
exports.handler = async (event) => {
  const records = event.Records || [];
  const batchItemFailures = [];
  for (const record of records) {
    const params = { Message: record.body };
    const groupId = record.attributes && record.attributes.MessageGroupId;
    if (groupId) {
      params.MessageGroupId = groupId;
      params.MessageDeduplicationId = record.messageId;
    }
    const results = await Promise.allSettled(
      topicArns.map((TopicArn) =>
        sns.send(new PublishCommand({ ...params, TopicArn }))
      )
    );
    // Report the record itself as failed if any topic publish failed, so SQS
    // only retries this record (not the whole batch). This is safe to retry:
    // topics already published to for this record will receive a duplicate,
    // which SNS/downstream consumers must tolerate, but no other record's
    // already-succeeded publishes are redone.
    if (results.some((result) => result.status === "rejected")) {
      batchItemFailures.push({ itemIdentifier: record.messageId });
    }
  }
  return { batchItemFailures };
};
`

// forwardLabelKey is the synthetic label that binds the source queue's link
// selector to its intermediary forwarder function, activating the
// aws/sqs/queue::aws/lambda/function poll link (which creates the event source
// mapping and injects the SQS-receive IAM). One label per queue: a single forwarder
// consumes the queue and fans out to all target topics.
func forwardLabelKey(queueName string) string {
	return fmt.Sprintf("celerity.queue.forward.%s", queueName)
}

func forwarderFunctionName(queueName string) string {
	return fmt.Sprintf("%s_topic_forwarder", queueName)
}

func forwarderRoleName(queueName string) string {
	return fmt.Sprintf("%s_topic_forwarder_role", queueName)
}

// buildTopicForwarder emits the single intermediary function + execution role that
// forwards the queue's messages to every target topic. The function is triggered by
// the source queue (via the synthetic forward label on the queue's link selector)
// and publishes to the topics (via its own link selector matching the union of the
// topics' labels). Each topic's function::sns link injects the topic ARN under a
// distinct CELERITY_FORWARD_TOPIC_ARN_<index> env var (renamed per topic). Both
// provider links inject the IAM the forwarder needs, so only a base role is emitted.
func buildTopicForwarder(queueName, appName string, edges []*TopicForwardEdge) (map[string]*schema.Resource, error) {
	funcName := forwarderFunctionName(queueName)
	roleName := forwarderRoleName(queueName)
	// Deployed names are app-scoped (blueprint resource names stay
	// app-agnostic) so two apps or concurrent runs sharing an account never
	// collide on create; both Lambda function names and IAM role names cap
	// at 64 chars.
	physicalFuncName := shared.AppScopedPhysicalName(appName, queueName+"-fwd", 64)
	physicalRoleName := shared.AppScopedPhysicalName(appName, queueName+"-fwd-role", 64)

	roleArnRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", roleName))
	if err != nil {
		return nil, err
	}

	// Deterministic order so env-var indices and the union selector are stable.
	sorted := append([]*TopicForwardEdge(nil), edges...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TopicName < sorted[j].TopicName })

	funcMeta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: queueName,
				AbstractResourceType: "celerity/queue",
				ResourceCategory:     transformutils.ResourceCategoryCodeHosting,
			},
		),
		Labels: &schema.StringMap{Values: map[string]string{
			forwardLabelKey(queueName): "true",
		}},
	}
	// The forwarder always reports per-record batch item failures (see
	// forwarderSource), so the event source mapping must be configured to honour
	// them; otherwise a partial failure would fail and redeliver the whole batch,
	// re-publishing to topics that already succeeded for unrelated records.
	funcMeta.Annotations.Values[forwarderReportBatchItemFailuresAnnotation] = pluginutils.StringToSubstitutions("true")

	// Union the topics' matched labels for the publish selector, and rename each
	// topic's injected ARN env var to a distinct CELERITY_FORWARD_TOPIC_ARN_<index>
	// the inline handler discovers by prefix.
	selectorLabels := map[string]string{}
	for index, edge := range sorted {
		maps.Copy(selectorLabels, edge.SelectorLabels)
		envVarNameKey := fmt.Sprintf("aws.lambda.sns.%s.envVarName", topic.ConcreteResourceName(edge.TopicName))
		funcMeta.Annotations.Values[envVarNameKey] = pluginutils.StringToSubstitutions(
			fmt.Sprintf("%s_%d", forwardTopicEnvVarPrefix, index))
	}

	funcRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/lambda/function"},
		Spec: core.MappingNodeFields(
			"functionName", core.MappingNodeFromString(physicalFuncName),
			"handler", core.MappingNodeFromString("index.handler"),
			"runtime", core.MappingNodeFromString(forwarderRuntime),
			"code", core.MappingNodeFields("zipFile", core.MappingNodeFromString(forwarderSource)),
			"memorySize", core.MappingNodeFromInt(forwarderMemorySize),
			"timeout", core.MappingNodeFromInt(forwarderTimeout),
			"role", roleArnRef,
		),
		Metadata: funcMeta,
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: selectorLabels},
		},
	}

	return map[string]*schema.Resource{
		funcName: funcRes,
		roleName: buildForwarderRole(queueName, physicalRoleName),
	}, nil
}

// buildForwarderRole is the forwarder's base execution role. Both provider links
// (sqs::function, function::sns) inject the SQS-receive and sns:Publish grants at
// deploy, so no inline policy is seeded.
func buildForwarderRole(queueName, physicalRoleName string) *schema.Resource {
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/iam/role"},
		Spec: awslambda.SeedRoleSpec(physicalRoleName, &awslambda.RolePlan{}),
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
}

// queueLinkSelectorWithForwards returns the source queue's link selector augmented
// with the synthetic forward label when the queue forwards to any topic. Bluelink
// label matching is per-label (each label forms an independent selector group), so
// adding the forward label creates a match to the forwarder function without
// disturbing the existing topic matches (which are inert — aws/sqs/queue declares
// no link to aws/sns/topic). Returns the selector unchanged when there are no
// forwards.
func queueLinkSelectorWithForwards(r *ResolvedQueue) *schema.LinkSelector {
	if len(r.TopicForwards) == 0 {
		return r.Resource.LinkSelector
	}
	labels := map[string]string{}
	if r.Resource.LinkSelector != nil && r.Resource.LinkSelector.ByLabel != nil {
		maps.Copy(labels, r.Resource.LinkSelector.ByLabel.Values)
	}
	labels[forwardLabelKey(r.Name)] = "true"
	return &schema.LinkSelector{ByLabel: &schema.StringMap{Values: labels}}
}

// forwardSelectorLabels returns the union of every forwarding edge's matched topic
// labels — the publish selector for the single forwarder. Empty when no edge
// carries labels (a latent mis-wire the emit guards against).
func forwardSelectorLabels(edges []*TopicForwardEdge) map[string]string {
	labels := map[string]string{}
	for _, edge := range edges {
		maps.Copy(labels, edge.SelectorLabels)
	}
	return labels
}
