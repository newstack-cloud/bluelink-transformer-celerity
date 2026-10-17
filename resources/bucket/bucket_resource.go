package bucket

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity Bucket.
func Resource(
	implementations ...shared.TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	return shared.ApplyTargetImplementations(&transformerv1.AbstractResourceDefinition{
		Type:             "celerity/bucket",
		Label:            "Celerity Bucket",
		PlainTextSummary: "An object storage bucket for a Celerity application.",
		FormattedSummary: "An object storage bucket for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define an object storage bucket that stores " +
			"files and other data for a Celerity application. Handlers can be triggered by " +
			"events emitted for objects in a bucket through a consumer.",
		FormattedDescription: "The abstract resource type used to define an object storage bucket that stores " +
			"files and other data for a Celerity application.\n\n" +
			"Handlers can be triggered by events emitted for objects in a bucket through a " +
			"`celerity/consumer` resource.\n\n" +
			"See the " +
			"[`celerity/bucket` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-bucket) " +
			"for more information.",
		Schema:  bucketResourceSchema(),
		Resolve: resolveBucket,
	}, implementations)
}
