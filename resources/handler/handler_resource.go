package handler

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity handler.
func Resource(deps *shared.Dependencies) *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	awsServerlessEmitter := newAWSServerlessEmitter(deps)

	return &transformerv1.AbstractResourceDefinition{
		Type:                 "celerity/handler",
		Label:                "Celerity Handler",
		PlainTextSummary:     "",
		FormattedSummary:     "",
		PlainTextDescription: "",
		FormattedDescription: "",
		Schema:               handlerResourceSchema(),
		Resolve:              resolveHandler,
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(
				awsServerlessEmitter.emit,
			),
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedHandler) string {
					return lambdaFuncResourceName(r.Name)
				},
			),
		},
	}
}
