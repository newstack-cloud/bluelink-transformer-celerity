//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type CacheTransformTestSuite struct {
	suite.Suite
}

func TestCacheTransformTestSuite(t *testing.T) {
	suite.Run(t, new(CacheTransformTestSuite))
}

func (s *CacheTransformTestSuite) Test_cache_in_a_managed_vpc_emits_rg_and_subnet_group() {
	out := s.transformCacheWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("sessions"),
		),
		"standard",
		map[string]string{"app": "orders"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	rg := resources["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.Equal("aws/elasticache/replicationGroup", rg.Type.Value)
	s.Equal("sessions", core.StringValue(rg.Spec.Fields["replicationGroupId"]))
	// Engine is Valkey (Redis OSS-compatible) with the 8.2 default injected.
	s.Equal("valkey", core.StringValue(rg.Spec.Fields["engine"]))
	s.Equal("8.2", core.StringValue(rg.Spec.Fields["engineVersion"]))
	s.Equal(1, core.IntValue(rg.Spec.Fields["numNodeGroups"]), "single-instance cache is one node group")
	s.Equal(0, core.IntValue(rg.Spec.Fields["replicasPerNodeGroup"]), "no replicas by default")
	s.Equal(6379, core.IntValue(rg.Spec.Fields["port"]))
	s.True(core.BoolValue(rg.Spec.Fields["transitEncryptionEnabled"]))
	s.Equal("sessions-cache-subnets", core.StringValue(rg.Spec.Fields["cacheSubnetGroupName"]))
	s.Require().NotNil(rg.Spec.Fields["securityGroupIds"], "security groups referenced from the VPC")
	// Labels preserved for handler links.
	s.Equal("orders", rg.Metadata.Labels.Values["app"])

	sng := resources["myCache_elasticache_subnet_group"]
	s.Require().NotNil(sng)
	s.Equal("aws/elasticache/subnetGroup", sng.Type.Value)
	s.Equal("sessions-cache-subnets", core.StringValue(sng.Spec.Fields["cacheSubnetGroupName"]))
	s.Require().NotNil(sng.Spec.Fields["subnetIds"], "subnet ids referenced from the VPC's private subnets")

	// Password auth (default) is fully wired: an AUTH-token secret is emitted and
	// the replication group selects it. No auth-deferred warning remains.
	secret := resources["myCache_cache_auth_secret"]
	s.Require().NotNil(secret, "password mode emits an auth-token secret")
	s.Equal("aws/secretsmanager/secret", secret.Type.Value)
	gen := secret.Spec.Fields["generateSecretString"]
	s.Require().NotNil(gen, "the token is randomly generated")
	s.Equal(32, core.IntValue(gen.Fields["passwordLength"]))
	s.NotEmpty(core.StringValue(gen.Fields["excludeCharacters"]), "Redis-forbidden characters excluded")
	// The secret carries the RG-selection label plus the cache's own labels (so a
	// handler that links to the cache also links to the secret).
	s.Equal("sessions", secret.Metadata.Labels.Values["celerity.cache.auth"])
	s.Equal("orders", secret.Metadata.Labels.Values["app"])
	// The RG activates the replicationGroup::secret link by selecting the secret.
	s.Require().NotNil(rg.LinkSelector)
	s.Require().NotNil(rg.LinkSelector.ByLabel)
	s.Equal("sessions", rg.LinkSelector.ByLabel.Values["celerity.cache.auth"])

	s.False(hasWarningContaining(out.Diagnostics, "authMode"), "no auth-deferred warning in password mode")
}

func (s *CacheTransformTestSuite) Test_iam_auth_mode_defers_with_a_scoped_warning() {
	out := s.transformCacheWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("sessions"),
			"authMode", core.MappingNodeFromString("iam"),
		),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	// No secret in iam mode; the RG carries no auth selector.
	s.NotContains(resources, "myCache_cache_auth_secret")
	rg := resources["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.True(core.BoolValue(rg.Spec.Fields["transitEncryptionEnabled"]))
	s.True(hasWarningContaining(out.Diagnostics, "authMode"), "iam mode raises a scoped deferral warning")
}

func (s *CacheTransformTestSuite) Test_cluster_mode_uses_multiple_node_groups() {
	out := s.transformCacheWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("sessions"),
			"clusterMode", core.MappingNodeFromBool(true),
		),
		"standard",
		nil,
	)
	rg := out.TransformedBlueprint.Resources.Values["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	// Cluster mode defaults to 3 shards, 0 replicas per shard.
	s.Equal(3, core.IntValue(rg.Spec.Fields["numNodeGroups"]))
	s.Equal(0, core.IntValue(rg.Spec.Fields["replicasPerNodeGroup"]))
}

func (s *CacheTransformTestSuite) Test_replica_and_shard_deploy_config_overrides_are_honoured() {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myCache": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/cache"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("sessions"),
						"clusterMode", core.MappingNodeFromBool(true),
					),
				},
				"myVpc": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("app-net"),
						"preset", core.MappingNodeFromString("standard"),
					),
				},
			},
		},
	}
	ctx := &fakeTransformContext{
		configVars: map[string]*core.ScalarValue{
			"aws.elasticache.sessions.numShards":   core.ScalarFromInt(5),
			"aws.elasticache.sessions.numReplicas": core.ScalarFromInt(2),
		},
		contextVars: map[string]*core.ScalarValue{
			core.ValidationContextVariableName: core.ScalarFromBool(true),
			"deployTarget":                     core.ScalarFromString(shared.AWSServerless),
		},
	}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          vpcCacheLinkGraph{vpc: "myVpc", cache: "myCache"},
			TransformerContext: ctx,
		},
	)
	s.Require().NoError(err)
	rg := out.TransformedBlueprint.Resources.Values["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.Equal(5, core.IntValue(rg.Spec.Fields["numNodeGroups"]), "numShards override applied")
	s.Equal(2, core.IntValue(rg.Spec.Fields["replicasPerNodeGroup"]), "numReplicas override applied")
}

func (s *CacheTransformTestSuite) Test_engine_defaults_to_valkey_and_honours_explicit_version() {
	out := s.transformCacheWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("sessions"),
			"engineVersion", core.MappingNodeFromString("8.0"),
		),
		"standard",
		nil,
	)
	rg := out.TransformedBlueprint.Resources.Values["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.Equal("valkey", core.StringValue(rg.Spec.Fields["engine"]))
	s.Equal("8.0", core.StringValue(rg.Spec.Fields["engineVersion"]), "explicit engineVersion is honoured")
}

func (s *CacheTransformTestSuite) Test_outputs_resolve_for_non_clustered_cache() {
	vals := s.transformCacheOutputs(false)

	// id and port are simple renames onto the replication group.
	s.assertResourceProp(vals["id"], "myCache_elasticache_rg", "spec", "id")
	s.assertResourceProp(vals["port"], "myCache_elasticache_rg", "spec", "port")
	// host is an emit-time derived value: the ValueRef resolves to it, and it
	// points at the primary endpoint for a non-clustered cache.
	s.Equal("myCache_elasticache_rg_host", valueRefName(vals["host"].Value))
	s.assertResourceProp(
		vals["myCache_elasticache_rg_host"],
		"myCache_elasticache_rg", "spec", "primaryEndPoint", "address",
	)
}

func (s *CacheTransformTestSuite) Test_outputs_resolve_for_clustered_cache() {
	vals := s.transformCacheOutputs(true)

	s.Equal("myCache_elasticache_rg_host", valueRefName(vals["host"].Value))
	// Cluster mode uses the configuration endpoint.
	s.assertResourceProp(
		vals["myCache_elasticache_rg_host"],
		"myCache_elasticache_rg", "spec", "configurationEndPoint", "address",
	)
}

func (s *CacheTransformTestSuite) Test_vpc_preset_without_private_subnets_is_rejected() {
	out := s.transformCacheWithVPC(
		core.MappingNodeFields("name", core.MappingNodeFromString("sessions")),
		"public", // no private subnets
		nil,
	)

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelError && strings.Contains(d.Message, "private subnets") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic about missing private subnets")
	// No replication group is emitted for an unsuitable placement.
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myCache_elasticache_rg")
}

func (s *CacheTransformTestSuite) Test_cache_without_a_vpc_warns_and_omits_placement() {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myCache": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/cache"},
					Spec: core.MappingNodeFields("name", core.MappingNodeFromString("sessions")),
				},
			},
		},
	}
	out := s.runTransform(bp, emptyLinkGraph{})

	s.True(hasWarningContaining(out.Diagnostics, "VPC placement"), "expected a VPC-placement warning")
	rg := out.TransformedBlueprint.Resources.Values["myCache_elasticache_rg"]
	s.Require().NotNil(rg, "the replication group is still emitted")
	s.Nil(rg.Spec.Fields["cacheSubnetGroupName"], "no subnet group without a VPC")
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myCache_elasticache_subnet_group")
}

// --- helpers ---

func (s *CacheTransformTestSuite) transformCacheWithVPC(
	cacheSpec *core.MappingNode,
	vpcPreset string,
	cacheLabels map[string]string,
) *transform.SpecTransformerTransformOutput {
	cacheRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/cache"},
		Spec: cacheSpec,
	}
	if cacheLabels != nil {
		cacheRes.Metadata = &schema.Metadata{Labels: &schema.StringMap{Values: cacheLabels}}
	}
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myCache": cacheRes,
				"myVpc": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("app-net"),
						"preset", core.MappingNodeFromString(vpcPreset),
					),
				},
			},
		},
	}
	return s.runTransform(bp, vpcCacheLinkGraph{vpc: "myVpc", cache: "myCache"})
}

// transformCacheOutputs transforms a cache with top-level blueprint values that
// reference its host/port/id outputs, returning the resolved value map.
func (s *CacheTransformTestSuite) transformCacheOutputs(clusterMode bool) map[string]*schema.Value {
	cacheSpec := core.MappingNodeFields("name", core.MappingNodeFromString("sessions"))
	if clusterMode {
		cacheSpec.Fields["clusterMode"] = core.MappingNodeFromBool(true)
	}
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myCache": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/cache"},
					Spec: cacheSpec,
				},
				"myVpc": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("app-net"),
						"preset", core.MappingNodeFromString("standard"),
					),
				},
			},
		},
		Values: &schema.ValueMap{
			Values: map[string]*schema.Value{
				"host": valueRefTo("${myCache.spec.host}"),
				"port": valueRefTo("${myCache.spec.port}"),
				"id":   valueRefTo("${myCache.spec.id}"),
			},
		},
	}
	out := s.runTransform(bp, vpcCacheLinkGraph{vpc: "myVpc", cache: "myCache"})
	s.Require().NotNil(out.TransformedBlueprint.Values)
	return out.TransformedBlueprint.Values.Values
}

// assertResourceProp checks a value resolves to a resource-property reference on
// resName with the given field path.
func (s *CacheTransformTestSuite) assertResourceProp(
	v *schema.Value,
	resName string,
	fields ...string,
) {
	s.Require().NotNil(v)
	s.Require().NotNil(v.Value)
	s.Require().NotNil(v.Value.StringWithSubstitutions)
	parts := v.Value.StringWithSubstitutions.Values
	s.Require().Len(parts, 1)
	prop := parts[0].SubstitutionValue.ResourceProperty
	s.Require().NotNil(prop, "expected a resource-property reference")
	s.Equal(resName, prop.ResourceName)
	gotFields := []string{}
	for _, item := range prop.Path {
		gotFields = append(gotFields, item.FieldName)
	}
	s.Equal(fields, gotFields)
}

func (s *CacheTransformTestSuite) runTransform(
	bp *schema.Blueprint,
	lg linktypes.DeclaredLinkGraph,
) *transform.SpecTransformerTransformOutput {
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          lg,
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}

func hasWarningContaining(diags []*core.Diagnostic, substr string) bool {
	for _, d := range diags {
		if d.Level == core.DiagnosticLevelWarning && strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

type vpcCacheLinkGraph struct {
	vpc   string
	cache string
}

func (g vpcCacheLinkGraph) Edges() []*linktypes.ResolvedLink {
	return []*linktypes.ResolvedLink{g.edge()}
}

func (g vpcCacheLinkGraph) EdgesFrom(name string) []*linktypes.ResolvedLink {
	if name == g.vpc {
		return []*linktypes.ResolvedLink{g.edge()}
	}
	return nil
}

func (g vpcCacheLinkGraph) EdgesTo(name string) []*linktypes.ResolvedLink {
	if name == g.cache {
		return []*linktypes.ResolvedLink{g.edge()}
	}
	return nil
}

func (vpcCacheLinkGraph) Resource(string) (*schema.Resource, linktypes.ResourceClass, bool) {
	return nil, "", false
}

func (g vpcCacheLinkGraph) edge() *linktypes.ResolvedLink {
	return &linktypes.ResolvedLink{
		Source:     g.vpc,
		Target:     g.cache,
		SourceType: "celerity/vpc",
		TargetType: "celerity/cache",
	}
}
