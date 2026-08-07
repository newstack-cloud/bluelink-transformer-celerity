package api

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity API.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/api",
		Label:            "Celerity API",
		PlainTextSummary: "An HTTP or WebSocket API for a Celerity application.",
		FormattedSummary: "An HTTP or WebSocket API for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define an HTTP API or a WebSocket API. An " +
			"API can be deployed to a serverless API gateway, a containerised environment " +
			"or a custom server, routing incoming requests to the handlers that are linked " +
			"to it.",
		FormattedDescription: "The abstract resource type used to define an HTTP API or a WebSocket API.\n\n" +
			"An API can be deployed to a serverless API gateway, a containerised " +
			"environment or a custom server, routing incoming requests to the " +
			"`celerity/handler` resources that are linked to it.\n\n" +
			"See the " +
			"[`celerity/api` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-api) " +
			"for more information.",
		Schema:  apiResourceSchema(),
		Resolve: resolveAPI,
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitAPI),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedAPI) string {
					return primaryConcreteName(r, parseProtocols(r.Resource.Spec))
				},
			),
		},
	}
}
