package vpcaws

import (
	"sort"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// The security groups this VPC has to mint for the resources placed in it, in a
// stable order.
//
// A group per placed resource rather than one shared group: a path opened to one
// resource would otherwise reach everything else in the VPC. A placement that
// needs no group of its own contributes none, and a database needs two, so this
// is not one group per placement.
func placedSecurityGroupNames(
	target string,
	ctx transform.Context,
	placements []vpc.Placement,
) []string {
	names := []string{}
	for _, placement := range placements {
		names = append(names, securityGroupNamesFor(target, ctx, placement)...)
	}

	sort.Strings(names)

	return names
}

func securityGroupNamesFor(
	target string,
	ctx transform.Context,
	placement vpc.Placement,
) []string {
	switch placement.ResourceType {
	case "celerity/cache":
		return []string{sharedaws.VPCNamedSecurityGroup(
			placement.ResourceName, sharedaws.SecurityGroupRoleCache,
		)}
	case "celerity/sqlDatabase":
		names := []string{sharedaws.VPCNamedSecurityGroup(
			placement.ResourceName, sharedaws.SecurityGroupRoleDatabase,
		)}
		// The same rule the database's own emit applies, read from the one place
		// both can reach: a group minted for a proxy that was never emitted is
		// left with nothing in it, and a proxy emitted without one cannot be
		// reached at all.
		if shared.DatabasePoolerRequired(
			target, sharedaws.AuroraEnabled(ctx, placement.ResourceName),
		) {
			names = append(names, sharedaws.VPCNamedSecurityGroup(
				placement.ResourceName, sharedaws.SecurityGroupRoleProxy,
			))
		}

		return names
	}

	return nil
}
