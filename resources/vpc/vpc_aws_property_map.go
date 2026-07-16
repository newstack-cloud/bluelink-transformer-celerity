package vpc

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/vpc.spec.id resolves to the synthetic flex VPC's vpcId output.
			"spec.id": {"spec", "vpcId"},
		},
	}
}
