package configaws

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/config"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PropertyMap maps the abstract resource's properties onto the concrete ones.
func PropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		ValueRefs: map[string]*transformutils.ValueRefSpec{
			"spec.id": {
				Suffix: "_id",
			},
		},
	}
}

// Rewriter resolves a config store's references through the derived value the
// emit produces, which holds the concrete store's id or an external literal.
func Rewriter(pm *transformutils.PropertyMap) transformutils.RewriterRegistration {
	return transformutils.RewriterFromPropertyMap(pm, func(r *config.ResolvedConfig) string {
		return concreteName(r.Name)
	})
}
