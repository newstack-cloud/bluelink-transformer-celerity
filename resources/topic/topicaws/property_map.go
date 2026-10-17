package topicaws

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PropertyMap maps the abstract topic's properties onto the concrete SNS topic.
func PropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/topic.spec.id resolves to the concrete SNS topic's ARN.
			"spec.id": {"spec", "topicArn"},
		},
	}
}

// Rewriter resolves a celerity/topic's references against what AWS emitted for
// it: the concrete topic's properties normally, and the literals read from the
// identifier the author declared for an external topic, which emits no concrete
// resource for a reference to point at.
func Rewriter(pm *transformutils.PropertyMap) transformutils.RewriterRegistration {
	return transformutils.TypedRewriterWithCapabilities(
		pm,
		func(r *topic.ResolvedTopic) []transformutils.ResourcePropertyRewriter {
			identifier := awsexternal.ParseOrNil(r.External)
			if identifier == nil {
				// Managed, or an identifier the emit reports as malformed.
				return []transformutils.ResourcePropertyRewriter{
					pm.Rewriter(r.Name, topicConcreteName(r.Name)),
				}
			}

			return []transformutils.ResourcePropertyRewriter{
				shared.ExternalLiteralRewriter(r.Name, map[string]string{
					"spec.id": identifier.Literal,
				}),
			}
		},
	)
}
