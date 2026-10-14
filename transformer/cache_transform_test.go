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
	// ElastiCache rejects a replication group with no node type ("Invalid Cache
	// Node Type: null"), so one is always emitted. The default is the one the
	// celerity/cache specification documents for
	// aws.elasticache.<name>.nodeType.
	s.Equal("cache.t3.micro", core.StringValue(rg.Spec.Fields["cacheNodeType"]))
	// A single instance is one primary with nothing to fail over to.
	s.Equal(0, core.IntValue(rg.Spec.Fields["replicasPerNodeGroup"]))
	s.False(core.BoolValue(rg.Spec.Fields["automaticFailoverEnabled"]))
	// At-rest encryption is not on by default in ElastiCache, so leaving the
	// field out deploys an unencrypted cache. On unless the deploy configuration
	// says otherwise, unlike transit encryption below.
	s.True(core.BoolValue(rg.Spec.Fields["atRestEncryptionEnabled"]))
	s.Equal(0, core.IntValue(rg.Spec.Fields["snapshotRetentionLimit"]))
	s.True(core.BoolValue(rg.Spec.Fields["transitEncryptionEnabled"]))
	// cacheSubnetGroupName references the emitted subnet group so a dependency
	// edge is created. A plain string leaves no edge, and the replication group
	// is then free to be created before the subnet group exists, which
	// ElastiCache rejects.
	s.Equal("myCache_elasticache_subnet_group",
		resourceRefName(rg.Spec.Fields["cacheSubnetGroupName"]))
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
	// Named individually rather than just "not empty": ElastiCache rejects a token
	// containing any of these outright, and one missing from the set only shows up
	// when Secrets Manager happens to generate it, which is a failure that hides
	// until a deployment gets far enough to apply the token.
	excluded := core.StringValue(gen.Fields["excludeCharacters"])
	for _, forbidden := range []string{"\"", "@", "/", "%", "\\", " "} {
		s.Contains(excluded, forbidden,
			"Redis AUTH tokens must not contain %q", forbidden)
	}
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

// When the author omits spec.name, every deployed identifier derives from an
// app-scoped base (<app>-<resourceName>) so two apps sharing an account never
// collide on generated names; blueprint RESOURCE names stay app-agnostic.
// Validation contexts use the placeholder app segment.
func (s *CacheTransformTestSuite) Test_omitted_name_app_scopes_deployed_identifiers() {
	out := s.transformCacheWithVPC(core.MappingNodeFields(), "standard", nil)
	resources := out.TransformedBlueprint.Resources.Values

	rg := resources["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.Equal("placeholder-app-myCache",
		core.StringValue(rg.Spec.Fields["replicationGroupId"]))

	sng := resources["myCache_elasticache_subnet_group"]
	s.Require().NotNil(sng)
	s.Equal("placeholder-app-myCache-cache-subnets",
		core.StringValue(sng.Spec.Fields["cacheSubnetGroupName"]))

	secret := resources["myCache_cache_auth_secret"]
	s.Require().NotNil(secret)
	s.Equal("placeholder-app-myCache-cache-auth",
		core.StringValue(secret.Spec.Fields["name"]))
}

// A user label that reuses the internal auth selector key must not displace the
// value the secret carries and the replication group selects on.
func (s *CacheTransformTestSuite) Test_user_label_cannot_override_auth_selector() {
	out := s.transformCacheWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("sessions"),
		),
		"standard",
		map[string]string{"celerity.cache.auth": "attacker-value"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	secret := resources["myCache_cache_auth_secret"]
	s.Require().NotNil(secret)
	s.Equal("sessions", secret.Metadata.Labels.Values["celerity.cache.auth"],
		"the internal auth selector value wins over a colliding user label")

	rg := resources["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.Equal("sessions", rg.LinkSelector.ByLabel.Values["celerity.cache.auth"],
		"the RG still selects the secret on the internal auth value")
}

func (s *CacheTransformTestSuite) Test_iam_auth_mode_emits_user_and_user_group() {
	out := s.transformCacheWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("sessions"),
			"authMode", core.MappingNodeFromString("iam"),
		),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	// iam mode uses ElastiCache RBAC, not a stored secret.
	s.NotContains(resources, "myCache_cache_auth_secret")
	s.False(hasWarningContaining(out.Diagnostics, "authMode"), "iam mode is implemented, not deferred")

	user := resources["myCache_cache_iam_user"]
	s.Require().NotNil(user)
	s.Equal("aws/elasticache/user", user.Type.Value)
	s.Equal("iam", core.StringValue(user.Spec.Fields["authenticationMode"].Fields["type"]))

	group := resources["myCache_cache_user_group"]
	s.Require().NotNil(group)
	s.Equal("aws/elasticache/userGroup", group.Type.Value)
	userIDs := group.Spec.Fields["userIds"].Items
	s.Require().Len(userIDs, 2)
	s.Equal("default", core.StringValue(userIDs[0]), "the managed default user is required in the group")
	s.Equal("myCache_cache_iam_user", resourceRefName(userIDs[1]),
		"the group references the emitted IAM user")

	rg := resources["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.True(core.BoolValue(rg.Spec.Fields["transitEncryptionEnabled"]))
	userGroupIds := rg.Spec.Fields["userGroupIds"].Items
	s.Require().Len(userGroupIds, 1)
	s.Equal("myCache_cache_user_group", resourceRefName(userGroupIds[0]),
		"the RG references the emitted user group")
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
	// The specification defines the replica and failover defaults per topology: a
	// cluster is expected to be highly available, which a shard with no replica
	// and no failover cannot be.
	s.Equal(3, core.IntValue(rg.Spec.Fields["numNodeGroups"]))
	s.Equal(1, core.IntValue(rg.Spec.Fields["replicasPerNodeGroup"]))
	s.True(core.BoolValue(rg.Spec.Fields["automaticFailoverEnabled"]))
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

// Transit encryption is fixed, not deploy configuration.
//
// authMode allows only "password" and "iam", and both require TLS, so there is
// no cache for which false is valid. A knob would be a switch whose only
// non-default position fails the deploy on every cache, so the key is not read
// at all. Asserting the default value alone would not catch its reintroduction.
func (s *CacheTransformTestSuite) Test_transit_encryption_ignores_deploy_configuration() {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myCache": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/cache"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("sessions"),
					),
				},
			},
		},
	}
	ctx := &fakeTransformContext{
		configVars: map[string]*core.ScalarValue{
			"aws.elasticache.sessions.transitEncryption": core.ScalarFromBool(false),
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
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: ctx,
		},
	)
	s.Require().NoError(err)

	rg := out.TransformedBlueprint.Resources.Values["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.True(core.BoolValue(rg.Spec.Fields["transitEncryptionEnabled"]),
		"transit encryption must stay on even when deploy config asks for false")
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

// A name is often environment-specific, so authors set it from a variable. The
// name reaches AWS unresolved, and the identifiers derived from it keep the
// reference with their suffix appended rather than becoming a bare suffix.
// Reading it with core.StringValue instead produced an empty name, which took
// the "no name given" path and silently replaced the author's name with a
// generated one.
func (s *CacheTransformTestSuite) Test_name_set_from_a_variable_reaches_the_deployed_identifiers() {
	nameRef, err := shared.SubstitutionMappingNode("${variables.cacheName}")
	s.Require().NoError(err)

	out := s.transformCacheWithVPC(
		core.MappingNodeFields("name", nameRef),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	rg := resources["myCache_elasticache_rg"]
	s.Require().NotNil(rg)
	s.Require().NotNil(
		rg.Spec.Fields["replicationGroupId"].StringWithSubstitutions,
		"the replication group id carries the reference, not a resolved string",
	)

	// "<name>-cache-subnets": the reference, then the literal suffix.
	sng := resources["myCache_elasticache_subnet_group"]
	s.Require().NotNil(sng)
	derived := sng.Spec.Fields["cacheSubnetGroupName"]
	s.Require().NotNil(derived.StringWithSubstitutions)
	values := derived.StringWithSubstitutions.Values
	s.Require().Len(values, 2)
	s.NotNil(values[0].SubstitutionValue, "the base is still the reference")
	s.Require().NotNil(values[1].StringValue)
	s.Equal("-cache-subnets", *values[1].StringValue)
}

// clusterMode decides how many node groups the replication group is given, so
// the transformer reads it while building the resource graph, before references
// are resolved. Read as a reference it yielded false, and a cache the author
// asked to run in cluster mode was deployed as a single instance with nothing
// reported.
func (s *CacheTransformTestSuite) Test_cluster_mode_set_from_a_reference_is_rejected() {
	clusterModeRef, err := shared.SubstitutionMappingNode("${variables.clusterMode}")
	s.Require().NoError(err)

	out := s.transformCacheWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("sessions"),
			"clusterMode", clusterModeRef,
		),
		"standard",
		nil,
	)

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelError && strings.Contains(d.Message, "clusterMode") {
			found = true
		}
	}
	s.True(found, "expected an error naming the field that cannot be a reference")

	// Nothing is emitted: a half-built cache is worse than none, and the author
	// has to choose a literal either way.
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myCache_elasticache_rg")
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
