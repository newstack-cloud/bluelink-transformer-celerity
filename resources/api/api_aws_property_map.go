package api

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
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
