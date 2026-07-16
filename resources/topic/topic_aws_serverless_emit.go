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

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			topicConcreteName(r.Name): res,
		},
	}, nil
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
