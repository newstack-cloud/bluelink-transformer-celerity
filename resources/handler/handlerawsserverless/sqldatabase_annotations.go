package handlerawsserverless

import (
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase/sqldatabaseaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Stamps the authMode annotation the aws/lambda/function::aws/rds/* links read,
// for each linked sqlDatabase using IAM database authentication. The provider
// link grants the function's execution role rds-db:connect only when the
// function carries aws.lambda.rds.<target>.authMode=iam, where <target> is the
// concrete resource it connects through. Which resource that is belongs to the
// database's emit, so it is asked rather than derived here. Without this the
// handler would have no RDS auth path in iam mode.
func stampSQLDatabaseAnnotations(r *handler.ResolvedHandler, run *transformutils.Run, lambda *schema.Resource) {
	for _, db := range r.SQLDatabases {
		if db == nil || db.Resource == nil {
			continue
		}
		target, mode := sqldatabaseaws.AuthTargetForHandler(
			shared.AWSServerless, run, db.Name, db.Resource.Spec,
		)
		if mode != "iam" {
			continue
		}
		setStringAnnotation(lambda.Metadata, fmt.Sprintf("aws.lambda.rds.%s.authMode", target), "iam")
	}
}
