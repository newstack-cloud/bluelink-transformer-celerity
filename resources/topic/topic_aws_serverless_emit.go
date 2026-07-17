package topic

import (
	"context"
	"fmt"
	"strings"

	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const fifoSuffix = ".fifo"

const (
	// AnnotationKeyBucketEvents is the celerity/bucket -> celerity/topic link
	// annotation (comma-separated created | deleted | metadataUpdated) selecting which
	// object-storage events flow from a linked bucket into this topic. It maps to the
	// provider aws.s3.sns.event.<index> annotations the aws/s3/bucket::aws/sns/topic
	// link consumes on the topic.
	AnnotationKeyBucketEvents = "celerity.topic.bucket.events"

	// AnnotationKeyBucketFilterPrefix restricts bucket notifications into this topic to
	// object keys with the given prefix (maps to aws.s3.sns.filterPrefix).
	AnnotationKeyBucketFilterPrefix = "celerity.topic.bucket.filterPrefix"

	// AnnotationKeyBucketFilterSuffix restricts bucket notifications into this topic to
	// object keys with the given suffix (maps to aws.s3.sns.filterSuffix).
	AnnotationKeyBucketFilterSuffix = "celerity.topic.bucket.filterSuffix"
)

func emitTopic(
	_ context.Context,
	_ *transformutils.Run,
	r *ResolvedTopic,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	nameNode, _ := pluginutils.GetValueByPath("$.name", r.Resource.Spec)
	fifoNode, _ := pluginutils.GetValueByPath("$.fifo", r.Resource.Spec)
	fifo := core.BoolValue(fifoNode)

	if name := core.StringValue(nameNode); name != "" {
		spec.Fields["topicName"] = core.MappingNodeFromString(topicName(name, fifo))
	}
	if fifo {
		spec.Fields["fifoTopic"] = core.MappingNodeFromBool(true)
	}
	if kms, ok := pluginutils.GetValueByPath("$.encryptionKeyId", r.Resource.Spec); ok {
		spec.Fields["kmsMasterKeyId"] = kms
	}
	// aws/sns/topic.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFromResourceMetadata(r.Resource.Metadata); tags != nil {
		spec.Fields["tags"] = tags
	}

	// Rewrite any ${resources.x.spec.y} references a user embedded in a value
	// (e.g. an encryptionKeyId pointing at another resource) into concrete form.
	spec = subwalk.WalkMappingNode(spec, transformutils.RewriteResourcePropertyRefs(rw))

	res := &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/sns/topic"},
		Spec:         spec,
		Metadata:     topicMetadata(r),
		LinkSelector: r.Resource.LinkSelector,
	}

	// Translate any celerity.topic.bucket.* notification config into the provider
	// aws.s3.sns.* annotations the aws/s3/bucket::aws/sns/topic link consumes.
	diagnostics := stampBucketNotifications(r, res.Metadata)

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			topicConcreteName(r.Name): res,
		},
		Diagnostics: diagnostics,
	}, nil
}

// stampBucketNotifications maps the topic's celerity.topic.bucket.{events,
// filterPrefix,filterSuffix} annotations onto the emitted topic's aws.s3.sns.*
// provider annotations, warning for any event with no S3 equivalent.
func stampBucketNotifications(r *ResolvedTopic, meta *schema.Metadata) []*core.Diagnostic {
	unsupported := sharedaws.StampBucketNotifications(r.Resource, meta, sharedaws.BucketNotificationKeys{
		CelerityEvents:       AnnotationKeyBucketEvents,
		CelerityFilterPrefix: AnnotationKeyBucketFilterPrefix,
		CelerityFilterSuffix: AnnotationKeyBucketFilterSuffix,
		ProviderPrefix:       "aws.s3.sns",
	})
	var diagnostics []*core.Diagnostic
	for _, event := range unsupported {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/topic %q requests bucket notification event %q, which has no aws-serverless "+
					"(S3) equivalent and is ignored; use created or deleted",
				r.Name, event,
			),
		})
	}
	return diagnostics
}

// topicMetadata carries the abstract topic's labels through to the concrete
// resource (so a handler's or bucket's linkSelector can match it) and stamps the
// framework's base annotations.
func topicMetadata(r *ResolvedTopic) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/topic",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
	if r.Resource.Metadata != nil {
		meta.Labels = r.Resource.Metadata.Labels
	}
	return meta
}

func topicName(name string, fifo bool) string {
	if fifo && !strings.HasSuffix(name, fifoSuffix) {
		return name + fifoSuffix
	}
	return name
}

func topicConcreteName(name string) string {
	return fmt.Sprintf("%s_sns_topic", name)
}
