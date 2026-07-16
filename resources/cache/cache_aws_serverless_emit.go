package cache

import (
	"context"
	"fmt"
	"maps"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// defaultEngine is Valkey, a Redis OSS-compatible fork. Engine is fixed for v0
	// (not a user-selectable spec field); "8.2" is a Valkey version, not a valid
	// version for the legacy "redis" engine.
	defaultEngine = "valkey"
	// defaultEngineVersion is applied when the abstract spec omits engineVersion
	// (resolver-owns-defaults; the abstract schema carries no Default).
	defaultEngineVersion = "8.2"
	defaultCachePort     = 6379

	// defaultClusterShards is the number of node groups (shards) for a cluster-mode
	// cache when aws.elasticache.<name>.numShards is not set.
	defaultClusterShards = 3
	// defaultReplicasPerNodeGroup is the number of read replicas per shard when
	// aws.elasticache.<name>.numReplicas is not set: 0 (single primary node).
	defaultReplicasPerNodeGroup = 0

	// defaultAuthTokenLength is the generated Redis AUTH token length. Redis AUTH
	// requires 16-128 printable characters; 32 keeps ample entropy.
	defaultAuthTokenLength = 32

	// cacheAuthLabelKey is the distinctive label the auth secret carries and the
	// replication group selects on, activating the provider's
	// aws/elasticache/replicationGroup::aws/secretsmanager/secret link. The link
	// reads the secret's raw string value and applies it as the RG's write-only
	// authToken at deploy time (requires transitEncryptionEnabled, set below).
	cacheAuthLabelKey = "celerity.cache.auth"

	// redisAuthExcludeCharacters are characters Redis AUTH tokens must not contain
	// (whitespace and the reserved delimiters the URL/connection-string forms and
	// the Redis protocol choke on): double-quote, at-sign, forward slash,
	// backslash and space.
	redisAuthExcludeCharacters = "\"@/\\ "
)

// presetsWithoutPrivateSubnets are managed VPC presets that provide no private
// subnets, so a cache (which requires private-subnet placement) cannot be placed
// into them.
var presetsWithoutPrivateSubnets = map[string]struct{}{
	"public":       {},
	"light-public": {},
}

func emitCache(
	_ context.Context,
	run *transformutils.Run,
	r *ResolvedCache,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	name := core.StringValue(specGet(r, "$.name"))
	if name == "" {
		name = r.Name
	}

	// Preset-suitability validation: a managed VPC must provide private subnets.
	// Referenced VPCs have unknown topology at transform time — the provider's
	// subnet-group validation covers those at plan time.
	if r.VPCName != "" && !r.VPCReferenced {
		if _, noPrivate := presetsWithoutPrivateSubnets[r.VPCPreset]; noPrivate {
			return &transformutils.EmitResult{
				Diagnostics: []*core.Diagnostic{
					{
						Level: core.DiagnosticLevelError,
						Message: fmt.Sprintf(
							"celerity/cache %q requires private subnets, but its placement VPC preset %q "+
								"provides none; use a preset with private subnets (standard, isolated, or light)",
							name, r.VPCPreset,
						),
					},
				},
			}, nil
		}
	}

	var diagnostics []*core.Diagnostic
	resources := map[string]*schema.Resource{}

	// Engine is fixed to Valkey for v0; honour spec.engine if a future schema
	// exposes it, otherwise default. engineVersion default 8.2 is injected here
	// (resolver-owns-defaults), not via a schema Default.
	engine := core.StringValue(specGet(r, "$.engine"))
	if engine == "" {
		engine = defaultEngine
	}
	engineVersion := core.StringValue(specGet(r, "$.engineVersion"))
	if engineVersion == "" {
		engineVersion = defaultEngineVersion
	}

	clusterMode := core.BoolValue(specGet(r, "$.clusterMode"))

	rgSpec := core.MappingNodeFields(
		"replicationGroupId", core.MappingNodeFromString(name),
		"replicationGroupDescription", core.MappingNodeFromString(fmt.Sprintf("Celerity cache %s", name)),
		"engine", core.MappingNodeFromString(engine),
		"engineVersion", core.MappingNodeFromString(engineVersion),
		"transitEncryptionEnabled", core.MappingNodeFromBool(true),
		"port", core.MappingNodeFromInt(defaultCachePort),
	)
	// clusterMode selects the shard/replica topology. numShards applies only in
	// cluster mode; numReplicas (replicas per shard) applies to both.
	if clusterMode {
		rgSpec.Fields["numNodeGroups"] = core.MappingNodeFromInt(
			elasticacheConfigInt(run, name, "numShards", defaultClusterShards))
	} else {
		rgSpec.Fields["numNodeGroups"] = core.MappingNodeFromInt(1)
	}
	rgSpec.Fields["replicasPerNodeGroup"] = core.MappingNodeFromInt(
		elasticacheConfigInt(run, name, "numReplicas", defaultReplicasPerNodeGroup))

	if r.VPCName != "" {
		vpcConcrete := vpc.ConcreteResourceName(r.VPCName)
		subnetGroupName := fmt.Sprintf("%s-cache-subnets", name)

		subnetIdsRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.privateSubnetIds}", vpcConcrete))
		if err != nil {
			return nil, err
		}
		sgRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.securityGroups}", vpcConcrete))
		if err != nil {
			return nil, err
		}

		resources[subnetGroupResourceName(r.Name)] = &schema.Resource{
			Type: &schema.ResourceTypeWrapper{Value: "aws/elasticache/subnetGroup"},
			Spec: core.MappingNodeFields(
				"cacheSubnetGroupName", core.MappingNodeFromString(subnetGroupName),
				"description", core.MappingNodeFromString(fmt.Sprintf("Subnets for Celerity cache %s", name)),
				"subnetIds", subnetIdsRef,
			),
			Metadata: infraMeta(r.Name),
		}
		rgSpec.Fields["cacheSubnetGroupName"] = core.MappingNodeFromString(subnetGroupName)
		rgSpec.Fields["securityGroupIds"] = sgRef
	} else {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/cache %q is not linked to a celerity/vpc; caches require VPC placement for "+
					"secure network access",
				name,
			),
		})
	}

	// Authentication. transitEncryption is already set on the RG (required for
	// Redis AUTH). Password mode is fully wired here; iam mode (ElastiCache
	// RBAC user groups) remains a scoped deferral.
	rgLinkSelector := r.Resource.LinkSelector
	if cacheAuthMode(r) == "password" {
		resources[authSecretResourceName(r.Name)] = buildAuthSecret(r, name)
		// Activate the replicationGroup::secret link: the RG selects the auth
		// secret by its distinctive label (merged with any existing selector).
		rgLinkSelector = mergeLinkSelectorLabel(r.Resource.LinkSelector, cacheAuthLabelKey, name)
	} else {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/cache %q uses authMode %q; ElastiCache IAM/RBAC user-group wiring is planned "+
					"and is not emitted for this cache, so no RBAC user groups are created. Password mode "+
					"is the supported v0 default",
				name, cacheAuthMode(r),
			),
		})
	}

	// The replication group is the resource handlers link to, so it carries the
	// cache's labels; the subnet group is internal and needs none.
	rgMeta := infraMeta(r.Name)
	if r.Resource.Metadata != nil {
		rgMeta.Labels = r.Resource.Metadata.Labels
	}
	resources[replicationGroupResourceName(r.Name)] = &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/elasticache/replicationGroup"},
		Spec:         rgSpec,
		Metadata:     rgMeta,
		LinkSelector: rgLinkSelector,
	}

	// Rewrite any ${resources.<abstract>.spec.x} references into concrete form.
	for _, res := range resources {
		res.Spec = subwalk.WalkMappingNode(res.Spec, transformutils.RewriteResourcePropertyRefs(rw))
	}

	// spec.host is a derived value because the endpoint differs by topology: the
	// configuration endpoint when clustered, the primary endpoint otherwise. The
	// property map's ValueRef ("spec.host" -> ${values.<rg>_host}) resolves to it.
	hostValue, err := cacheHostValue(r.Name, clusterMode)
	if err != nil {
		return nil, err
	}

	return &transformutils.EmitResult{
		Resources: resources,
		DerivedValues: map[string]*schema.Value{
			cacheHostKey(r.Name): hostValue,
		},
		Diagnostics: diagnostics,
	}, nil
}

// elasticacheConfigInt resolves an aws.elasticache.<name>.<suffix> deploy-config
// integer, falling back to the given default when unset.
func elasticacheConfigInt(run *transformutils.Run, name, suffix string, fallback int) int {
	if run != nil {
		if node, ok := sharedaws.ResolveDeployConfigNode(run.TransformContext, "aws.elasticache", name, suffix); ok {
			return core.IntValue(node)
		}
	}
	return fallback
}

// cacheHostValue builds the derived host value: the configuration endpoint
// address in cluster mode, the primary endpoint address otherwise.
func cacheHostValue(name string, clusterMode bool) (*schema.Value, error) {
	endpointField := "primaryEndPoint"
	if clusterMode {
		endpointField = "configurationEndPoint"
	}
	return shared.SubstitutionBlueprintValue(
		fmt.Sprintf("${resources.%s.spec.%s.address}", replicationGroupResourceName(name), endpointField),
	)
}

// cacheHostKey is the derived-value name the property map's "spec.host" ValueRef
// (concreteName + "_host") resolves to; it must equal <rg>_host.
func cacheHostKey(name string) string {
	return replicationGroupResourceName(name) + "_host"
}

func cacheAuthMode(r *ResolvedCache) string {
	mode := core.StringValue(specGet(r, "$.authMode"))
	if mode == "" {
		return "password"
	}
	return mode
}

func infraMeta(abstractName string) *schema.Metadata {
	// Labels are set from the cache's own metadata (preserved for handler links).
	return &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: abstractName,
				AbstractResourceType: "celerity/cache",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
}

func specGet(r *ResolvedCache, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, r.Resource.Spec)
	return node
}

// buildAuthSecret emits the Secrets Manager secret whose generated random value
// becomes the cache's Redis AUTH token. The token is Redis-AUTH-safe (length,
// excluded characters) and stored as the raw secret string, which is what the
// replicationGroup::secret link reads. The secret carries the cache's own labels
// so a handler that links to the cache also links to the secret via the
// aws/lambda/function::aws/secretsmanager/secret link (injecting it as a
// SECRET_<name> env var), plus the distinctive auth label the RG selects on.
func buildAuthSecret(r *ResolvedCache, name string) *schema.Resource {
	labels := map[string]string{cacheAuthLabelKey: name}
	if r.Resource.Metadata != nil && r.Resource.Metadata.Labels != nil {
		maps.Copy(labels, r.Resource.Metadata.Labels.Values)
	}
	meta := infraMeta(r.Name)
	meta.Labels = &schema.StringMap{Values: labels}

	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/secretsmanager/secret"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString(fmt.Sprintf("%s-cache-auth", name)),
			"description", core.MappingNodeFromString(
				fmt.Sprintf("Redis AUTH token for Celerity cache %s", name)),
			"generateSecretString", core.MappingNodeFields(
				"passwordLength", core.MappingNodeFromInt(defaultAuthTokenLength),
				"excludeCharacters", core.MappingNodeFromString(redisAuthExcludeCharacters),
			),
		),
		Metadata: meta,
	}
}

// mergeLinkSelectorLabel returns a link selector that carries the given byLabel
// entry in addition to any existing selector labels. byLabel semantics are a
// union across labels, so appending the auth label preserves every existing
// edge while adding the RG -> auth-secret edge.
func mergeLinkSelectorLabel(existing *schema.LinkSelector, key, value string) *schema.LinkSelector {
	values := map[string]string{key: value}
	var exclude *schema.StringList
	if existing != nil {
		if existing.ByLabel != nil {
			maps.Copy(values, existing.ByLabel.Values)
		}
		exclude = existing.Exclude
	}
	return &schema.LinkSelector{
		ByLabel: &schema.StringMap{Values: values},
		Exclude: exclude,
	}
}

func authSecretResourceName(name string) string {
	return fmt.Sprintf("%s_cache_auth_secret", name)
}

func replicationGroupResourceName(name string) string {
	return fmt.Sprintf("%s_elasticache_rg", name)
}

func subnetGroupResourceName(name string) string {
	return fmt.Sprintf("%s_elasticache_subnet_group", name)
}
