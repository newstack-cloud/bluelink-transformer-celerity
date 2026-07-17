package links

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// VPCToCacheLink declares the celerity/vpc -> celerity/cache relationship.
//
// On aws-serverless the cache is placed within the VPC's private subnets (caches
// require VPC placement). The placement is driven entirely by the resolved VPC on
// the cache resource, so the link carries no author-facing annotations.
func VPCToCacheLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/vpc",
		ResourceTypeB:    "celerity/cache",
		PlainTextSummary: "Places a cache within a VPC.",
		FormattedSummary: "Places a `celerity/cache` within a `celerity/vpc`.",
		PlainTextDescription: "Places the cache within the VPC's private subnets. Caches require VPC " +
			"placement, so a cache belongs to at most one VPC.",
		FormattedDescription: "Places the cache within the `celerity/vpc`'s private subnets. Caches " +
			"require VPC placement, so a cache belongs to at most one `celerity/vpc`.",
		// A cache is placed within at most one VPC.
		CardinalityB: provider.LinkCardinality{Min: 0, Max: 1},
	}
}
