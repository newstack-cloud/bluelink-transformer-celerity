package shared

import "fmt"

// Roles an in-VPC resource plays, used to name the security group the VPC mints
// for it. A database and the proxy in front of it are both placed in the VPC and
// need groups of their own, so the resource name alone does not identify one.
const (
	// SecurityGroupRoleCache is a cache's own group.
	SecurityGroupRoleCache = "cache"
	// SecurityGroupRoleDatabase is a database instance's own group.
	SecurityGroupRoleDatabase = "db"
	// SecurityGroupRoleProxy is the group for the proxy fronting a database.
	SecurityGroupRoleProxy = "proxy"
)

// VPCNamedSecurityGroup is the name a flex VPC mints a security group under for
// an in-VPC resource, and the key that resource reads back from
// securityGroupIdsByName.
//
// Both sides derive the name from here so they cannot drift: a consumer
// referencing a name the VPC never minted resolves to nothing, and the links
// that open paths to it fail at deploy time with the resource reported as having
// no security group of its own.
//
// A group per resource rather than one shared group is what keeps a path opened
// to one resource from reaching everything else in the VPC. The provider refuses
// to pair against a group it did not mint for exactly that reason.
func VPCNamedSecurityGroup(abstractName, role string) string {
	return fmt.Sprintf("%s-%s", abstractName, role)
}

// VPCSecurityGroupIDRef is the substitution an in-VPC resource uses to reference
// the group the VPC minted for it.
func VPCSecurityGroupIDRef(vpcConcreteName, abstractName, role string) string {
	return fmt.Sprintf(
		"${resources.%s.spec.securityGroupIdsByName[\"%s\"]}",
		vpcConcreteName,
		VPCNamedSecurityGroup(abstractName, role),
	)
}
