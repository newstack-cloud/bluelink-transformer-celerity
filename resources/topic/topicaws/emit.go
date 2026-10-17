package topicaws

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// fifoSuffix is what an ordered topic's deployed name has to end in.
const fifoSuffix = ".fifo"

// Emit builds the SNS topic a celerity/topic maps to, for the named AWS
// deploy target.
func Emit(
	ctx context.Context,
	run *transformutils.Run,
	target string,
	r *topic.ResolvedTopic,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	fifoNode, _ := pluginutils.GetValueByPath("$.fifo", r.Resource.Spec)

	structural := append(
		[]shared.StructuralField{{Name: "fifo", Value: fifoNode}},
		shared.ExternalModeStructuralFields(r.Resource)...,
	)
	if diagnostics := shared.DeferredValueErrors(
		target,
		"topic", r.Name, r.Resource,
		structural...,
	); len(diagnostics) > 0 {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	// An external topic emits nothing: it names an SNS topic Celerity does not
	// own. Handlers still reach it, through the resources store and a grant on
	// their execution role.
	runCtx, _ := transformutils.Use[*shared.RunContext](run)
	if _, diagnostics, stop := awsexternal.EmitOutcome(
		ctx, runCtx, "celerity/topic", r.Name, r.Resource,
	); stop {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	res := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/sns/topic"},
		// Rewritten so a ${resources.x.spec.y} reference a user embedded in a
		// value, such as an encryptionKeyId pointing at another resource, reaches
		// the concrete form.
		Spec: subwalk.WalkMappingNode(
			buildTopicSpec(run, r, core.BoolValue(fifoNode)),
			transformutils.RewriteResourcePropertyRefs(rw),
		),
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

// The SNS topic's own shape, from the abstract spec plus the tags every emitted
// resource carries.
func buildTopicSpec(
	run *transformutils.Run,
	r *topic.ResolvedTopic,
	fifo bool,
) *core.MappingNode {
	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	nameNode, _ := pluginutils.GetValueByPath("$.name", r.Resource.Spec)
	if topicNameNode := shared.PhysicalNameWithSuffix(nameNode, fifo, fifoSuffix); topicNameNode != nil {
		spec.Fields["topicName"] = topicNameNode
	}
	if fifo {
		spec.Fields["fifoTopic"] = core.MappingNodeFromBool(true)
	}
	if kms, ok := pluginutils.GetValueByPath("$.encryptionKeyId", r.Resource.Spec); ok {
		spec.Fields["kmsMasterKeyId"] = kms
	}
	// aws/sns/topic.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      shared.ResolveAppName(run),
		ResourceName: r.Name,
		ResourceType: "celerity/topic",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	return spec
}

// stampBucketNotifications maps the topic's celerity.topic.bucket.{events,
// filterPrefix,filterSuffix} annotations onto the emitted topic's aws.s3.sns.*
// provider annotations, warning for any event with no S3 equivalent.
func stampBucketNotifications(r *topic.ResolvedTopic, meta *schema.Metadata) []*core.Diagnostic {
	result := sharedaws.StampBucketNotifications(r.Resource, meta, sharedaws.BucketNotificationKeys{
		CelerityEvents:       topic.AnnotationKeyBucketEvents,
		CelerityFilterPrefix: topic.AnnotationKeyBucketFilterPrefix,
		CelerityFilterSuffix: topic.AnnotationKeyBucketFilterSuffix,
		ProviderPrefix:       "aws.s3.sns",
	})

	return sharedaws.BucketNotificationDiagnostics("celerity/topic", r.Name, r.Resource, result)
}

func topicMetadata(r *topic.ResolvedTopic) *schema.Metadata {
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

func topicConcreteName(name string) string {
	return fmt.Sprintf("%s_sns_topic", name)
}

// ConcreteResourceName is the concrete aws/sns/topic resource name emitted for the
// abstract topic. Exported so a queue's topic-forwarding emit can reference the
// topic (for the function::sns env-var-rename annotation key).
func ConcreteResourceName(name string) string {
	return topicConcreteName(name)
}
