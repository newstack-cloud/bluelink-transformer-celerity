package topic

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Topic.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
		Type:    "celerity/topic",
		Label:   "Celerity Topic",
		Schema:  topicResourceSchema(),
		Resolve: resolveTopic,
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitTopic),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedTopic) string {
					return topicConcreteName(r.Name)
				},
			),
		},
	}
}
