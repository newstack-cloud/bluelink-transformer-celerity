package queueaws

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// fifoSuffix is what an ordered queue's deployed name has to end in.
const fifoSuffix = ".fifo"

const (
	deadLetterMaxAttemptsAnnotation  = queue.AnnotationKeyDeadLetterMaxAttempts
	redriveMaxReceiveCountAnnotation = "aws.sqs.redrive.maxReceiveCount"
)

// Emit builds the SQS queue for an abstract queue. An AWS target that needs more
// than the queue itself calls this and adds to what it returns.
func Emit(
	ctx context.Context,
	run *transformutils.Run,
	target string,
	r *queue.ResolvedQueue,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	fifoNode, _ := pluginutils.GetValueByPath("$.fifo", r.Resource.Spec)

	structural := append(
		[]shared.StructuralField{{Name: "fifo", Value: fifoNode}},
		shared.ExternalModeStructuralFields(r.Resource)...,
	)
	if diagnostics := shared.DeferredValueErrors(
		target,
		"queue", r.Name, r.Resource,
		structural...,
	); len(diagnostics) > 0 {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	// An external queue emits nothing, it names an SQS queue Celerity does not
	// own. Handlers still reach it, through the resources store and a grant on
	// their execution role.
	runCtx, _ := transformutils.Use[*shared.RunContext](run)
	if _, diagnostics, stop := awsexternal.EmitOutcome(
		ctx, runCtx, "celerity/queue", r.Name, r.Resource,
	); stop {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	res := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/sqs/queue"},
		// Rewritten so a ${resources.x.spec.y} reference a user embedded in a
		// value, such as an encryptionKeyId pointing at another resource, reaches
		// the concrete form.
		Spec: subwalk.WalkMappingNode(
			buildQueueSpec(run, r, core.BoolValue(fifoNode)),
			transformutils.RewriteResourcePropertyRefs(rw),
		),
		Metadata: queueMetadata(r),
		// Carried through so a dead-letter queue -> queue relationship still
		// resolves against the concrete queue.
		LinkSelector: r.Resource.LinkSelector,
	}

	// Translate any celerity.queue.bucket.* notification config into the provider
	// aws.s3.sqs.* annotations the aws/s3/bucket::aws/sqs/queue link consumes.
	diagnostics := stampBucketNotifications(r, res.Metadata)

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			queueConcreteName(r.Name): res,
		},
		Diagnostics: diagnostics,
	}, nil
}

// The SQS queue's own shape. The name and FIFO flag come from the abstract spec,
// retention and message size from deploy config.
func buildQueueSpec(
	run *transformutils.Run,
	r *queue.ResolvedQueue,
	fifo bool,
) *core.MappingNode {
	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	nameNode, _ := pluginutils.GetValueByPath("$.name", r.Resource.Spec)
	if queueNameNode := shared.PhysicalNameWithSuffix(nameNode, fifo, fifoSuffix); queueNameNode != nil {
		spec.Fields["queueName"] = queueNameNode
	}
	if fifo {
		spec.Fields["fifoQueue"] = core.MappingNodeFromBool(true)
	}
	if vt, ok := pluginutils.GetValueByPath("$.visibilityTimeout", r.Resource.Spec); ok {
		spec.Fields["visibilityTimeout"] = vt
	}
	if kms, ok := pluginutils.GetValueByPath("$.encryptionKeyId", r.Resource.Spec); ok {
		spec.Fields["kmsMasterKeyId"] = kms
	}

	// Deploy-config-sourced settings (global + per-queue override). These have no
	// spec-field source.
	deployName := core.StringValue(nameNode)
	if deployName == "" {
		deployName = r.Name
	}

	if v, ok := sharedaws.ResolveDeployConfigNode(
		run.TransformContext, "aws.sqs", deployName, "messageRetentionPeriod",
	); ok {
		spec.Fields["messageRetentionPeriod"] = v
	}

	if v, ok := sharedaws.ResolveDeployConfigNode(
		run.TransformContext, "aws.sqs", deployName, "maxMessageSize",
	); ok {
		spec.Fields["maximumMessageSize"] = v
	}

	// aws/sqs/queue.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      shared.ResolveAppName(run),
		ResourceName: r.Name,
		ResourceType: "celerity/queue",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	return spec
}

// stampBucketNotifications maps the queue's celerity.queue.bucket.{events,
// filterPrefix,filterSuffix} annotations onto the emitted queue's aws.s3.sqs.*
// provider annotations, warning for any event with no S3 equivalent.
func stampBucketNotifications(r *queue.ResolvedQueue, meta *schema.Metadata) []*core.Diagnostic {
	result := sharedaws.StampBucketNotifications(r.Resource, meta, sharedaws.BucketNotificationKeys{
		CelerityEvents:       queue.AnnotationKeyBucketEvents,
		CelerityFilterPrefix: queue.AnnotationKeyBucketFilterPrefix,
		CelerityFilterSuffix: queue.AnnotationKeyBucketFilterSuffix,
		ProviderPrefix:       "aws.s3.sqs",
	})
	return sharedaws.BucketNotificationDiagnostics("celerity/queue", r.Name, r.Resource, result)
}

// Carries the abstract queue's labels through to the concrete resource (so a
// handler's or parent queue's linkSelector can match it) and translates the
// celerity dead-letter annotation into the provider redrive annotation.
func queueMetadata(r *queue.ResolvedQueue) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/queue",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}

	if r.Resource.Metadata == nil {
		return meta
	}
	meta.Labels = r.Resource.Metadata.Labels

	// A queue that is a parent for a dead-letter queue carries
	// celerity.queue.deadLetterMaxAttempts; re-key it (value verbatim) so the
	// aws/sqs/queue::aws/sqs/queue link writes it into the source queue's
	// redrivePolicy.maxReceiveCount.
	if annos := r.Resource.Metadata.Annotations; annos != nil {
		if v, ok := annos.Values[deadLetterMaxAttemptsAnnotation]; ok {
			meta.Annotations.Values[redriveMaxReceiveCountAnnotation] = v
		}
	}

	return meta
}

func queueConcreteName(name string) string {
	return fmt.Sprintf("%s_sqs_queue", name)
}

// ConcreteResourceName is the concrete aws/sqs/queue resource name emitted for the
// abstract queue. Exported so the internal resources config store can reference it.
func ConcreteResourceName(name string) string {
	return queueConcreteName(name)
}
