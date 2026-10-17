package bucketaws

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PropertyMap maps the abstract resource's properties onto the concrete ones.
func PropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/bucket.spec.id resolves to the concrete S3 bucket's ARN.
			"spec.id": {"spec", "arn"},
			// celerity/bucket.spec.name resolves to the concrete bucket name, so
			// references such as an access-logging target bucket rewrite correctly.
			"spec.name": {"spec", "bucketName"},
		},
	}
}

// Rewriter resolves a celerity/bucket's references against what AWS emitted for
// it: the concrete bucket's properties normally, and the literals read from the
// identifier the author declared for an external bucket, which emits no concrete
// resource for a reference to point at.
func Rewriter(pm *transformutils.PropertyMap) transformutils.RewriterRegistration {
	return transformutils.TypedRewriterWithCapabilities(
		pm,
		func(r *bucket.ResolvedBucket) []transformutils.ResourcePropertyRewriter {
			identifier := awsexternal.ParseOrNil(r.External)
			if identifier == nil {
				// Managed, or an identifier the emit reports as malformed.
				return []transformutils.ResourcePropertyRewriter{
					pm.Rewriter(r.Name, bucketConcreteName(r.Name)),
				}
			}

			return []transformutils.ResourcePropertyRewriter{
				shared.ExternalLiteralRewriter(r.Name, map[string]string{
					"spec.id":   identifier.Literal,
					"spec.name": identifier.ResourceName(),
				}),
			}
		},
	)
}
