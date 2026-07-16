package bucket

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
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
