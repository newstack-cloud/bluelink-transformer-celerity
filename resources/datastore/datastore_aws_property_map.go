package datastore

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/datastore.spec.id resolves to the concrete DynamoDB table's ARN.
			"spec.id": {"spec", "arn"},
		},
	}
}
