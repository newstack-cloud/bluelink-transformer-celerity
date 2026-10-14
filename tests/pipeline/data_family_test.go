//go:build unit

package pipeline

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// The field-heavy emission family: a managed VPC hosting an IAM-auth cache and
// a standalone Postgres database, with a handler linked to both, staged
// end-to-end. The vpc emit requires the "aws.region" deploy config, which the
// production engine passes as transformer config, so the test supplies it
// explicitly.
//
// This scenario guards two former upstream regressions: subwalk must preserve
// nil Items on whole-value substitution references (fixed in blueprint
// v0.51.1), and the array/map/object spec validators must accept a whole-value
// StringWithSubstitutions node such as
// "${resources.<vpc>.spec.privateSubnetIds}" instead of hard-failing on
// Items == nil (fixed after v0.51.1). Requires blueprint > v0.51.1.
func TestPipelineDataFamily(t *testing.T) {
	h := Setup(t)
	params := pipelineParams(h, ManifestPath(), awsRegionTransformerConfig(), nil)
	result := h.StageWithParams(t, "data_family.blueprint", params)

	types := transformedResourceTypes(result.Transformed)
	expectedTypes := []string{
		"aws/flex/vpc",
		"aws/elasticache/replicationGroup",
		"aws/elasticache/subnetGroup",
		"aws/elasticache/user",
		"aws/elasticache/userGroup",
		"aws/rds/dbInstance",
		"aws/rds/dbProxy",
		"aws/rds/dbSubnetGroup",
		"aws/lambda/function",
	}
	for _, expected := range expectedTypes {
		require.Contains(t, types, expected, "expected a %s resource in the transformed output")
	}

	// Every member of the family must survive concrete validation and be
	// planned for creation, including the subnet groups and proxy whose
	// subnet lists are whole-value substitution references to the VPC's
	// computed outputs.
	for _, name := range []string{
		"appNetwork_flex_vpc",
		"sessionsCache_elasticache_rg",
		"sessionsCache_elasticache_subnet_group",
		"sessionsCache_cache_iam_user",
		"sessionsCache_cache_user_group",
		"ordersDb_rds_instance",
		"ordersDb_rds_proxy",
		"ordersDb_rds_subnet_group",
		"dataHandler_lambda_func",
	} {
		requirePlanned(t, result, name)
	}

	// iam authMode swaps the cache's auth-token secret for an ElastiCache
	// user/userGroup pair, and the standalone (non-Aurora) database self-manages
	// its master password via RDS (manageMasterUserPassword). Neither emits a
	// secret.
	//
	// The one secret here is the database's application user, which exists for a
	// different reason: the application authenticates as its own role rather than
	// the master, whose credentials the proxy holds to reach the database and
	// which would give any handler the whole instance.
	require.Equal(t, []string{"ordersDb_sqldb_app_user_secret"},
		types["aws/secretsmanager/secret"],
		"the only secret should be the database's application user")
	requirePlanned(t, result, "ordersDb_sqldb_app_user_secret")
	// dbCluster is Aurora-only (aws.aurora.<name>.enabled deploy config); the
	// default standalone path emits dbInstance + dbProxy instead.
	require.NotContains(t, types, "aws/rds/dbCluster",
		"the standalone (non-Aurora) path must not emit an RDS cluster")
}

// The VPC mints a group per in-VPC resource and each resource references its own,
// with both sides deriving the name from the same helper.
//
// Referencing the VPC's whole securityGroupIds list instead is what a real
// deployment failed on: a link opening a path to the cache could not tell which
// of the listed groups was the cache's, and the provider refuses to pair against
// a group it did not mint for that resource, because a shared group would open
// the path to everything else in it.
func TestPipelineDataFamilyNamedSecurityGroups(t *testing.T) {
	h := Setup(t)
	params := pipelineParams(h, ManifestPath(), awsRegionTransformerConfig(), nil)
	result := h.StageWithParams(t, "data_family.blueprint", params)

	vpc := result.Transformed.Resources.Values["appNetwork_flex_vpc"]
	require.NotNil(t, vpc)

	minted := map[string]bool{}
	for _, item := range vpc.Spec.Fields["securityGroups"].Items {
		minted[core.StringValue(item)] = true
	}

	// A group for the cache, one for the database instance and one for the proxy
	// in front of it. The proxy reaches the instance by pairing its own group with
	// the instance's, so they cannot be the same group.
	for _, name := range []string{
		"sessionsCache-cache",
		"ordersDb-db",
		"ordersDb-proxy",
	} {
		require.True(t, minted[name], "the VPC must mint a %q security group", name)
	}

	// Each consumer references the group minted for it, by name.
	for _, tc := range []struct {
		resource string
		field    string
		group    string
	}{
		{"sessionsCache_elasticache_rg", "securityGroupIds", "sessionsCache-cache"},
		{"ordersDb_rds_instance", "vpcSecurityGroups", "ordersDb-db"},
		{"ordersDb_rds_proxy", "vpcSecurityGroupIds", "ordersDb-proxy"},
	} {
		resource := result.Transformed.Resources.Values[tc.resource]
		require.NotNil(t, resource, tc.resource)

		items := resource.Spec.Fields[tc.field].Items
		require.Len(t, items, 1,
			"%s.%s references exactly the one group minted for it", tc.resource, tc.field)

		rendered := renderSubstitutions(items[0])
		require.Contains(t, rendered, tc.group,
			"%s.%s must reference the %q group", tc.resource, tc.field, tc.group)
		require.Contains(t, rendered, "securityGroupIdsByName",
			"%s.%s must resolve a named group, not the VPC's whole list", tc.resource, tc.field)

		// Every name referenced has to be one the VPC actually minted, or it
		// resolves to nothing and the link fails at deploy time.
		require.True(t, minted[tc.group],
			"%s references a group the VPC does not mint", tc.resource)
	}

	// The proxy and the instance must not share a group.
	proxyGroup := renderSubstitutions(
		result.Transformed.Resources.Values["ordersDb_rds_proxy"].Spec.Fields["vpcSecurityGroupIds"].Items[0])
	instanceGroup := renderSubstitutions(
		result.Transformed.Resources.Values["ordersDb_rds_instance"].Spec.Fields["vpcSecurityGroups"].Items[0])
	require.NotEqual(t, proxyGroup, instanceGroup,
		"the proxy pairs its group with the instance's, so they must be distinct")
}
