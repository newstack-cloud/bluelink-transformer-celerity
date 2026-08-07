package sqldatabase

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity SQL Database.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/sqlDatabase",
		Label:            "Celerity SQL Database",
		PlainTextSummary: "A SQL database for a Celerity application.",
		FormattedSummary: "A SQL database for a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define a SQL database for a Celerity " +
			"application. Both the database infrastructure (provisioning, networking and " +
			"access control) and the database schema (migrations, validation and exports) " +
			"are managed as a single concern.",
		FormattedDescription: "The abstract resource type used to define a SQL database for a Celerity " +
			"application.\n\n" +
			"Both the database infrastructure (provisioning, networking and access control) " +
			"and the database schema (migrations, validation and exports) are managed as a " +
			"single concern.\n\n" +
			"See the " +
			"[`celerity/sqlDatabase` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-sql-database) " +
			"for more information.",
		Schema:  sqlDatabaseResourceSchema(),
		Resolve: resolveSQLDatabase,
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitSQLDatabase),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedSQLDatabase) string {
					return instanceResourceName(r.Name)
				},
			),
		},
	}
}
