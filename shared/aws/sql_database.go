package aws

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// AuroraEnabled reports whether a celerity/sqlDatabase is deployed on Aurora.
//
// Read from deploy config rather than from the abstract spec, so it is not a
// property of the resolved resource. Declared here rather than with the
// database's own emit because a VPC's emit needs it too, to decide which network
// identities the database will be reached through, and the database's emit
// already depends on the VPC's.
func AuroraEnabled(ctx transform.Context, databaseName string) bool {
	node, _ := ResolveDeployConfigNode(ctx, "aws.aurora", databaseName, "enabled")

	return core.BoolValue(node)
}
