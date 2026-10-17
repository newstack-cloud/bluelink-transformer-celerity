package apiawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/api"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PropertyMap maps the abstract resource's properties onto the concrete ones.
func PropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/api.spec.baseUrl resolves to the primary concrete API
			// Gateway's endpoint.
			"spec.baseUrl": {"spec", "apiEndpoint"},
		},
		ValueRefs: map[string]*transformutils.ValueRefSpec{
			// celerity/api.spec.id resolves to a synthesised API Gateway ARN, held
			// as a derived value (see synthesizeIDValue) because the concrete
			// aws/apigatewayv2/api exposes no ARN attribute of its own.
			"spec.id": {Suffix: "_id_arn"},
		},
	}
}

// Rewriter resolves an API's references onto the concrete API this target emits,
// whose identity depends on which protocols the API declares.
func Rewriter(pm *transformutils.PropertyMap) transformutils.RewriterRegistration {
	return transformutils.RewriterFromPropertyMap(pm, func(r *api.ResolvedAPI) string {
		return primaryConcreteName(r, parseProtocols(r.Resource.Spec))
	})
}
