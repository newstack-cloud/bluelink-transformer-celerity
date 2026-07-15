package queue

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/queue.spec.id resolves to the concrete SQS queue's ARN.
			"spec.id": {"spec", "arn"},
		},
	}
}
