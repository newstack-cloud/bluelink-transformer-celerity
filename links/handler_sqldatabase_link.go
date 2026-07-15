package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

func HandlerToSqlDatabaseLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/handler", "celerity/sqlDatabase",
		"Allows a handler to connect to a SQL database.",
		"Grants the handler network and credential access to the linked celerity/sqlDatabase at runtime.",
	)
}
