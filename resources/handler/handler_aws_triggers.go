package handler

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/source"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// annotationVPCSubnetType is the aws/flex/vpc::aws/lambda/function link annotation
	// (AppliesTo resourceB, the function) selecting which subnet tier the function
	// is placed in.
	annotationVPCSubnetType = "aws.flexvpc.lambda.subnetType"

	// annotationVPCEgress is the aws/flex/vpc::aws/lambda/function link annotation
	// (AppliesTo resourceB, the function) narrowing what the function may reach
	// outside the VPC. Stamped only when the author set it: the provider reads its
	// absence as "resolve the reach from the VPC's topology".
	annotationVPCEgress = "aws.flexvpc.lambda.egress"

	// SQS event-source-mapping link annotations (AppliesTo the function).
	annotationSQSBatchSize               = "aws.sqs.lambda.batchSize"
	annotationSQSReportBatchItemFailures = "aws.sqs.lambda.reportBatchItemFailures"

	// DynamoDB stream event-source-mapping link annotations (AppliesTo the function).
	annotationDynamoDBBatchSize               = "aws.dynamodb.lambda.stream.batchSize"
	annotationDynamoDBStartingPosition        = "aws.dynamodb.lambda.stream.startingPosition"
	annotationDynamoDBReportBatchItemFailures = "aws.dynamodb.lambda.stream.reportBatchItemFailures"

	// S3 object-event notification link annotation (AppliesTo the function).
	annotationS3Event = "aws.s3.lambda.event.%d"

	// batchSizePath is the consumer spec path for the poll batch size.
	batchSizePath = "$.batchSize"

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
	annotationConsumerDatastoreStartFromBeginning = AnnotationKeyConsumerDatastoreStartFromBeginning
	annotationConsumerBucketEvents                = AnnotationKeyConsumerBucketEvents

	// annotationConsumerDeadLetterQueue toggles creation of a dead-letter queue for a
	// Celerity-topic consumer (default true).
	annotationConsumerDeadLetterQueue = AnnotationKeyConsumerDeadLetterQueue
	// annotationConsumerDeadLetterQueueMaxAttempts sets the DLQ redrive maxReceiveCount.
	annotationConsumerDeadLetterQueueMaxAttempts = AnnotationKeyConsumerDeadLetterQueueMaxAttempts

	// annotationConsumerTopicPollPrefix is a synthetic per-consumer label key (value
	// "true") stamped on the function and the topic fan-out queue's linkSelector so
	// the aws/sqs/queue::aws/lambda/function event-source-mapping link (and the
	// queue's redrive link to its DLQ) activate by label-on-source. It is namespaced
	// by consumer name so a handler absorbing several topic consumers keeps one
	// distinct wiring label per consumer.
	annotationConsumerTopicPollPrefix = "celerity.consumer.topicPoll."
)

// Sets the emitted function's labels to the union of the handler's own labels and
// every absorbed consumer's labels (handler wins on key conflicts). This lets an
// inbound source/api/subscription link that selected the consumer by label now
// resolve against the concrete function.
func applyLambdaLabels(r *ResolvedHandler, lambda *schema.Resource) []*core.Diagnostic {
	labels, diagnostics := consumerLabelUnion(r)
	if r.Resource.Metadata != nil && r.Resource.Metadata.Labels != nil {
		maps.Copy(labels, r.Resource.Metadata.Labels.Values)
	}
	// A placement inherited from an application component is matched on the
	// component's labels, which the function does not otherwise carry. Adding
	// only the labels the VPC's selector matched lets the concrete
	// VPC-to-function link select the function without widening it to every
	// label on the component.
	maps.Copy(labels, r.inheritedVPCSelectorLabels)
	if key, value, ok := guardTargetLabel(r); ok {
		labels[key] = value
	}
	if len(labels) == 0 {
		return diagnostics
	}
	lambda.Metadata.Labels = &schema.StringMap{Values: labels}
	return diagnostics
}

// The label pairing a custom guard's handler function with the API whose authorizer
// invokes it, for handlers that implement one.
//
// Only the guard's own handler carries it. A handler merely protected BY the guard is
// a route, and its permission comes from the api::function link instead.
func guardTargetLabel(r *ResolvedHandler) (string, string, bool) {
	if r.APILink == nil {
		return "", "", false
	}

	guardName := annotationValue(r.Resource, AnnotationKeyGuardCustom, "")
	if guardName == "" {
		return "", "", false
	}

	return LabelKeyGuardTarget, GuardTargetLabelValue(r.APILink.Name, guardName), true
}

// Layers the VPC-placement and consumer event-source link annotations onto the
// emitted function's metadata. Returns diagnostics for any consumer source that
// could not be fully wired on aws-serverless.
func stampTriggerAnnotations(r *ResolvedHandler, lambda *schema.Resource) []*core.Diagnostic {
	if r.VPC != nil {
		setStringAnnotation(lambda.Metadata, annotationVPCSubnetType, r.VPCSubnetType)
		// Absent rather than empty when the author said nothing, so the provider
		// resolves the reach from the VPC's topology instead of reading a value.
		if r.VPCEgress != "" {
			setStringAnnotation(lambda.Metadata, annotationVPCEgress, r.VPCEgress)
		}
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
			r, sqs, "queue/topic",
			"SQS event-source-mapping settings (batchSize, reportBatchItemFailures)",
		))
	}
	if batchOrFailuresConflict(ddb) || startingPositionConflict(ddb) {
		diagnostics = append(diagnostics, sharedSettingWarning(
			r, ddb, "datastore",
			"DynamoDB stream settings (batchSize, reportBatchItemFailures, startingPosition)",
		))
	}
	if bucketEventSetsDiverge(bucket) {
		diagnostics = append(diagnostics, bucketEventUnionWarning(r, bucket))
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
		if node, ok := pluginutils.GetValueByPath(batchSizePath, spec); ok {
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
	r *ResolvedHandler,
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
			r.Name, kindLabel, consumerNameList(bindings), settingLabel,
		),
		Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
	}
}

func bucketEventUnionWarning(r *ResolvedHandler, bindings []*ConsumerBinding) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/handler %q absorbs multiple bucket consumers (%s) requesting different event sets, "+
				"but on aws-serverless the S3 notification events are combined into one list applied to "+
				"every linked bucket, so each bucket triggers on the union rather than only its own events. "+
				"Split the consumers across separate handlers for independent event sets.",
			r.Name, consumerNameList(bindings),
		),
		Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
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

// Warns when a consumer declares a routingKey but this function serves only one
// of its handlers, so nothing will route.
func routingKeyDeferred(r *ResolvedHandler, binding *ConsumerBinding) *core.Diagnostic {
	if !r.HasRoutingTag {
		return nil
	}
	// The function serves the whole consumer, so it routes in-process and the key
	// reaches it.
	if r.RoutingKey != "" {
		return nil
	}
	node, ok := pluginutils.GetValueByPath("$.routingKey", consumerSpec(binding))
	if !ok || node == nil {
		return nil
	}
	key := core.StringValue(node)
	if key == "" {
		return nil
	}
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/consumer %q sets routingKey %q, but handler %q is deployed as its own function "+
				"serving a single route rather than as one function for the whole consumer. Messages are "+
				"delivered to whichever of the consumer's functions polls first, so the routing key is not "+
				"applied. Every handler of a routed consumer has to be deployed as one function for "+
				"routing to happen.",
			binding.ConsumerName,
			key,
			r.Name,
		),
		Range: consumerRange(binding, node),
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
	if node, ok := pluginutils.GetValueByPath(batchSizePath, spec); ok {
		setIntAnnotation(lambda.Metadata, annotationSQSBatchSize, core.IntValue(node))
	}
	if partialFailures(spec) {
		setBoolAnnotation(lambda.Metadata, annotationSQSReportBatchItemFailures, true)
	}
}

func stampDatastoreBinding(lambda *schema.Resource, binding *ConsumerBinding) {
	// startingPosition is required by the provider link; derive it from the
	// consumer's celerity.consumer.datastore.startFromBeginning annotation.
	setStringAnnotation(lambda.Metadata, annotationDynamoDBStartingPosition, startingPosition(binding))

	spec := consumerSpec(binding)
	if node, ok := pluginutils.GetValueByPath(batchSizePath, spec); ok {
		setIntAnnotation(lambda.Metadata, annotationDynamoDBBatchSize, core.IntValue(node))
	}
	if partialFailures(spec) {
		setBoolAnnotation(lambda.Metadata, annotationDynamoDBReportBatchItemFailures, true)
	}
}

// stampBucketBinding stamps the S3 notification event annotations starting at
// baseIndex (so successive bucket bindings occupy distinct indices in the
// function's flat event list) and returns how many it stamped.
func stampBucketBinding(lambda *schema.Resource, binding *ConsumerBinding, baseIndex int) (int, []*core.Diagnostic) {
	events, unsupported := bucketEvents(binding)
	for offset, event := range events {
		setStringAnnotation(lambda.Metadata, fmt.Sprintf(annotationS3Event, baseIndex+offset), event)
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
	if node, ok := pluginutils.GetValueByPath(batchSizePath, spec); ok {
		setIntAnnotation(lambda.Metadata, annotationSQSBatchSize, core.IntValue(node))
	}
	if partialFailures(spec) {
		setBoolAnnotation(lambda.Metadata, annotationSQSReportBatchItemFailures, true)
	}
	setLabel(lambda.Metadata, topicPollLabelKey(binding.ConsumerName), "true")
}

func startingPosition(binding *ConsumerBinding) string {
	fromBeginning, _ := transformutils.GetBoolAnnotation(
		binding.ConsumerResource,
		annotationConsumerDatastoreStartFromBeginning,
		"",
		false,
	)
	if fromBeginning {
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
		annotationConsumerBucketEvents,
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
	return sharedaws.S3EventForCelerityEvent(consumerEvent)
}

// Builds one aws/events/rule per absorbed schedule, targeting the emitted function.
// Setting targets[].arn to the function reference activates the provider's
// aws/events/rule::aws/lambda/function link (which creates the invoke permission),
// so no permission is emitted here.
func emitScheduleRules(
	r *ResolvedHandler,
	funcResourceName string,
	appName string,
) (map[string]*schema.Resource, []*core.Diagnostic, error) {
	rules := map[string]*schema.Resource{}
	var diagnostics []*core.Diagnostic
	for _, schedule := range r.Schedules {
		rule, diag, err := scheduleRule(schedule.Name, schedule.Resource, funcResourceName, appName)
		if err != nil {
			return nil, nil, err
		}
		if diag != nil {
			diagnostics = append(diagnostics, diag)
		}
		if rule != nil {
			rules[scheduleRuleResourceName(schedule.Name)] = rule
		}
	}
	return rules, diagnostics, nil
}

func scheduleRule(
	scheduleName string,
	scheduleResource *schema.Resource,
	funcResourceName string,
	appName string,
) (*schema.Resource, *core.Diagnostic, error) {
	expression := scheduleExpression(scheduleResource)
	if expression == "" {
		// An events/rule with an empty scheduleExpression is invalid and would only
		// fail at deploy; report it and emit no rule instead.
		return nil, &core.Diagnostic{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"celerity/schedule %q has no schedule expression (spec.schedule); no aws/events/rule has "+
					"been emitted. Set a rate() or cron() expression",
				scheduleName,
			),
			Range: shared.DiagnosticRange(shared.ResourceMeta(scheduleResource)),
		}, nil
	}

	arnRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", funcResourceName),
	)
	if err != nil {
		return nil, nil, err
	}

	target := core.MappingNodeFields(
		"id", core.MappingNodeFromString(funcResourceName),
		"arn", arnRef,
	)
	if input, ok := scheduleInput(scheduleResource); ok {
		target.Fields["input"] = core.MappingNodeFromString(input)
	}

	// An explicit, app-scoped rule name: the Cloud Control AWS::Events::Rule
	// handler produces a null pointer exception
	// ("Cannot invoke CharSequence.length() because this.text is
	// null") when Name is omitted (verified against the live API),
	// and app scoping keeps concurrent apps/runs collision-free like every
	// other deployed name (EventBridge rule names cap at 64 chars).
	spec := core.MappingNodeFields(
		"name", core.MappingNodeFromString(
			shared.AppScopedPhysicalName(appName, scheduleName+"-rule", 64)),
		"scheduleExpression", core.MappingNodeFromString(expression),
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
	}, nil, nil
}

func scheduleExpression(scheduleResource *schema.Resource) string {
	if scheduleResource == nil {
		return ""
	}
	node, _ := pluginutils.GetValueByPath("$.schedule", scheduleResource.Spec)
	return core.StringValue(node)
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

// Builds the event-source resources an absorbed consumer needs that genuinely
// belong to this one function: a standalone aws/lambda/eventSourceMapping for
// each external stream or external SQS source.
//
// A Celerity-topic source contributes none. Its fan-out belongs to the consumer
// rather than to any one handler absorbing it, so collectTopicFanouts
// contributes it as a shared parent instead, and the topic branch here only
// validates.
func emitConsumerSubscriptions(
	r *ResolvedHandler,
	funcResourceName string,
) (map[string]*schema.Resource, error) {
	resources := map[string]*schema.Resource{}
	for _, binding := range r.ConsumerBindings {
		switch binding.SourceKind {
		case ConsumerSourceTopic:
			// The fan-out belongs to the consumer, not to any one handler that
			// absorbs it, so it is contributed as a shared parent instead (see
			// collectTopicFanouts). Its references are built here as well, purely
			// so a malformed one is reported against the handler that owns the
			// binding; shared parent collection has no way to report an error.
			if _, err := topicSnsSubscriptionSpec(
				topicQueueResourceName(binding.ConsumerName),
			); err != nil {
				return nil, err
			}
		case ConsumerSourceExternal:
			// An event source mapping really is per-function, so it stays here.
			if err := emitExternalSources(resources, binding, funcResourceName); err != nil {
				return nil, err
			}
		}
	}
	return resources, nil
}

// Collects the fan-out for every Celerity-topic consumer the handlers absorb,
// one per consumer rather than one per handler.
//
// A consumer with a routingKey is absorbed by every handler it routes to, and
// each of them would otherwise contribute the same three resources under the
// same keys and collide; the seen set is what reduces them to one.
func collectTopicFanouts(
	handlers []*ResolvedHandler,
	appName string,
) ([]transformutils.SharedParent, error) {
	parents := []transformutils.SharedParent{}
	seen := map[string]bool{}

	for _, handler := range handlers {
		for _, binding := range handler.ConsumerBindings {
			if binding.SourceKind != ConsumerSourceTopic || seen[binding.ConsumerName] {
				continue
			}
			seen[binding.ConsumerName] = true

			fanout, err := topicFanoutParents(binding, appName)
			if err != nil {
				return nil, err
			}
			parents = append(parents, fanout...)
		}
	}

	return parents, nil
}

// The SNS -> SQS -> Lambda fan-out one Celerity-topic consumer needs: an
// intermediary SQS queue subscribed to the topic (giving a reliable, scalable
// fan-out and the subscriberId output), an SNS subscription (sqs protocol) and,
// unless disabled, a dead-letter queue with a redrive policy.
func topicFanoutParents(
	binding *ConsumerBinding,
	appName string,
) ([]transformutils.SharedParent, error) {
	queueName := topicQueueResourceName(binding.ConsumerName)

	subscriptionSpec, err := topicSnsSubscriptionSpec(queueName)
	if err != nil {
		return nil, err
	}

	parents := []transformutils.SharedParent{
		topicFanoutQueueParent(binding, queueName, appName),
		{
			Key:             "topicSubscription:" + binding.ConsumerName,
			ResourceName:    snsSubscriptionResourceName(binding.ConsumerName),
			ResourceType:    "aws/sns/subscription",
			LinkAnnotations: consumerBaseAnnotations(binding.ConsumerName),
			SeedSpec:        subscriptionSpec,
		},
	}

	if deadLetterQueueEnabled(binding) {
		parents = append(parents, topicDeadLetterQueueParent(binding, appName))
	}

	return parents, nil
}

// The intermediary SQS queue subscribed to the topic. Its linkSelector carries the
// synthetic poll label so the aws/sqs/queue::aws/lambda/function link creates the
// event source mapping to the function; the same label (plus a redrive annotation)
// links it to its dead-letter queue.
func topicFanoutQueueParent(
	binding *ConsumerBinding,
	queueName string,
	appName string,
) transformutils.SharedParent {
	annotations := consumerBaseAnnotations(binding.ConsumerName)
	if attempts, ok := transformutils.GetAnnotation(
		binding.ConsumerResource,
		annotationConsumerDeadLetterQueueMaxAttempts,
		"",
	); ok && deadLetterQueueEnabled(binding) {
		annotations.Values[redriveMaxReceiveCountAnnotation] = pluginutils.StringToSubstitutions(
			core.StringValue(attempts),
		)
	}

	return transformutils.SharedParent{
		Key:             "topicQueue:" + binding.ConsumerName,
		ResourceName:    queueName,
		ResourceType:    "aws/sqs/queue",
		LinkAnnotations: annotations,
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{
				Values: map[string]string{
					topicPollLabelKey(binding.ConsumerName): "true",
				},
			},
		},
		SeedSpec: fanoutQueueSpec(appName, binding, "topic-queue"),
	}
}

func topicDeadLetterQueueParent(
	binding *ConsumerBinding,
	appName string,
) transformutils.SharedParent {
	return transformutils.SharedParent{
		Key:             "topicDLQ:" + binding.ConsumerName,
		ResourceName:    topicDLQResourceName(binding.ConsumerName),
		ResourceType:    "aws/sqs/queue",
		LinkAnnotations: consumerBaseAnnotations(binding.ConsumerName),
		// The fan-out queue's linkSelector selects this queue by the poll label,
		// activating the aws/sqs/queue::aws/sqs/queue redrive link.
		Labels: &schema.StringMap{
			Values: map[string]string{
				topicPollLabelKey(binding.ConsumerName): "true",
			},
		},
		SeedSpec: fanoutQueueSpec(appName, binding, "topic-dlq"),
	}
}

// An SQS queue name is account-visible, so it is app-scoped like every other
// physical name the transformer invents. 80 is the SQS limit.
//
// Naming these also keeps them serialisable: a resource whose spec is an empty
// mapping node does not survive the plugin boundary, because protobuf cannot
// tell an empty map from an absent one, and it arrives back at the engine with
// nothing set at all.
func fanoutQueueSpec(appName string, binding *ConsumerBinding, role string) *core.MappingNode {
	spec := core.MappingNodeFields(
		"queueName", core.MappingNodeFromString(
			shared.AppScopedPhysicalName(
				appName,
				fmt.Sprintf("%s-%s", binding.ConsumerName, role),
				sqsQueueNameMaxLength,
			),
		),
	)

	// The fan-out queue and its dead-letter queue exist to serve one consumer, so
	// they carry that consumer's identity and its author's labels rather than the
	// absorbing handler's. Passing no labels would leave them the only resources
	// an author labelled that cannot be found by those labels in a bill.
	if tags := sharedaws.SpecTagsFor(consumerMetadata(binding), sharedaws.Identity{
		AppName:      appName,
		ResourceName: binding.ConsumerName,
		ResourceType: "celerity/consumer",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	return spec
}

// The abstract consumer's metadata, which is where its author's labels live. The
// generated poll label is set on the emitted queue instead, so it stays out of
// the tags.
func consumerMetadata(binding *ConsumerBinding) *schema.Metadata {
	if binding == nil || binding.ConsumerResource == nil {
		return nil
	}

	return binding.ConsumerResource.Metadata
}

const sqsQueueNameMaxLength = 80

func topicSnsSubscriptionSpec(
	queueResourceName string,
) (*core.MappingNode, error) {
	endpointRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", queueResourceName),
	)
	if err != nil {
		return nil, err
	}

	// topicArn is contributed by the absorbing handler rather than seeded here:
	// an in-blueprint celerity/topic is carried as an abstract reference that only
	// the emit phase's resource-property rewriter can resolve to the concrete
	// <topic>_sns_topic. See topicFanoutContributions.
	return core.MappingNodeFields(
		"protocol", core.MappingNodeFromString("sqs"),
		"endpoint", endpointRef,
		// Without raw delivery SQS receives the SNS envelope, so a handler reading
		// the message body gets {Type, MessageId, TopicArn, Message} and has to know
		// its payload is a JSON string nested in Message. The consumer contract says
		// the body is what was published, and routing reads a field off that body,
		// so the envelope breaks both. Raw delivery makes the wire match the
		// contract rather than asking every handler to unwrap.
		"rawMessageDelivery", core.MappingNodeFromBool(true),
	), nil
}

// The parts of a topic fan-out that only the emit phase can resolve, keyed by
// the shared parent they belong to.
//
// Every handler that absorbs the consumer contributes the same value, which
// merges cleanly; the alternative, seeding it during shared parent collection,
// would leave an abstract reference no resource in the transformed blueprint
// owns.
func topicFanoutContributions(
	r *ResolvedHandler,
	resPropRewriter transformutils.ResourcePropertyRewriter,
) map[string]*core.MappingNode {
	contributions := map[string]*core.MappingNode{}
	visitor := transformutils.RewriteResourcePropertyRefs(resPropRewriter)

	for _, binding := range r.ConsumerBindings {
		if binding.SourceKind != ConsumerSourceTopic {
			continue
		}
		contributions["topicSubscription:"+binding.ConsumerName] = core.MappingNodeFields(
			"topicArn", subwalk.WalkMappingNode(binding.TopicARN, visitor),
		)
	}

	return contributions
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
	// eventSourceArn requires a queue ARN; a sourceId given as a queue URL is
	// normalised to its ARN the same way as for IAM scoping.
	eventSourceArn := core.MappingNodeFromString(externalSQSResourceARN(binding.ExternalSQSArn))
	spec, err := eventSourceMappingSpec(eventSourceArn, funcResourceName)
	if err != nil {
		return nil, err
	}

	consumerSpec := consumerSpec(binding)
	if node, ok := pluginutils.GetValueByPath(batchSizePath, consumerSpec); ok {
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
	// Default enabled to true. When absent, or present as something that is not a flag,
	// keep the queue.
	enabled, _ := transformutils.GetBoolAnnotation(
		binding.ConsumerResource,
		annotationConsumerDeadLetterQueue,
		"",
		true,
	)
	return enabled
}

func topicPollLabelKey(consumerName string) string {
	return annotationConsumerTopicPollPrefix + consumerName
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
	annotationKey, _ := disambiguationAnnotationKey(binding.SourceKind)
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/consumer %q matches multiple %s sources by label; none was named via the %q "+
				"annotation, so %q was selected deterministically. Set %q to the intended source name "+
				"to make the binding explicit.",
			binding.ConsumerName,
			binding.SourceKind,
			annotationKey,
			binding.SourceAbstractName,
			annotationKey,
		),
		Range: consumerRange(binding),
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
		Range: consumerRange(binding),
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
			Range: consumerRange(binding),
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
		Range: consumerRange(binding),
	}
}

// Anchors a diagnostic to the consumer resource a binding came
// from. Diagnostics about a binding are almost always about what the consumer
// declared, so the consumer is the useful place to point even when the concrete
// source lives elsewhere.
func consumerRange(binding *ConsumerBinding, nodes ...*core.MappingNode) *core.DiagnosticRange {
	candidates := make([]*source.Meta, 0, len(nodes)+1)
	for _, node := range nodes {
		candidates = append(candidates, shared.NodeMeta(node))
	}
	candidates = append(candidates, shared.ResourceMeta(binding.ConsumerResource))

	return shared.DiagnosticRange(candidates...)
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
