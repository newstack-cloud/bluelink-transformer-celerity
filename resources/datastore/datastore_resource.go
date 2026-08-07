package datastore

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Datastore.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/datastore",
		Label:            "Celerity Datastore",
		PlainTextSummary: "A NoSQL data store for a Celerity application.",
		FormattedSummary: "A NoSQL data store for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a NoSQL data store (or table) for a " +
			"Celerity application. Handlers can be triggered by changes in a data store " +
			"through a consumer.",
		FormattedDescription: "The abstract resource type used to define a NoSQL data store (or table) for a " +
			"Celerity application.\n\n" +
			"Handlers can be triggered by changes in a data store through a " +
			"`celerity/consumer` resource.\n\n" +
			"See the " +
			"[`celerity/datastore` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-datastore) " +
			"for more information.",
		Schema:  datastoreResourceSchema(),
		Resolve: resolveDatastore,
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitDatastore),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedDatastore) string {
					return datastoreConcreteName(r.Name)
				},
			),
		},
	}
}
