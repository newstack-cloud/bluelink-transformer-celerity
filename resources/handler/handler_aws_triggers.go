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

// Sets the emitted function's labels to the union of the handler's own labels and
// every absorbed consumer's labels (handler wins on key conflicts). This lets an
// inbound source/api/subscription link that selected the consumer by label now
// resolve against the concrete function.
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

// Layers the VPC-placement and consumer event-source link annotations onto the
// emitted function's metadata. Returns diagnostics for any consumer source that
// could not be fully wired on aws-serverless.
func stampTriggerAnnotations(r *ResolvedHandler, lambda *schema.Resource) []*core.Diagnostic {
	if r.VPC != nil {
		setStringAnnotation(lambda.Metadata, annVPCSubnetType, r.VPCSubnetType)
	}

	diagnostics := []*core.Diagnostic{}
	// The aws.s3.lambda.event.<index> annotations form a single flat list read
	// off the function, so the index must run across all bucket bindings; a
	// per-binding reset would overwrite earlier bindings' events.
	s3EventBase := 0
	for _, binding := range r.ConsumerBindings {
		stamped, diags := stampConsumerBinding(lambda, binding, s3EventBase)
		diagnostics = append(diagnostics, diags...)
		s3EventBase += stamped
		if diag := routingKeyDeferred(r, binding); diag != nil {
			diagnostics = append(diagnostics, diag)
		}
	}
	diagnostics = append(diagnostics, perFunctionTriggerConflicts(r)...)
	return diagnostics
}

// perFunctionTriggerConflicts warns when a handler absorbs several consumers of
// the same event-source family whose settings diverge. On aws-serverless the
// SQS/DynamoDB batch settings and the S3 notification event list are single
// per-function values applied to every source of that kind, so divergent
// per-consumer settings cannot all take effect. The common one-consumer-per-
// handler case, and same-kind consumers that share settings, never trigger it.
func perFunctionTriggerConflicts(r *ResolvedHandler) []*core.Diagnostic {
	var sqs, ddb, bucket []*ConsumerBinding
	for _, binding := range r.ConsumerBindings {
		switch binding.SourceKind {
		case ConsumerSourceQueue, ConsumerSourceTopic:
			sqs = append(sqs, binding)
		case ConsumerSourceDatastore:
			ddb = append(ddb, binding)
		case ConsumerSourceBucket:
			bucket = append(bucket, binding)
		}
	}

	var diagnostics []*core.Diagnostic
	if batchOrFailuresConflict(sqs) {
		diagnostics = append(diagnostics, sharedSettingWarning(
			r.Name, sqs, "queue/topic",
			"SQS event-source-mapping settings (batchSize, reportBatchItemFailures)",
		))
	}
	if batchOrFailuresConflict(ddb) || startingPositionConflict(ddb) {
		diagnostics = append(diagnostics, sharedSettingWarning(
			r.Name, ddb, "datastore",
			"DynamoDB stream settings (batchSize, reportBatchItemFailures, startingPosition)",
		))
	}
	if bucketEventSetsDiverge(bucket) {
		diagnostics = append(diagnostics, bucketEventUnionWarning(r.Name, bucket))
	}
	return diagnostics
}

// batchOrFailuresConflict reports whether the bindings set two or more distinct
// batchSize values, or disagree on partialFailures.
func batchOrFailuresConflict(bindings []*ConsumerBinding) bool {
	if len(bindings) < 2 {
		return false
	}
	batchSizes := map[int]bool{}
	var seenPF, seenNoPF bool
	for _, binding := range bindings {
		spec := consumerSpec(binding)
		if node, ok := pluginutils.GetValueByPath("$.batchSize", spec); ok {
			batchSizes[core.IntValue(node)] = true
		}
		if partialFailures(spec) {
			seenPF = true
		} else {
			seenNoPF = true
		}
	}
	return len(batchSizes) >= 2 || (seenPF && seenNoPF)
}

func startingPositionConflict(bindings []*ConsumerBinding) bool {
	if len(bindings) < 2 {
		return false
	}
	positions := map[string]bool{}
	for _, binding := range bindings {
		positions[startingPosition(binding)] = true
	}
	return len(positions) >= 2
}

func bucketEventSetsDiverge(bindings []*ConsumerBinding) bool {
	if len(bindings) < 2 {
		return false
	}
	sets := map[string]bool{}
	for _, binding := range bindings {
		events, _ := bucketEvents(binding)
		sort.Strings(events)
		sets[strings.Join(events, ",")] = true
	}
	return len(sets) >= 2
}

func sharedSettingWarning(
	handlerName string,
	bindings []*ConsumerBinding,
	kindLabel string,
	settingLabel string,
) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/handler %q absorbs multiple %s consumers (%s) with differing settings, but on "+
				"aws-serverless the %s are a single per-function value applied to every source of this "+
				"kind, so only one consumer's settings take effect. Split the consumers across separate "+
				"handlers to configure them independently.",
			handlerName, kindLabel, consumerNameList(bindings), settingLabel,
		),
	}
}

func bucketEventUnionWarning(handlerName string, bindings []*ConsumerBinding) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/handler %q absorbs multiple bucket consumers (%s) requesting different event sets, "+
				"but on aws-serverless the S3 notification events are combined into one list applied to "+
				"every linked bucket, so each bucket triggers on the union rather than only its own events. "+
				"Split the consumers across separate handlers for independent event sets.",
			handlerName, consumerNameList(bindings),
		),
	}
}

func consumerNameList(bindings []*ConsumerBinding) string {
	names := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		names = append(names, binding.ConsumerName)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Warns when a consumer sets a non-default routingKey while its handler activates
// routing (the celerity.handler.consumer.route annotation). The route tag reaches
// the runtime via the CELERITY_HANDLER_TAG environment variable, but the routing
// key (the payload field to route on) is not propagated to the function, so a
// non-default value would be silently ignored at runtime.
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

// stampConsumerBinding stamps one binding's trigger annotations and returns the
// number of S3 event annotations it stamped (0 for non-bucket sources) so the
// caller can keep the aws.s3.lambda.event.<index> list unique across bindings.
func stampConsumerBinding(
	lambda *schema.Resource,
	binding *ConsumerBinding,
	s3EventBase int,
) (int, []*core.Diagnostic) {
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
		stamped, diags := stampBucketBinding(lambda, binding, s3EventBase)
		return stamped, append(diagnostics, diags...)
	case ConsumerSourceTopic:
		stampTopicBinding(lambda, binding)
	case ConsumerSourceExternal:
		diagnostics = append(diagnostics, externalObjectStorageDeferred(binding)...)
	case ConsumerSourceUnknown:
		diagnostics = append(diagnostics, unknownSourceDeferred(binding))
	}
	return 0, diagnostics
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

// stampBucketBinding stamps the S3 notification event annotations starting at
// baseIndex (so successive bucket bindings occupy distinct indices in the
// function's flat event list) and returns how many it stamped.
func stampBucketBinding(lambda *schema.Resource, binding *ConsumerBinding, baseIndex int) (int, []*core.Diagnostic) {
	events, unsupported := bucketEvents(binding)
	for offset, event := range events {
		setStringAnnotation(lambda.Metadata, fmt.Sprintf(annS3Event, baseIndex+offset), event)
	}

	diagnostics := []*core.Diagnostic{}
	for _, raw := range unsupported {
		diagnostics = append(diagnostics, bucketEventUnsupported(binding, raw))
	}
	return len(events), diagnostics
}

// Stamps the SQS event-source-mapping annotations (read by the
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

// Maps the abstract celerity.consumer.bucket.events annotation (comma-separated
// created|deleted|metadataUpdated) to concrete S3 event strings. Returns the
// mapped events and any consumer events with no S3 equivalent (metadataUpdated) so
// the caller can warn rather than drop them silently.
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

// Builds one aws/events/rule per absorbed schedule, targeting the emitted function.
// Setting targets[].arn to the function reference activates the provider's
// aws/events/rule::aws/lambda/function link (which creates the invoke permission),
// so no permission is emitted here.
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

// Returns the schedule's spec.input JSON-encoded as a string, as required by the
// aws/events/rule target input field.
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

// Builds the concrete event-source resources that an absorbed consumer needs
// beyond the function-side annotations: an SQS fan-out queue + SNS subscription
// (+ optional dead-letter queue) for a Celerity-topic source, and a standalone
// aws/lambda/eventSourceMapping for each external stream or external SQS source.
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

// Wires the SNS -> SQS -> Lambda fan-out for a Celerity-topic consumer: an
// intermediary SQS queue subscribed to the topic (giving a reliable, scalable
// fan-out and the subscriberId output), an SNS subscription (sqs protocol) and,
// unless disabled, a dead-letter queue with a redrive policy.
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

// The intermediary SQS queue subscribed to the topic. Its linkSelector carries the
// synthetic poll label so the aws/sqs/queue::aws/lambda/function link creates the
// event source mapping to the function; the same label (plus a redrive annotation)
// links it to its dead-letter queue.
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

// Emits a standalone aws/lambda/eventSourceMapping for each external stream
// (dbStream/dataStream) and for a raw external SQS sourceId. The out-of-blueprint
// object storage sources are surfaced as warnings on the function side (see
// externalObjectStorageDeferred).
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
		return true // default true when the annotation is absent.
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

// Returns one rewriter per absorbed topic-source consumer that resolves
// ${<consumer>.spec.subscriberId} to the concrete SQS fan-out queue created for
// that consumer (<consumer>_topic_queue). subscriberId is the ID of the
// subscription created when a queue subscribes to a topic; on aws-serverless that
// subscriber is the intermediary fan-out queue, identified by its ARN (matching how
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

// Collects the out-of-blueprint event sources every absorbed consumer polls via a
// standalone event source mapping. These have no provider link to inject
// source-read IAM, so the transformer seeds the statements onto the handler's
// execution role (see awslambda.SeedRoleSpec). Returning them here also folds them
// into the role fingerprint, so a handler with external sources never shares a role
// with one that lacks them.
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

// Renders an ARN MappingNode (a literal scalar or a ${...} substitution) to its
// canonical string form, used both to fingerprint the role plan and to embed the
// ARN in the seeded policy statement.
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

// Renders an external SQS sourceId to an ARN suitable for scoping an IAM statement.
// A literal queue URL (https://sqs.<region>.amazonaws.com/<account>/<name>) is
// converted to its arn:aws:sqs:<region>:<account>:<name> form; a literal ARN or a
// substitution is used verbatim.
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
