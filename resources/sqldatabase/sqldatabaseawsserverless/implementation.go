// Package sqldatabaseawsserverless is the aws-serverless implementation of
// celerity/sqlDatabase: the RDS resources from sqldatabaseaws, plus the
// maintenance runner that lets the CLI run privileged SQL against a database
// in private subnets.
//
// The runner is specific to this target rather than to AWS. A database has to
// be reached from inside its VPC to create the application role, and on
// aws-serverless the thing that reaches it is a function.
package sqldatabaseawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase/sqldatabaseaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Implementation is what the registry composes with sqldatabase.Resource.
//
// The property map and rewriter are sqldatabaseaws's: the runner is not
// addressable from a blueprint, so it adds nothing a reference can point at.
func Implementation() shared.TargetImplementation {
	propertyMap := sqldatabaseaws.PropertyMap()

	return shared.TargetImplementation{
		Target:      shared.AWSServerless,
		Emitter:     transformutils.TypedEmitter(Emit),
		PropertyMap: propertyMap,
		Rewriter: transformutils.RewriterFromPropertyMap(
			&propertyMap,
			func(r *sqldatabase.ResolvedSQLDatabase) string {
				return sqldatabaseaws.InstanceResourceName(r.Name)
			},
		),
	}
}
