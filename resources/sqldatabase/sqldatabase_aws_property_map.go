package sqldatabase

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/sqlDatabase.spec.id resolves to the RDS instance ARN. (On the
			// Aurora path the primary is the db.serverless writer instance emitted
			// under the same concrete name, which also carries dbInstanceArn.)
			"spec.id": {"spec", "dbInstanceArn"},
			// spec.port resolves to the primary instance's connection endpoint port,
			// which does not branch on proxy-presence or Aurora.
			"spec.port": {"spec", "endpoint", "port"},
		},
		ValueRefs: map[string]*transformutils.ValueRefSpec{
			// spec.host / spec.readHost / spec.databaseName are emit-time derived
			// values because host depends on proxy-presence (proxy endpoint when a
			// proxy is emitted, else the instance/cluster writer endpoint) and
			// readHost depends on Aurora (cluster reader endpoint) vs standalone
			// (reader-instance endpoint); databaseName is a literal known at emit.
			// The emit builds each derived value keyed <instance>_host /
			// <instance>_read_host / <instance>_database_name.
			"spec.host":         {Suffix: "_host"},
			"spec.readHost":     {Suffix: "_read_host"},
			"spec.databaseName": {Suffix: "_database_name"},
		},
	}
}
