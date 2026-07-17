package links

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// VPCToSqlDatabaseLink declares the celerity/vpc -> celerity/sqlDatabase
// relationship.
//
// On aws-serverless the database (and its subnet group and RDS Proxy) is placed
// within the VPC's private subnets; SQL databases require VPC placement. The
// placement is driven entirely by the resolved VPC on the sqlDatabase resource, so
// the link carries no author-facing annotations.
func VPCToSqlDatabaseLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/vpc",
		ResourceTypeB:    "celerity/sqlDatabase",
		PlainTextSummary: "Places a SQL database within a VPC.",
		FormattedSummary: "Places a `celerity/sqlDatabase` within a `celerity/vpc`.",
		PlainTextDescription: "Places the SQL database within the VPC's private subnets. SQL databases " +
			"require VPC placement, so a database belongs to at most one VPC.",
		FormattedDescription: "Places the SQL database within the `celerity/vpc`'s private subnets. SQL " +
			"databases require VPC placement, so a database belongs to at most one `celerity/vpc`.",
		// A SQL database is placed within at most one VPC.
		CardinalityB: provider.LinkCardinality{Min: 0, Max: 1},
	}
}
