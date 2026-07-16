package handler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ConsumerSourceKind classifies the concrete AWS event source a consumer binds a
// handler to on the aws-serverless target.
type ConsumerSourceKind string

const (
	// ConsumerSourceQueue is an SQS event source mapping (poll model), backed by a
	// linked celerity/queue.
	ConsumerSourceQueue ConsumerSourceKind = "queue"
	// ConsumerSourceDatastore is a DynamoDB stream event source mapping (poll
	// model), backed by a linked celerity/datastore.
	ConsumerSourceDatastore ConsumerSourceKind = "datastore"
	// ConsumerSourceBucket is an S3 object-event notification (push model), backed
	// by a linked celerity/bucket.
	ConsumerSourceBucket ConsumerSourceKind = "bucket"
	// ConsumerSourceTopic is an SNS-topic source referenced through the consumer's
	// sourceId. On aws-serverless it is wired as an SQS fan-out queue subscribed to
	// the topic that in turn triggers the function.
	ConsumerSourceTopic ConsumerSourceKind = "topic"
	// ConsumerSourceExternal is an out-of-blueprint event source described by the
	// consumer's externalEvents (a database stream, data stream or object storage
	// bucket, referenced by ARN) or by a raw external SQS URL/ARN sourceId.
	ConsumerSourceExternal ConsumerSourceKind = "external"
	// ConsumerSourceUnknown is a consumer whose source could not be classified.
	ConsumerSourceUnknown ConsumerSourceKind = ""
)

// celerityTopicSourcePrefix marks a consumer sourceId as a Celerity topic ARN.
const celerityTopicSourcePrefix = "celerity::topic::"

// Consumer source disambiguation annotations (L2). When a consumer matches the
// link selector of multiple sources of the same type, these name the source to
// bind to.
const (
	annConsumerSourceQueue     = "celerity.consumer.queue"
	annConsumerSourceDatastore = "celerity.consumer.datastore"
	annConsumerSourceBucket    = "celerity.consumer.bucket"
)

// ExternalStreamBinding captures one externalEvents stream entry (dbStream or
// dataStream) to be wired as a standalone aws/lambda/eventSourceMapping polling an
// out-of-blueprint stream ARN.
type ExternalStreamBinding struct {
	// Key is the externalEvents map key; it names the emitted event source mapping.
	Key string
	// SourceType is the externalEvents sourceType (dbStream | dataStream).
	SourceType string
	// EventSourceArn is the stream ARN (dbStreamId / dataStreamId) used as the event
	// source mapping's eventSourceArn.
	EventSourceArn *core.MappingNode
	// BatchSize is the optional per-batch size for the mapping.
	BatchSize *core.MappingNode
	// PartialFailures maps to the ReportBatchItemFailures function response type.
	PartialFailures bool
	// StartFromBeginning maps to a startingPosition of TRIM_HORIZON (else LATEST).
	StartFromBeginning bool
}

// ConsumerBinding captures how an absorbed consumer wires its event source to the
// handler's emitted Lambda. The handler emit reads these to stamp event-source
// annotations, propagate the label union, and emit any push-model or standalone
// resources (an SNS subscription, an SQS fan-out queue, an event source mapping).
type ConsumerBinding struct {
	// ConsumerName is the abstract consumer resource name.
	ConsumerName string
	// ConsumerResource is the consumer's abstract resource (spec + metadata).
	ConsumerResource *schema.Resource
	// SourceKind is the classified concrete event source.
	SourceKind ConsumerSourceKind
	// SourceAbstractName is the abstract source resource name (queue/datastore/
	// bucket) the consumer links from; empty for topic and external sources.
	SourceAbstractName string
	// TopicARN is the SNS topic ARN value for a ConsumerSourceTopic binding: either a
	// literal ARN string node (from a celerity::topic::<arn> sourceId) or the
	// original substitution node when the sourceId references an in-blueprint
	// celerity/topic (ARN known only at deploy time).
	TopicARN *core.MappingNode
	// ExternalStreams holds the externalEvents dbStream/dataStream entries to wire as
	// standalone event source mappings.
	ExternalStreams []*ExternalStreamBinding
	// ExternalSQSArn is the ARN/URL of a raw external SQS sourceId; when set the
	// consumer is wired as a standalone SQS event source mapping.
	ExternalSQSArn *core.MappingNode
	// DeferredObjectStores lists externalEvents objectStorage bucket references that
	// are deferred on aws-serverless (an out-of-blueprint S3 notification cannot be
	// configured from this blueprint).
	DeferredObjectStores []string
	// Ambiguous marks a linked-source binding that matched multiple sources of the
	// same type without a disambiguation annotation; a warning is surfaced on emit.
	Ambiguous bool
}

// One classified in-blueprint source edge into a consumer.
type linkedSourceEdge struct {
	kind   ConsumerSourceKind
	source string
}

// Classifies each absorbed consumer's event source using the link graph (for
// linked queue/datastore/bucket sources) and the consumer's own spec (for
// Celerity-topic and external sources).
func resolveConsumerBindings(
	target *ResolvedHandler,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) {
	for _, consumer := range target.Consumers {
		binding := &ConsumerBinding{
			ConsumerName:     consumer.Name,
			ConsumerResource: consumer.Resource,
		}
		classifyConsumerSource(binding, consumer, linkGraph, blueprint)
		target.ConsumerBindings = append(target.ConsumerBindings, binding)
	}
}

func classifyConsumerSource(
	binding *ConsumerBinding,
	consumer *types.LinkedResource,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) {
	// A linked source resource wins: queue/datastore/bucket edges into the consumer
	// describe a concrete, in-blueprint event source.
	linked := collectLinkedSourceEdges(linkGraph.EdgesTo(consumer.Name))
	if len(linked) > 0 {
		selectLinkedSource(binding, consumer.Resource, linked)
		return
	}

	// Otherwise fall back to the consumer's own spec: a Celerity topic sourceId, a
	// raw external SQS sourceId, or an externalEvents configuration.
	classifyFromConsumerSpec(binding, consumer.Resource, blueprint)
}

func collectLinkedSourceEdges(edges []*linktypes.ResolvedLink) []linkedSourceEdge {
	linked := []linkedSourceEdge{}
	for _, edge := range edges {
		if kind, ok := linkedSourceKind(edge.SourceType); ok {
			linked = append(linked, linkedSourceEdge{kind: kind, source: edge.Source})
		}
	}
	return linked
}

// Picks the single source edge to bind. When multiple candidate edges match (a
// consumer matching several same-type sources by label), it honours a
// celerity.consumer.<queue|datastore|bucket> disambiguation annotation naming the
// source; failing that it picks deterministically and flags the binding as
// ambiguous so the emit can warn.
func selectLinkedSource(
	binding *ConsumerBinding,
	resource *schema.Resource,
	edges []linkedSourceEdge,
) {
	if len(edges) == 1 {
		binding.SourceKind = edges[0].kind
		binding.SourceAbstractName = edges[0].source
		return
	}

	if named, ok := namedDisambiguatedSource(resource, edges); ok {
		binding.SourceKind = named.kind
		binding.SourceAbstractName = named.source
		return
	}

	sort.Slice(edges, func(i, j int) bool {
		return edges[i].source < edges[j].source
	})
	binding.SourceKind = edges[0].kind
	binding.SourceAbstractName = edges[0].source
	binding.Ambiguous = true
}

func namedDisambiguatedSource(
	resource *schema.Resource,
	edges []linkedSourceEdge,
) (linkedSourceEdge, bool) {
	for _, edge := range edges {
		annKey, ok := disambiguationAnnotationKey(edge.kind)
		if !ok {
			continue
		}
		value, present := transformutils.GetAnnotation(resource, annKey, "")
		if present && core.StringValue(value) == edge.source {
			return edge, true
		}
	}
	return linkedSourceEdge{}, false
}

func disambiguationAnnotationKey(kind ConsumerSourceKind) (string, bool) {
	switch kind {
	case ConsumerSourceQueue:
		return annConsumerSourceQueue, true
	case ConsumerSourceDatastore:
		return annConsumerSourceDatastore, true
	case ConsumerSourceBucket:
		return annConsumerSourceBucket, true
	default:
		return "", false
	}
}

func linkedSourceKind(sourceType string) (ConsumerSourceKind, bool) {
	switch sourceType {
	case "celerity/queue":
		return ConsumerSourceQueue, true
	case "celerity/datastore":
		return ConsumerSourceDatastore, true
	case "celerity/bucket":
		return ConsumerSourceBucket, true
	default:
		return ConsumerSourceUnknown, false
	}
}

func classifyFromConsumerSpec(
	binding *ConsumerBinding,
	resource *schema.Resource,
	blueprint *schema.Blueprint,
) {
	if resource == nil || resource.Spec == nil {
		return
	}

	if sourceID, ok := pluginutils.GetValueByPath("$.sourceId", resource.Spec); ok && sourceID != nil {
		if classifyFromSourceID(binding, sourceID, blueprint) {
			return
		}
	}

	if events, ok := pluginutils.GetValueByPath("$.externalEvents", resource.Spec); ok && events != nil {
		classifyExternalEvents(binding, events)
	}
}

// Classifies a consumer sourceId into a topic (literal ARN or in-blueprint
// substitution) or a raw external SQS source. Reports whether it consumed the
// sourceId.
func classifyFromSourceID(
	binding *ConsumerBinding,
	sourceID *core.MappingNode,
	blueprint *schema.Blueprint,
) bool {
	if arn, isTopic := celerityTopicARN(sourceID); isTopic {
		binding.SourceKind = ConsumerSourceTopic
		binding.TopicARN = core.MappingNodeFromString(arn)
		return true
	}

	if inBlueprintTopicSourceID(sourceID, blueprint) {
		binding.SourceKind = ConsumerSourceTopic
		// Wire the fan-out using the substitution verbatim as the topic ARN; the
		// concrete topic ARN is resolved at deploy time.
		binding.TopicARN = sourceID
		return true
	}

	if externalSQSSourceID(sourceID) {
		binding.SourceKind = ConsumerSourceExternal
		binding.ExternalSQSArn = sourceID
		return true
	}

	return false
}

// Returns the topic ARN carried by a sourceId literal of the form
// "celerity::topic::<arn>". Only handles literal string sourceIds; a sourceId
// expressed as a substitution cannot be classified as a topic here.
func celerityTopicARN(sourceID *core.MappingNode) (string, bool) {
	value := core.StringValue(sourceID)
	if !strings.HasPrefix(value, celerityTopicSourcePrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, celerityTopicSourcePrefix), true
}

// Reports whether the sourceId is a substitution that references an in-blueprint
// celerity/topic resource (e.g. ${ordersTopic.spec.arn}). Only resource-property
// references can be classified here.
func inBlueprintTopicSourceID(sourceID *core.MappingNode, blueprint *schema.Blueprint) bool {
	if sourceID == nil || sourceID.StringWithSubstitutions == nil {
		return false
	}
	for _, value := range sourceID.StringWithSubstitutions.Values {
		sub := value.SubstitutionValue
		if sub == nil || sub.ResourceProperty == nil {
			continue
		}
		res := shared.GetResourceByName(blueprint, sub.ResourceProperty.ResourceName)
		if res != nil && res.Type != nil && res.Type.Value == "celerity/topic" {
			return true
		}
	}
	return false
}

// Reports whether a literal sourceId is a raw external SQS URL or ARN (an
// out-of-blueprint queue).
func externalSQSSourceID(sourceID *core.MappingNode) bool {
	value := core.StringValue(sourceID)
	return strings.HasPrefix(value, "https://sqs.") ||
		strings.HasPrefix(value, "arn:aws:sqs:")
}

func classifyExternalEvents(binding *ConsumerBinding, externalEvents *core.MappingNode) {
	binding.SourceKind = ConsumerSourceExternal

	// Deterministic ordering so emitted resource names are stable.
	keys := make([]string, 0, len(externalEvents.Fields))
	for key := range externalEvents.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		config := externalEvents.Fields[key]
		sourceType := ""
		if node, ok := pluginutils.GetValueByPath("$.sourceType", config); ok {
			sourceType = core.StringValue(node)
		}
		switch sourceType {
		case "dbStream", "dataStream":
			if stream := externalStreamBinding(key, sourceType, config); stream != nil {
				binding.ExternalStreams = append(binding.ExternalStreams, stream)
			}
		case "objectStorage":
			binding.DeferredObjectStores = append(
				binding.DeferredObjectStores,
				objectStorageBucketRef(key, config),
			)
		}
	}
}

func externalStreamBinding(
	key string,
	sourceType string,
	config *core.MappingNode,
) *ExternalStreamBinding {
	arnPath := "$.sourceConfiguration.dbStreamId"
	if sourceType == "dataStream" {
		arnPath = "$.sourceConfiguration.dataStreamId"
	}
	arn, ok := pluginutils.GetValueByPath(arnPath, config)
	if !ok || arn == nil {
		return nil
	}

	stream := &ExternalStreamBinding{
		Key:            key,
		SourceType:     sourceType,
		EventSourceArn: arn,
	}
	if batchSize, ok := pluginutils.GetValueByPath("$.sourceConfiguration.batchSize", config); ok {
		stream.BatchSize = batchSize
	}
	if pf, ok := pluginutils.GetValueByPath("$.sourceConfiguration.partialFailures", config); ok {
		stream.PartialFailures = core.BoolValue(pf)
	}
	if sfb, ok := pluginutils.GetValueByPath("$.sourceConfiguration.startFromBeginning", config); ok {
		stream.StartFromBeginning = core.BoolValue(sfb)
	}
	return stream
}

func objectStorageBucketRef(key string, config *core.MappingNode) string {
	if bucket, ok := pluginutils.GetValueByPath("$.sourceConfiguration.bucket", config); ok {
		if name := core.StringValue(bucket); name != "" {
			return name
		}
	}
	return key
}

// Collects the labels of every absorbed consumer so the emitted Lambda can carry
// them. A source's linkSelector selected the consumer by label; propagating those
// labels onto the function re-establishes the concrete source -> function event
// source once the consumer is absorbed.
func consumerLabelUnion(r *ResolvedHandler) (map[string]string, []*core.Diagnostic) {
	labels := map[string]string{}
	origin := map[string]string{}
	var diagnostics []*core.Diagnostic
	for _, consumer := range r.Consumers {
		if consumer.Resource == nil || consumer.Resource.Metadata == nil {
			continue
		}
		if consumer.Resource.Metadata.Labels == nil {
			continue
		}
		for key, value := range consumer.Resource.Metadata.Labels.Values {
			if existing, ok := labels[key]; ok && existing != value {
				// Two consumers disagree on the same label; the function can only
				// carry one value, so the conflict is surfaced rather than silently
				// resolved. Identical duplicate values are fine and never warn.
				diagnostics = append(diagnostics, &core.Diagnostic{
					Level: core.DiagnosticLevelWarning,
					Message: fmt.Sprintf(
						"celerity/handler %q absorbs consumers %q and %q that set label %q to conflicting "+
							"values (%q vs %q); the function keeps %q. Give the consumers matching labels to "+
							"avoid ambiguous link resolution",
						r.Name, origin[key], consumer.Name, key, existing, value, existing,
					),
				})
				continue
			}
			labels[key] = value
			origin[key] = consumer.Name
		}
	}
	return labels, diagnostics
}
