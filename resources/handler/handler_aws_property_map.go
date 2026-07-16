package handler

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			"spec.handlerName":          {"spec", "functionName"},
			"spec.memory":               {"spec", "memorySize"},
			"spec.timeout":              {"spec", "timeout"},
			"spec.environmentVariables": {"spec", "environment", "variables"},
			"spec.id":                   {"spec", "arn"},
		},
		ValueRefs: map[string]*transformutils.ValueRefSpec{
			"spec.codeLocation": {
				Suffix: "_code_location",
			},
			// The lambda runtime is often a different string than the Celerity
			// runtime, so a derived value retains a reference to the original.
			"spec.runtime": {
				Suffix: "_celerity_runtime",
			},

			"spec.handler": {
				Suffix: "_handler_id",
			},

			"spec.tracingEnabled": {
				Suffix: "_tracing_enabled",
			},
		},
	}
}
