package handler

import (
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Stamps the aws/lambda/function::aws/rds/dbProxy (and ::aws/rds/dbCluster) link's
// authMode annotation for each linked sqlDatabase that uses IAM database
// authentication. The provider link grants the function's execution role
// rds-db:connect only when the function carries
// aws.lambda.rds.<target>.authMode=iam, where <target> is the concrete resource
// the function links to (the RDS Proxy for standalone RDS, or the Aurora cluster).
// Without this the handler would have no RDS auth path in iam mode.
func stampSQLDatabaseAnnotations(r *ResolvedHandler, run *transformutils.Run, lambda *schema.Resource) {
	for _, db := range r.SQLDatabases {
		if db == nil || db.Resource == nil {
			continue
		}
		target, mode := sqldatabase.AuthTargetForHandler(run, db.Name, db.Resource.Spec)
		if mode != "iam" {
			continue
		}
		setStringAnnotation(lambda.Metadata, fmt.Sprintf("aws.lambda.rds.%s.authMode", target), "iam")
	}
}
