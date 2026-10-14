package vpc

import (
	"context"
	"sort"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedVPC carries the abstract VPC through to the emit phase. Labels and
// linkSelector are preserved verbatim so placement links (flex/vpc -> handler,
// cache, sqlDatabase) resolve against the concrete resources.
type ResolvedVPC struct {
	Name     string
	Resource *schema.Resource
	// SecurityGroupNames are the groups this VPC provisions for the resources placed
	// in it, in a stable order. Each placed resource that workloads reach over
	// the network needs a group of its own so a path opened to one does not also
	// reach the others sharing it.
	SecurityGroupNames []string
}

func (v *ResolvedVPC) ResourceName() string {
	return v.Name
}

func (v *ResolvedVPC) ResourceType() string {
	return "celerity/vpc"
}

func resolveVPC(
	_ context.Context,
	run *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedVPC{
		Name:               name,
		Resource:           resource,
		SecurityGroupNames: placedSecurityGroupNames(run, name, linkGraph),
	}, nil
}

// placedSecurityGroupNames collects the group names this VPC has to mint, from
// the resources placed in it.
//
// Sorted so the emitted list is stable: an order that varied with graph
// traversal would show as a change on every stage.
func placedSecurityGroupNames(
	run *transformutils.Run,
	vpcName string,
	linkGraph linktypes.DeclaredLinkGraph,
) []string {
	if linkGraph == nil {
		return nil
	}

	names := []string{}
	for _, edge := range linkGraph.EdgesFrom(vpcName) {
		if edge == nil {
			continue
		}
		switch edge.TargetType {
		case "celerity/cache":
			names = append(names, shared.VPCNamedSecurityGroup(
				edge.Target, shared.SecurityGroupRoleCache,
			))
		case "celerity/sqlDatabase":
			names = append(names, shared.VPCNamedSecurityGroup(
				edge.Target, shared.SecurityGroupRoleDatabase,
			))
			// Aurora pools connections itself, so no proxy is emitted for it and
			// minting a group for one would leave an empty group behind.
			if !auroraEnabled(run, edge.Target) {
				names = append(names, shared.VPCNamedSecurityGroup(
					edge.Target, shared.SecurityGroupRoleProxy,
				))
			}
		}
	}

	sort.Strings(names)
	return names
}

// auroraEnabled reads the same deploy config key the sqlDatabase transform reads,
// rather than importing it: sqlDatabase already imports this package, so the
// dependency cannot run the other way.
func auroraEnabled(run *transformutils.Run, databaseName string) bool {
	if run == nil {
		return false
	}
	node, _ := sharedaws.ResolveDeployConfigNode(
		run.TransformContext, "aws.aurora", databaseName, "enabled",
	)
	return core.BoolValue(node)
}
