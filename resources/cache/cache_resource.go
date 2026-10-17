package cache

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity Cache.
func Resource(
	implementations ...shared.TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	return shared.ApplyTargetImplementations(&transformerv1.AbstractResourceDefinition{
		Type:             "celerity/cache",
		Label:            "Celerity Cache",
		PlainTextSummary: "An in-memory cache for a Celerity application.",
		FormattedSummary: "An in-memory cache for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define an in-memory cache for a Celerity " +
			"application. Redis OSS-compatible in-memory databases are supported, " +
			"abstracting the difference between single-instance and cluster topologies.",
		FormattedDescription: "The abstract resource type used to define an in-memory cache for a Celerity " +
			"application.\n\n" +
			"Redis OSS-compatible in-memory databases are supported, abstracting the " +
			"difference between single-instance and cluster topologies.\n\n" +
			"See the " +
			"[`celerity/cache` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-cache) " +
			"for more information.",
		Schema:  cacheResourceSchema(),
		Resolve: resolveCache,
	}, implementations)
}
