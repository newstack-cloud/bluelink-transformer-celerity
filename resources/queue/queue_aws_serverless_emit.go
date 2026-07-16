package queue

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
	// deadLetterMaxAttemptsAnnotation is the celerity/queue -> celerity/queue link
	// annotation set on a queue used as a dead-letter queue's parent; it maps to the
	// provider redrive annotation the aws/sqs/queue::aws/sqs/queue link consumes.
	deadLetterMaxAttemptsAnnotation  = "celerity.queue.deadLetterMaxAttempts"
	redriveMaxReceiveCountAnnotation = "aws.sqs.redrive.maxReceiveCount"
)

func emitQueue(
	_ context.Context,
	run *transformutils.Run,
	r *ResolvedQueue,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	nameNode, _ := pluginutils.GetValueByPath("$.name", r.Resource.Spec)
	fifoNode, _ := pluginutils.GetValueByPath("$.fifo", r.Resource.Spec)
	fifo := core.BoolValue(fifoNode)

	if name := core.StringValue(nameNode); name != "" {
		spec.Fields["queueName"] = core.MappingNodeFromString(queueName(name, fifo))
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

	// Deploy-config-sourced settings (global + per-queue override, §2.1). These
	// have no spec-field source.
	deployName := core.StringValue(nameNode)
	if deployName == "" {
		deployName = r.Name
	}
	if v, ok := sharedaws.ResolveDeployConfigNode(run.TransformContext, "aws.sqs", deployName, "messageRetentionPeriod"); ok {
		spec.Fields["messageRetentionPeriod"] = v
	}
	if v, ok := sharedaws.ResolveDeployConfigNode(run.TransformContext, "aws.sqs", deployName, "maxMessageSize"); ok {
		spec.Fields["maximumMessageSize"] = v
	}
	// aws/sqs/queue.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFromResourceMetadata(r.Resource.Metadata); tags != nil {
		spec.Fields["tags"] = tags
	}

	// Rewrite any ${resources.x.spec.y} references a user embedded in a value
	// (e.g. an encryptionKeyId pointing at another resource) into concrete form.
	spec = subwalk.WalkMappingNode(spec, transformutils.RewriteResourcePropertyRefs(rw))

	res := &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/sqs/queue"},
		Spec:     spec,
		Metadata: queueMetadata(r),
		// Preserve the abstract queue's linkSelector so a dead-letter
		// (queue -> queue) relationship still resolves against the concrete
		// resources by label.
		LinkSelector: r.Resource.LinkSelector,
	}

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			queueConcreteName(r.Name): res,
		},
	}, nil
}

// This carries the abstract queue's labels through to the concrete
// resource (so a handler's or parent queue's linkSelector can match it), stamps
// the framework's base annotations, and translates the celerity dead-letter
// annotation into the provider redrive annotation.
func queueMetadata(r *ResolvedQueue) *schema.Metadata {
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

func queueName(name string, fifo bool) string {
	if fifo && !strings.HasSuffix(name, fifoSuffix) {
		return name + fifoSuffix
	}
	return name
}

func queueConcreteName(name string) string {
	return fmt.Sprintf("%s_sqs_queue", name)
}
