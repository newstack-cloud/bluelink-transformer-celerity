package datastore

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity Datastore.
func Resource(
	implementations ...shared.TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	return shared.ApplyTargetImplementations(&transformerv1.AbstractResourceDefinition{
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
	}, implementations)
}
