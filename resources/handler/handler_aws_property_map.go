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
			// The Celerity runtime will be referenceable via a derived value
			// with the suffix "_celerity_runtime".
			// As the lambda runtime will often be a different string value
			// than the Celerity runtime, to retain references to the original
			// value, we need to introduce a derived value.
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
