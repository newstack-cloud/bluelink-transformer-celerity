package topic

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/topic.spec.id resolves to the concrete SNS topic's ARN.
			"spec.id": {"spec", "topicArn"},
		},
	}
}
