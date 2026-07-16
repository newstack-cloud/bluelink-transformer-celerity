package handler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// annVPCSubnetType is the aws/flex/vpc::aws/lambda/function link annotation
	// (AppliesTo resourceB, the function) selecting which subnet tier the function
	// is placed in.
	annVPCSubnetType = "aws.flexvpc.lambda.subnetType"

	// SQS event-source-mapping link annotations (AppliesTo the function).
	annSQSBatchSize            = "aws.sqs.lambda.batchSize"
	annSQSReportBatchItemFails = "aws.sqs.lambda.reportBatchItemFailures"

	// DynamoDB stream event-source-mapping link annotations (AppliesTo the function).
	annDDBBatchSize            = "aws.dynamodb.lambda.stream.batchSize"
	annDDBStartingPosition     = "aws.dynamodb.lambda.stream.startingPosition"
	annDDBReportBatchItemFails = "aws.dynamodb.lambda.stream.reportBatchItemFailures"

	// S3 object-event notification link annotation (AppliesTo the function).
	annS3Event = "aws.s3.lambda.event.%d"

	// redriveMaxReceiveCountAnnotation is the aws/sqs/queue::aws/sqs/queue link
	// annotation (set on the source queue) that maps to the redrive policy's
	// maxReceiveCount, reused here for a topic fan-out queue's dead-letter queue.
	redriveMaxReceiveCountAnnotation = "aws.sqs.redrive.maxReceiveCount"
)

const (
	streamStartTrimHorizon = "TRIM_HORIZON"
	streamStartLatest      = "LATEST"
)

// reportBatchItemFailures is the event source mapping function response type that
// enables partial batch failure reporting.
const reportBatchItemFailures = "ReportBatchItemFailures"

// Consumer annotation keys read from the abstract consumer.
const (
	annConsumerDatastoreStartFromBeginning = "celerity.consumer.datastore.startFromBeginning"
	annConsumerBucketEvents                = "celerity.consumer.bucket.events"

	// annConsumerDeadLetterQueue toggles creation of a dead-letter queue for a
	// Celerity-topic consumer (default true).
	annConsumerDeadLetterQueue = "celerity.consumer.deadLetterQueue"
	// annConsumerDeadLetterQueueMaxAttempts sets the DLQ redrive maxReceiveCount.
	annConsumerDeadLetterQueueMaxAttempts = "celerity.consumer.deadLetterQueueMaxAttempts"

	// annConsumerTopicPollPrefix is a synthetic per-consumer label key (value
	// "true") stamped on the function and the topic fan-out queue's linkSelector so
	// the aws/sqs/queue::aws/lambda/function event-source-mapping link (and the
	// queue's redrive link to its DLQ) activate by label-on-source. It is namespaced
	// by consumer name so a handler absorbing several topic consumers keeps one
	// distinct wiring label per consumer.
	annConsumerTopicPollPrefix = "celerity.consumer.topicPoll."
)

// applyLambdaLabels sets the emitted function's labels to the union of the
// handler's own labels and every absorbed consumer's labels (handler wins on key
// conflicts). This lets an inbound source/api/subscription link that selected the
// consumer by label now resolve against the concrete function.
func applyLambdaLabels(r *ResolvedHandler, lambda *schema.Resource) {
	labels := consumerLabelUnion(r)
	if r.Resource.Metadata != nil && r.Resource.Metadata.Labels != nil {
		for key, value := range r.Resource.Metadata.Labels.Values {
			labels[key] = value
		}
	}
	if len(labels) == 0 {
		return
	}
	lambda.Metadata.Labels = &schema.StringMap{Values: labels}
}

// stampTriggerAnnotations layers the VPC-placement and consumer event-source link
// annotations onto the emitted function's metadata. It returns diagnostics for any
// consumer source that could not be fully wired on aws-serverless.
func stampTriggerAnnotations(r *ResolvedHandler, lambda *schema.Resource) []*core.Diagnostic {
	if r.VPC != nil {
		setStringAnnotation(lambda.Metadata, annVPCSubnetType, r.VPCSubnetType)
	}

	diagnostics := []*core.Diagnostic{}
	for _, binding := range r.ConsumerBindings {
		diagnostics = append(diagnostics, stampConsumerBinding(lambda, binding)...)
		if diag := routingKeyDeferred(r, binding); diag != nil {
			diagnostics = append(diagnostics, diag)
		}
	}
	return diagnostics
}

// routingKeyDeferred warns when a consumer sets a non-default routingKey while its
// handler activates routing (the celerity.handler.consumer.route annotation). The
// route tag reaches the runtime via the CELERITY_HANDLER_TAG environment variable,
// but the routing key (the payload field to route on) is not propagated to the
// function, so a non-default value would be silently ignored at runtime.
func routingKeyDeferred(r *ResolvedHandler, binding *ConsumerBinding) *core.Diagnostic {
	if !r.HasRoutingTag {
		return nil
	}
	node, ok := pluginutils.GetValueByPath("$.routingKey", consumerSpec(binding))
	if !ok || node == nil {
		return nil
	}
	key := core.StringValue(node)
	if key == "" || key == "event" {
		return nil
	}
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/consumer %q sets routingKey %q while handler %q activates routing, but the "+
				"routing key is not propagated to the aws-serverless function; the runtime routes on the "+
				"default \"event\" field. Rename the payload field to \"event\" or route using the default.",
			binding.ConsumerName,
			key,
			r.Name,
		),
	}
}

func stampConsumerBinding(lambda *schema.Resource, binding *ConsumerBinding) []*core.Diagnostic {
	diagnostics := []*core.Diagnostic{}
	if binding.Ambiguous {
		diagnostics = append(diagnostics, ambiguousSourceWarning(binding))
	}

	switch binding.SourceKind {
	case ConsumerSourceQueue:
		stampQueueBinding(lambda, binding)
	case ConsumerSourceDatastore:
		stampDatastoreBinding(lambda, binding)
	case ConsumerSourceBucket:
		diagnostics = append(diagnostics, stampBucketBinding(lambda, binding)...)
	case ConsumerSourceTopic:
		stampTopicBinding(lambda, binding)
	case ConsumerSourceExternal:
		diagnostics = append(diagnostics, externalObjectStorageDeferred(binding)...)
	case ConsumerSourceUnknown:
		diagnostics = append(diagnostics, unknownSourceDeferred(binding))
	}
	return diagnostics
}

func stampQueueBinding(lambda *schema.Resource, binding *ConsumerBinding) {
	spec := consumerSpec(binding)
	if node, ok := pluginutils.GetValueByPath("$.batchSize", spec); ok {
		setIntAnnotation(lambda.Metadata, annSQSBatchSize, core.IntValue(node))
	}
	if partialFailures(spec) {
		setBoolAnnotation(lambda.Metadata, annSQSReportBatchItemFails, true)
	}
}

func stampDatastoreBinding(lambda *schema.Resource, binding *ConsumerBinding) {
	// startingPosition is required by the provider link; derive it from the
	// consumer's celerity.consumer.datastore.startFromBeginning annotation.
	setStringAnnotation(lambda.Metadata, annDDBStartingPosition, startingPosition(binding))

	spec := consumerSpec(binding)
	if node, ok := pluginutils.GetValueByPath("$.batchSize", spec); ok {
		setIntAnnotation(lambda.Metadata, annDDBBatchSize, core.IntValue(node))
	}
	if partialFailures(spec) {
		setBoolAnnotation(lambda.Metadata, annDDBReportBatchItemFails, true)
	}
}

func stampBucketBinding(lambda *schema.Resource, binding *ConsumerBinding) []*core.Diagnostic {
	events, unsupported := bucketEvents(binding)
	for index, event := range events {
		setStringAnnotation(lambda.Metadata, fmt.Sprintf(annS3Event, index), event)
	}

	diagnostics := []*core.Diagnostic{}
	for _, raw := range unsupported {
		diagnostics = append(diagnostics, bucketEventUnsupported(binding, raw))
	}
	return diagnostics
}

// stampTopicBinding stamps the SQS event-source-mapping annotations (read by the
// aws/sqs/queue::aws/lambda/function link on the topic fan-out queue) and the
// synthetic poll label that makes the queue's linkSelector resolve to the function.
func stampTopicBinding(lambda *schema.Resource, binding *ConsumerBinding) {
	spec := consumerSpec(binding)
	if node, ok := pluginutils.GetValueByPath("$.batchSize", spec); ok {
		setIntAnnotation(lambda.Metadata, annSQSBatchSize, core.IntValue(node))
	}
	if partialFailures(spec) {
		setBoolAnnotation(lambda.Metadata, annSQSReportBatchItemFails, true)
	}
	setLabel(lambda.Metadata, topicPollLabelKey(binding.ConsumerName), "true")
}

func startingPosition(binding *ConsumerBinding) string {
	value, ok := transformutils.GetAnnotation(
		binding.ConsumerResource,
		annConsumerDatastoreStartFromBeginning,
		"",
	)
	if ok && core.BoolValue(value) {
		return streamStartTrimHorizon
	}
	return streamStartLatest
}

// bucketEvents maps the abstract celerity.consumer.bucket.events annotation
// (comma-separated created|deleted|metadataUpdated) to concrete S3 event strings.
// It returns the mapped events and any consumer events with no S3 equivalent
// (metadataUpdated) so the caller can warn rather than drop them silently.
func bucketEvents(binding *ConsumerBinding) (mapped []string, unsupported []string) {
	value, ok := transformutils.GetAnnotation(
		binding.ConsumerResource,
		annConsumerBucketEvents,
		"",
	)
	if !ok {
		return nil, nil
	}

	for _, raw := range splitAndTrim(core.StringValue(value)) {
		if event, mappable := s3EventForConsumerEvent(raw); mappable {
			mapped = append(mapped, event)
		} else {
			unsupported = append(unsupported, raw)
		}
	}
	return mapped, unsupported
}

func splitAndTrim(value string) []string {
	parts := []string{}
	for _, raw := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

func s3EventForConsumerEvent(consumerEvent string) (string, bool) {
	switch consumerEvent {
	case "created":
		return "s3:ObjectCreated:*", true
	case "deleted":
		return "s3:ObjectRemoved:*", true
	default:
		return "", false
	}
}

// emitScheduleRules builds one aws/events/rule per absorbed schedule, targeting the
// emitted function. Setting targets[].arn to the function reference activates the
// provider's aws/events/rule::aws/lambda/function link (which creates the invoke
// permission), so no permission is emitted here.
func emitScheduleRules(
	r *ResolvedHandler,
	funcResourceName string,
) (map[string]*schema.Resource, error) {
	rules := map[string]*schema.Resource{}
	for _, schedule := range r.Schedules {
		rule, err := scheduleRule(schedule.Name, schedule.Resource, funcResourceName)
		if err != nil {
			return nil, err
		}
		rules[scheduleRuleResourceName(schedule.Name)] = rule
	}
	return rules, nil
}

func scheduleRule(
	scheduleName string,
	scheduleResource *schema.Resource,
	funcResourceName string,
) (*schema.Resource, error) {
	arnRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", funcResourceName),
	)
	if err != nil {
		return nil, err
	}

	target := core.MappingNodeFields(
		"id", core.MappingNodeFromString(funcResourceName),
		"arn", arnRef,
	)
	if input, ok := scheduleInput(scheduleResource); ok {
		target.Fields["input"] = core.MappingNodeFromString(input)
	}

	spec := core.MappingNodeFields(
		"scheduleExpression", scheduleExpression(scheduleResource),
		"targets", core.MappingNodeItems(target),
	)

	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/events/rule"},
		Spec: spec,
		Metadata: &schema.Metadata{
			Annotations: transformutils.TransformerBaseAnnotations(
				&transformutils.TransformerBaseAnnotationsInput{
					AbstractResourceName: scheduleName,
					AbstractResourceType: "celerity/schedule",
					ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
				},
			),
		},
	}, nil
}

func scheduleExpression(scheduleResource *schema.Resource) *core.MappingNode {
	if scheduleResource == nil {
		return core.MappingNodeFromString("")
	}
	node, _ := pluginutils.GetValueByPath("$.schedule", scheduleResource.Spec)
	return core.MappingNodeFromString(core.StringValue(node))
}

// scheduleInput returns the schedule's spec.input JSON-encoded as a string, as
// required by the aws/events/rule target input field.
func scheduleInput(scheduleResource *schema.Resource) (string, bool) {
	if scheduleResource == nil {
		return "", false
	}
	node, ok := pluginutils.GetValueByPath("$.input", scheduleResource.Spec)
	if !ok || node == nil {
		return "", false
	}
	encoded, err := json.Marshal(node)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// emitConsumerSubscriptions builds the concrete event-source resources that an
// absorbed consumer needs beyond the function-side annotations: an SQS fan-out
// queue + SNS subscription (+ optional dead-letter queue) for a Celerity-topic
// source, and a standalone aws/lambda/eventSourceMapping for each external stream
// or external SQS source.
func emitConsumerSubscriptions(
	r *ResolvedHandler,
	funcResourceName string,
) (map[string]*schema.Resource, error) {
	resources := map[string]*schema.Resource{}
	for _, binding := range r.ConsumerBindings {
		switch binding.SourceKind {
		case ConsumerSourceTopic:
			if err := emitTopicFanout(resources, binding, funcResourceName); err != nil {
				return nil, err
			}
		case ConsumerSourceExternal:
			if err := emitExternalSources(resources, binding, funcResourceName); err != nil {
				return nil, err
			}
		}
	}
	return resources, nil
}

// emitTopicFanout wires the SNS -> SQS -> Lambda fan-out for a Celerity-topic
// consumer: an intermediary SQS queue subscribed to the topic (giving a reliable,
// scalable fan-out and the subscriberId output), an SNS subscription (sqs protocol)
// and, unless disabled, a dead-letter queue with a redrive policy.
func emitTopicFanout(
	resources map[string]*schema.Resource,
	binding *ConsumerBinding,
	funcResourceName string,
) error {
	queueName := topicQueueResourceName(binding.ConsumerName)

	resources[queueName] = topicFanoutQueue(binding)

	subscription, err := topicSnsSubscription(binding, queueName)
	if err != nil {
		return err
	}
	resources[snsSubscriptionResourceName(binding.ConsumerName)] = subscription

	if deadLetterQueueEnabled(binding) {
		resources[topicDLQResourceName(binding.ConsumerName)] = topicDeadLetterQueue(binding)
	}

	return nil
}

// topicFanoutQueue is the intermediary SQS queue subscribed to the topic. Its
// linkSelector carries the synthetic poll label so the aws/sqs/queue::aws/lambda/
// function link creates the event source mapping to the function; the same label
// (plus a redrive annotation) links it to its dead-letter queue.
func topicFanoutQueue(binding *ConsumerBinding) *schema.Resource {
	meta := &schema.Metadata{
		Annotations: consumerBaseAnnotations(binding.ConsumerName),
	}
	if attempts, ok := transformutils.GetAnnotation(
		binding.ConsumerResource,
		annConsumerDeadLetterQueueMaxAttempts,
		"",
	); ok && deadLetterQueueEnabled(binding) {
		setStringAnnotation(meta, redriveMaxReceiveCountAnnotation, core.StringValue(attempts))
	}

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/sqs/queue"},
		Spec:     &core.MappingNode{Fields: map[string]*core.MappingNode{}},
		Metadata: meta,
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{
				Values: map[string]string{
					topicPollLabelKey(binding.ConsumerName): "true",
				},
			},
		},
	}
}

func topicDeadLetterQueue(binding *ConsumerBinding) *schema.Resource {
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/sqs/queue"},
		Spec: &core.MappingNode{Fields: map[string]*core.MappingNode{}},
		Metadata: &schema.Metadata{
			Annotations: consumerBaseAnnotations(binding.ConsumerName),
			// The fan-out queue's linkSelector selects this queue by the poll label,
			// activating the aws/sqs/queue::aws/sqs/queue redrive link.
			Labels: &schema.StringMap{
				Values: map[string]string{
					topicPollLabelKey(binding.ConsumerName): "true",
				},
			},
		},
	}
}

func topicSnsSubscription(
	binding *ConsumerBinding,
	queueResourceName string,
) (*schema.Resource, error) {
	endpointRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", queueResourceName),
	)
	if err != nil {
		return nil, err
	}

	spec := core.MappingNodeFields(
		"protocol", core.MappingNodeFromString("sqs"),
		"topicArn", binding.TopicARN,
		"endpoint", endpointRef,
	)

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/sns/subscription"},
		Spec:     spec,
		Metadata: &schema.Metadata{Annotations: consumerBaseAnnotations(binding.ConsumerName)},
	}, nil
}

// emitExternalSources emits a standalone aws/lambda/eventSourceMapping for each
// external stream (dbStream/dataStream) and for a raw external SQS sourceId. The
// out-of-blueprint object storage sources are surfaced as warnings on the function
// side (see externalObjectStorageDeferred).
func emitExternalSources(
	resources map[string]*schema.Resource,
	binding *ConsumerBinding,
	funcResourceName string,
) error {
	for _, stream := range binding.ExternalStreams {
		esm, err := streamEventSourceMapping(binding, stream, funcResourceName)
		if err != nil {
			return err
		}
		resources[externalStreamESMResourceName(binding.ConsumerName, stream.Key)] = esm
	}

	if binding.ExternalSQSArn != nil {
		esm, err := externalSQSEventSourceMapping(binding, funcResourceName)
		if err != nil {
			return err
		}
		resources[externalSQSESMResourceName(binding.ConsumerName)] = esm
	}

	return nil
}

func streamEventSourceMapping(
	binding *ConsumerBinding,
	stream *ExternalStreamBinding,
	funcResourceName string,
) (*schema.Resource, error) {
	spec, err := eventSourceMappingSpec(stream.EventSourceArn, funcResourceName)
	if err != nil {
		return nil, err
	}
	if stream.BatchSize != nil {
		spec.Fields["batchSize"] = stream.BatchSize
	}
	if stream.PartialFailures {
		spec.Fields["functionResponseTypes"] = core.MappingNodeItems(
			core.MappingNodeFromString(reportBatchItemFailures),
		)
	}
	spec.Fields["startingPosition"] = core.MappingNodeFromString(
		streamStartingPosition(stream.StartFromBeginning),
	)

	return eventSourceMappingResource(binding.ConsumerName, spec), nil
}

func externalSQSEventSourceMapping(
	binding *ConsumerBinding,
	funcResourceName string,
) (*schema.Resource, error) {
	spec, err := eventSourceMappingSpec(binding.ExternalSQSArn, funcResourceName)
	if err != nil {
		return nil, err
	}

	consumerSpec := consumerSpec(binding)
	if node, ok := pluginutils.GetValueByPath("$.batchSize", consumerSpec); ok {
		spec.Fields["batchSize"] = node
	}
	if partialFailures(consumerSpec) {
		spec.Fields["functionResponseTypes"] = core.MappingNodeItems(
			core.MappingNodeFromString(reportBatchItemFailures),
		)
	}
	// startingPosition is stream-only and is intentionally omitted for SQS sources.

	return eventSourceMappingResource(binding.ConsumerName, spec), nil
}

func eventSourceMappingSpec(
	eventSourceArn *core.MappingNode,
	funcResourceName string,
) (*core.MappingNode, error) {
	// The provider aws/lambda/eventSourceMapping.functionName accepts a name or ARN;
	// the function's spec.functionName is used to imply a dependency on the function.
	funcNameRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.functionName}", funcResourceName),
	)
	if err != nil {
		return nil, err
	}

	return core.MappingNodeFields(
		"eventSourceArn", eventSourceArn,
		"functionName", funcNameRef,
	), nil
}

func eventSourceMappingResource(consumerName string, spec *core.MappingNode) *schema.Resource {
	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/lambda/eventSourceMapping"},
		Spec:     spec,
		Metadata: &schema.Metadata{Annotations: consumerBaseAnnotations(consumerName)},
	}
}

func streamStartingPosition(startFromBeginning bool) string {
	if startFromBeginning {
		return streamStartTrimHorizon
	}
	return streamStartLatest
}

func consumerBaseAnnotations(consumerName string) *schema.StringOrSubstitutionsMap {
	return transformutils.TransformerBaseAnnotations(
		&transformutils.TransformerBaseAnnotationsInput{
			AbstractResourceName: consumerName,
			AbstractResourceType: "celerity/consumer",
			ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
		},
	)
}

func deadLetterQueueEnabled(binding *ConsumerBinding) bool {
	value, ok := transformutils.GetAnnotation(
		binding.ConsumerResource,
		annConsumerDeadLetterQueue,
		"",
	)
	if !ok {
		return true // default true per the celerity/consumer spec.
	}
	return core.BoolValue(value)
}

func topicPollLabelKey(consumerName string) string {
	return annConsumerTopicPollPrefix + consumerName
}

func consumerSpec(binding *ConsumerBinding) *core.MappingNode {
	if binding.ConsumerResource == nil {
		return nil
	}
	return binding.ConsumerResource.Spec
}

func partialFailures(spec *core.MappingNode) bool {
	node, ok := pluginutils.GetValueByPath("$.partialFailures", spec)
	if !ok {
		return false
	}
	return core.BoolValue(node)
}

func ambiguousSourceWarning(binding *ConsumerBinding) *core.Diagnostic {
	annKey, _ := disambiguationAnnotationKey(binding.SourceKind)
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/consumer %q matches multiple %s sources by label; none was named via the %q "+
				"annotation, so %q was selected deterministically. Set %q to the intended source name "+
				"to make the binding explicit.",
			binding.ConsumerName,
			binding.SourceKind,
			annKey,
			binding.SourceAbstractName,
			annKey,
		),
	}
}

func bucketEventUnsupported(binding *ConsumerBinding, event string) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/consumer %q requests the %q bucket event, which has no Amazon S3 notification "+
				"equivalent (S3 emits no metadata-updated event); it has been skipped",
			binding.ConsumerName,
			event,
		),
	}
}

func externalObjectStorageDeferred(binding *ConsumerBinding) []*core.Diagnostic {
	diagnostics := []*core.Diagnostic{}
	for _, bucket := range binding.DeferredObjectStores {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/consumer %q uses an externalEvents objectStorage source for bucket %q; an S3 "+
					"event notification must be configured on the bucket itself, but the bucket is defined "+
					"outside this blueprint so the transformer cannot modify it. The event source has been "+
					"skipped; configure the S3 notification on the bucket in the blueprint that owns it, or "+
					"route events through a queue/topic consumer instead.",
				binding.ConsumerName,
				bucket,
			),
		})
	}
	return diagnostics
}

func unknownSourceDeferred(binding *ConsumerBinding) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/consumer %q could not be classified to a concrete event source (no linked "+
				"queue/datastore/bucket and no recognised sourceId or externalEvents); its trigger has "+
				"been skipped",
			binding.ConsumerName,
		),
	}
}

func setStringAnnotation(meta *schema.Metadata, key, value string) {
	meta.Annotations.Values[key] = pluginutils.StringToSubstitutions(value)
}

func setIntAnnotation(meta *schema.Metadata, key string, value int) {
	setStringAnnotation(meta, key, strconv.Itoa(value))
}

func setBoolAnnotation(meta *schema.Metadata, key string, value bool) {
	setStringAnnotation(meta, key, strconv.FormatBool(value))
}

func setLabel(meta *schema.Metadata, key, value string) {
	if meta.Labels == nil {
		meta.Labels = &schema.StringMap{Values: map[string]string{}}
	}
	if meta.Labels.Values == nil {
		meta.Labels.Values = map[string]string{}
	}
	meta.Labels.Values[key] = value
}

func scheduleRuleResourceName(scheduleName string) string {
	return fmt.Sprintf("%s_events_rule", scheduleName)
}

func snsSubscriptionResourceName(consumerName string) string {
	return fmt.Sprintf("%s_sns_subscription", consumerName)
}

func topicQueueResourceName(consumerName string) string {
	return fmt.Sprintf("%s_topic_queue", consumerName)
}

func topicDLQResourceName(consumerName string) string {
	return fmt.Sprintf("%s_topic_dlq", consumerName)
}

func externalStreamESMResourceName(consumerName, key string) string {
	return fmt.Sprintf("%s_%s_esm", consumerName, key)
}

func externalSQSESMResourceName(consumerName string) string {
	return fmt.Sprintf("%s_sqs_esm", consumerName)
}

// consumerSubscriberRewriters returns one rewriter per absorbed topic-source
// consumer that resolves ${<consumer>.spec.subscriberId} to the concrete SQS
// fan-out queue created for that consumer (<consumer>_topic_queue). Per the
// celerity/consumer schema, subscriberId is the ID of the subscription created
// when a queue is used to subscribe to a topic; on aws-serverless that subscriber
// is the intermediary fan-out queue, identified by its ARN (matching how
// celerity/queue.spec.id resolves to the concrete queue ARN).
func consumerSubscriberRewriters(r *ResolvedHandler) []transformutils.ResourcePropertyRewriter {
	rewriters := []transformutils.ResourcePropertyRewriter{}
	for _, binding := range r.ConsumerBindings {
		if binding.SourceKind != ConsumerSourceTopic {
			continue
		}
		rewriters = append(rewriters, subscriberIDRewriter(binding.ConsumerName))
	}
	return rewriters
}

func subscriberIDRewriter(consumerName string) transformutils.ResourcePropertyRewriter {
	queueName := topicQueueResourceName(consumerName)
	return func(ref *substitutions.SubstitutionResourceProperty) *substitutions.Substitution {
		if ref == nil || ref.ResourceName != consumerName {
			return nil
		}
		if !transformutils.PathExact(ref, "spec", "subscriberId") {
			return nil
		}
		return transformutils.RewriteFields(ref, queueName, "spec", "arn")
	}
}

// externalRoleSources collects the out-of-blueprint event sources every absorbed
// consumer polls via a standalone event source mapping. These have no provider
// link to inject source-read IAM, so the transformer seeds the statements onto the
// handler's execution role (see awslambda.SeedRoleSpec). Returning them here also
// folds them into the role fingerprint, so a handler with external sources never
// shares a role with one that lacks them.
func externalRoleSources(r *ResolvedHandler) []awslambda.ExternalEventSource {
	sources := []awslambda.ExternalEventSource{}
	for _, binding := range r.ConsumerBindings {
		for _, stream := range binding.ExternalStreams {
			service := streamRoleService(stream.SourceType)
			if service == "" {
				continue
			}
			if arn := arnString(stream.EventSourceArn); arn != "" {
				sources = append(sources, awslambda.ExternalEventSource{Service: service, ARN: arn})
			}
		}
		if binding.ExternalSQSArn != nil {
			if arn := externalSQSResourceARN(binding.ExternalSQSArn); arn != "" {
				sources = append(sources, awslambda.ExternalEventSource{
					Service: awslambda.ExternalSourceServiceSQS,
					ARN:     arn,
				})
			}
		}
	}

	// Deterministic ordering so the fingerprint and the seed are stable.
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Service != sources[j].Service {
			return sources[i].Service < sources[j].Service
		}
		return sources[i].ARN < sources[j].ARN
	})
	return sources
}

func streamRoleService(sourceType string) string {
	switch sourceType {
	case "dbStream":
		return awslambda.ExternalSourceServiceDynamoDBStream
	case "dataStream":
		return awslambda.ExternalSourceServiceKinesisStream
	default:
		return ""
	}
}

// arnString renders an ARN MappingNode (a literal scalar or a ${...} substitution)
// to its canonical string form, used both to fingerprint the role plan and to
// embed the ARN in the seeded policy statement.
func arnString(node *core.MappingNode) string {
	if node == nil {
		return ""
	}
	if node.StringWithSubstitutions != nil {
		if value, err := substitutions.SubstitutionsToString("", node.StringWithSubstitutions); err == nil {
			return value
		}
	}
	return core.StringValue(node)
}

// externalSQSResourceARN renders an external SQS sourceId to an ARN suitable for
// scoping an IAM statement. A literal queue URL
// (https://sqs.<region>.amazonaws.com/<account>/<name>) is converted to its
// arn:aws:sqs:<region>:<account>:<name> form; a literal ARN or a substitution is
// used verbatim.
func externalSQSResourceARN(node *core.MappingNode) string {
	value := arnString(node)
	if arn, ok := sqsURLToARN(value); ok {
		return arn
	}
	return value
}

func sqsURLToARN(url string) (string, bool) {
	const prefix = "https://sqs."
	if !strings.HasPrefix(url, prefix) {
		return "", false
	}
	// <region>.amazonaws.com/<account>/<name>
	rest := strings.TrimPrefix(url, prefix)
	host, path, ok := strings.Cut(rest, "/")
	if !ok {
		return "", false
	}
	region, _, ok := strings.Cut(host, ".")
	if !ok || region == "" {
		return "", false
	}
	account, name, ok := strings.Cut(path, "/")
	if !ok || account == "" || name == "" {
		return "", false
	}
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", region, account, name), true
}
