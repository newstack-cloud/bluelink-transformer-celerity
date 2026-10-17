package queueaws

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PropertyMap maps the abstract queue's paths onto the emitted SQS queue's.
func PropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/queue.spec.id resolves to the concrete SQS queue's ARN.
			"spec.id": {"spec", "arn"},
		},
	}
}

// Rewriter resolves references to a celerity/queue against what AWS emitted for
// it. An external queue emits no concrete resource, so its references resolve
// to literals read from the identifier the author declared rather than to a
// resource property.
func Rewriter(propertyMap *transformutils.PropertyMap) transformutils.RewriterRegistration {
	return transformutils.TypedRewriterWithCapabilities(
		propertyMap,
		func(r *queue.ResolvedQueue) []transformutils.ResourcePropertyRewriter {
			identifier := awsexternal.ParseOrNil(r.External)
			if identifier == nil {
				// Managed, or an identifier the emit reports as malformed.
				return []transformutils.ResourcePropertyRewriter{
					propertyMap.Rewriter(r.Name, ConcreteResourceName(r.Name)),
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
