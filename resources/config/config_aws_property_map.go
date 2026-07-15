package config

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		ValueRefs: map[string]*transformutils.ValueRefSpec{
			"spec.id": {
				Suffix: "_id",
			},
		},
	}
}
