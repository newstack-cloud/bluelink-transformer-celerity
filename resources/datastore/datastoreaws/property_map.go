package datastoreaws

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PropertyMap maps the abstract resource's properties onto the concrete ones.
func PropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/datastore.spec.id resolves to the concrete DynamoDB table's ARN.
			"spec.id": {"spec", "arn"},
			// celerity/datastore.spec.name resolves to the concrete table name so
			// references to the store name rewrite to the emitted table.
			"spec.name": {"spec", "tableName"},
		},
	}
}

// Rewriter resolves a celerity/datastore's references against what AWS emitted
// for it. This handles the concrete table's properties normally, and the literals read from
// the identifier the author declared for an external data store, which does not emit a
// concrete resource for a reference to point at.
func Rewriter(pm *transformutils.PropertyMap) transformutils.RewriterRegistration {
	return transformutils.TypedRewriterWithCapabilities(
		pm,
		func(r *datastore.ResolvedDatastore) []transformutils.ResourcePropertyRewriter {
			identifier := awsexternal.ParseOrNil(r.External)
			if identifier == nil {
				// Managed, or an identifier the emit reports as malformed.
				return []transformutils.ResourcePropertyRewriter{
					pm.Rewriter(r.Name, datastoreConcreteName(r.Name)),
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
