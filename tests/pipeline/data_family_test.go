//go:build unit

package pipeline

import (
	"testing"

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
		"aws/rds/dbProxyTargetGroup",
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
		"ordersDb_rds_proxy_target_group",
		"ordersDb_rds_subnet_group",
		"dataHandler_lambda_func",
	} {
		requirePlanned(t, result, name)
	}

	// iam authMode swaps the cache's auth-token secret for an ElastiCache
	// user/userGroup pair, and the standalone (non-Aurora) database self-manages
	// its master password via RDS (manageMasterUserPassword), so no
	// secretsmanager secret is emitted anywhere in this fixture.
	require.NotContains(t, types, "aws/secretsmanager/secret",
		"iam-auth cache + RDS-managed password must not emit a secretsmanager secret")
	// dbCluster is Aurora-only (aws.aurora.<name>.enabled deploy config); the
	// default standalone path emits dbInstance + dbProxy instead.
	require.NotContains(t, types, "aws/rds/dbCluster",
		"the standalone (non-Aurora) path must not emit an RDS cluster")
}
