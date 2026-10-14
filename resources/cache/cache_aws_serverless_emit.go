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

	// defaultCacheNodeType is the node size applied when
	// aws.elasticache.<name>.nodeType is not set.
	//
	// ElastiCache requires a node type and rejects a replication group without one
	// ("Invalid Cache Node Type: null"). The abstract celerity/cache carries no
	// size, deliberately, since sizing is a property of the deploy target rather
	// than of the application, so the default lives here.
	//
	// The value is the one the celerity/cache specification documents for this
	// option, not an independently chosen size.
	defaultCacheNodeType = "cache.t3.micro"

	// defaultClusterShards is the number of node groups (shards) for a cluster-mode
	// cache when aws.elasticache.<name>.numShards is not set.
	defaultClusterShards = 3
	// Read replicas per shard when aws.elasticache.<name>.numReplicas is not set.
	//
	// The specification defines this per topology, a single instance is one
	// primary and nothing else, while a cluster is expected to be highly
	// available, which a shard without a replica cannot be.
	defaultReplicasSingleInstance = 0
	defaultReplicasClusterMode    = 1

	// Whether ElastiCache promotes a replica when the primary fails, when
	// aws.elasticache.<name>.automaticFailover is not set. Follows the same
	// reasoning as the replica count: only meaningful, and only expected, where
	// there is a replica to promote.
	defaultAutomaticFailoverSingleInstance = false
	defaultAutomaticFailoverClusterMode    = true

	// At-rest encryption from the specification: on unless the deploy
	// configuration turns it off, since a cache holding application data should
	// not be readable at rest by default. Unlike transit encryption this is a
	// real choice — nothing else in the spec depends on it.
	defaultAtRestEncryption = true

	// Days of automatic snapshots retained when
	// aws.elasticache.<name>.snapshotRetentionDays is not set. Zero disables
	// snapshots, matching the specification.
	defaultSnapshotRetentionDays = 0

	// defaultAuthTokenLength is the generated Redis AUTH token length. Redis AUTH
	// requires 16-128 printable characters; 32 keeps ample entropy.
	defaultAuthTokenLength = 32

	// cacheAuthLabelKey is the distinctive label the auth secret carries and the
	// replication group selects on, activating the provider's
	// aws/elasticache/replicationGroup::aws/secretsmanager/secret link. The link
	// reads the secret's raw string value and applies it as the RG's write-only
	// authToken at deploy time (requires transitEncryptionEnabled, set below).
	cacheAuthLabelKey = "celerity.cache.auth"

	// The printable characters a generated Redis AUTH
	// token must not contain.
	//
	// ElastiCache allows only alphanumerics plus '!', '&', '#', '$', '^', '<', '>'
	// and '-', so this is the complement of that set rather than a list of known-bad
	// delimiters.
	redisAuthExcludeCharacters = "\"%'()*+,./:;=?@[\\]_`{|}~ "

	// maxCachePhysicalNameLen caps the generated physical base name at the
	// ElastiCache replication group ID limit (40), the tightest of the
	// identifiers derived from it.
	maxCachePhysicalNameLen = 40
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
	// name is the logical cache name: deploy-config lookup key, label value and
	// description text. physicalName is the base every deployed identifier
	// derives from, an author-provided spec.name as-is (author owns
	// uniqueness, matching queue/bucket), otherwise app-scoped so two apps in
	// one account never collide on generated names.
	if diagnostics := shared.DeferredValueErrors(
		shared.AWSServerless,
		"cache", r.Name, r.Resource,
		shared.StructuralField{Name: "clusterMode", Value: specGet(r, "$.clusterMode")},
		shared.StructuralField{Name: "authMode", Value: specGet(r, "$.authMode")},
	); len(diagnostics) > 0 {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	// A referenced spec.name is carried through to the deployed identifiers, but
	// cannot serve as the logical name as it keys deploy-config lookups and the
	// auth label, both read here, before references are resolved. The resource's
	// own name stands in for those, exactly as it does when spec.name is not set.
	nameNode := specGet(r, "$.name")
	name := core.StringValue(nameNode)
	physicalName := nameNode
	if !shared.HasStringValue(nameNode) {
		name = r.Name
		physicalName = core.MappingNodeFromString(
			shared.AppScopedPhysicalName(
				shared.ResolveAppName(run), r.Name, maxCachePhysicalNameLen,
			),
		)
	} else if name == "" {
		name = r.Name
	}

	if diag := cachePresetSuitabilityError(r, name); diag != nil {
		return &transformutils.EmitResult{Diagnostics: []*core.Diagnostic{diag}}, nil
	}

	resources := map[string]*schema.Resource{}
	engine := cacheEngine(r)
	clusterMode := core.BoolValue(specGet(r, "$.clusterMode"))

	rgSpec := replicationGroupBaseSpec(r, run, name, engine, physicalName, clusterMode)

	var diagnostics []*core.Diagnostic
	if diag, err := applyVPCPlacement(r, name, physicalName, rgSpec, resources); err != nil {
		return nil, err
	} else if diag != nil {
		diagnostics = append(diagnostics, diag)
	}

	rgLinkSelector, err := applyCacheAuth(r, name, engine, physicalName, rgSpec, resources)
	if err != nil {
		return nil, err
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

	sharedaws.ApplyTags(resources, r.Resource.Metadata, sharedaws.Identity{
		AppName:      shared.ResolveAppName(run),
		ResourceName: r.Name,
		ResourceType: "celerity/cache",
	})

	return &transformutils.EmitResult{
		Resources: resources,
		DerivedValues: map[string]*schema.Value{
			cacheHostKey(r.Name): hostValue,
		},
		Diagnostics: diagnostics,
	}, nil
}

// cachePresetSuitabilityError validates that a managed VPC provides private
// subnets (a cache requires private-subnet placement). Referenced VPCs have
// unknown topology at transform time — the provider's subnet-group validation
// covers those at plan time. Returns nil when there is nothing to reject.
func cachePresetSuitabilityError(r *ResolvedCache, name string) *core.Diagnostic {
	if r.VPCName == "" || r.VPCReferenced {
		return nil
	}
	if _, noPrivate := presetsWithoutPrivateSubnets[r.VPCPreset]; !noPrivate {
		return nil
	}
	return &core.Diagnostic{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/cache %q requires private subnets, but its placement VPC preset %q "+
				"provides none; use a preset with private subnets (standard, isolated, or light)",
			name, r.VPCPreset,
		),
		Range: core.DiagnosticRangeFromSourceMeta(r.Resource.SourceMeta, nil),
	}
}

// cacheEngine resolves the cache engine, fixed to Valkey for v0 but honouring
// spec.engine if a future schema exposes it.
func cacheEngine(r *ResolvedCache) string {
	if engine := core.StringValue(specGet(r, "$.engine")); engine != "" {
		return engine
	}
	return defaultEngine
}

// cacheEngineVersion resolves the engine version, defaulting to 8.2 here
// (resolver-owns-defaults), not via a schema Default.
func cacheEngineVersion(r *ResolvedCache) string {
	if version := core.StringValue(specGet(r, "$.engineVersion")); version != "" {
		return version
	}
	return defaultEngineVersion
}

// replicationGroupBaseSpec builds the replication group's spec without VPC
// placement or auth fields. clusterMode selects the shard/replica topology:
// numShards applies only in cluster mode; numReplicas (replicas per shard)
// applies to both.
func replicationGroupBaseSpec(
	r *ResolvedCache,
	run *transformutils.Run,
	name, engine string,
	physicalName *core.MappingNode,
	clusterMode bool,
) *core.MappingNode {
	spec := core.MappingNodeFields(
		"replicationGroupId", physicalName,
		"replicationGroupDescription", core.MappingNodeFromString(fmt.Sprintf("Celerity cache %s", name)),
		"engine", core.MappingNodeFromString(engine),
		"engineVersion", core.MappingNodeFromString(cacheEngineVersion(r)),
		"port", core.MappingNodeFromInt(defaultCachePort),
		"cacheNodeType", core.MappingNodeFromString(
			elasticacheConfigString(run, name, "nodeType", defaultCacheNodeType),
		),
		// Always on, and deliberately not configurable. Both auth modes require
		// TLS, an AUTH token and IAM authentication each depend on it and
		// authMode allows only those two, so there is no celerity/cache for which
		// false is valid.
		"transitEncryptionEnabled", core.MappingNodeFromBool(true),
		"atRestEncryptionEnabled", core.MappingNodeFromBool(
			elasticacheConfigBool(run, name, "atRestEncryption", defaultAtRestEncryption),
		),
		"snapshotRetentionLimit", core.MappingNodeFromInt(
			elasticacheConfigInt(run, name, "snapshotRetentionDays", defaultSnapshotRetentionDays),
		),
	)

	defaultReplicas := defaultReplicasSingleInstance
	defaultFailover := defaultAutomaticFailoverSingleInstance
	if clusterMode {
		defaultReplicas = defaultReplicasClusterMode
		defaultFailover = defaultAutomaticFailoverClusterMode
		spec.Fields["numNodeGroups"] = core.MappingNodeFromInt(
			elasticacheConfigInt(run, name, "numShards", defaultClusterShards))
	} else {
		spec.Fields["numNodeGroups"] = core.MappingNodeFromInt(1)
	}

	spec.Fields["replicasPerNodeGroup"] = core.MappingNodeFromInt(
		elasticacheConfigInt(run, name, "numReplicas", defaultReplicas))
	spec.Fields["automaticFailoverEnabled"] = core.MappingNodeFromBool(
		elasticacheConfigBool(run, name, "automaticFailover", defaultFailover),
	)

	return spec
}

// applyVPCPlacement emits the subnet group and sets the RG's placement fields
// when the cache is linked to a VPC. When it is not, it returns a warning
// diagnostic (the RG is still emitted) and leaves the placement fields unset.
func applyVPCPlacement(
	r *ResolvedCache,
	name string,
	physicalName *core.MappingNode,
	rgSpec *core.MappingNode,
	resources map[string]*schema.Resource,
) (*core.Diagnostic, error) {
	if r.VPCName == "" {
		return &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/cache %q is not linked to a celerity/vpc; caches require VPC placement for "+
					"secure network access",
				name,
			),
			Range: core.DiagnosticRangeFromSourceMeta(r.Resource.SourceMeta, nil),
		}, nil
	}

	vpcConcrete := vpc.ConcreteResourceName(r.VPCName)
	subnetGroupName := shared.DerivedName(physicalName, "-cache-subnets")

	subnetIdsRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.privateSubnetIds}", vpcConcrete))
	if err != nil {
		return nil, err
	}
	// The group the VPC created for this cache, not the VPC's whole list. A link
	// opening a path to the cache pairs the workload's group with a group the VPC
	// minted for this resource alone; given the whole list it cannot tell which
	// group is the cache's, and pairing against a shared one would open a path to
	// everything else in it.
	sgRef, err := shared.SubstitutionMappingNode(
		shared.VPCSecurityGroupIDRef(
			vpcConcrete, r.Name, shared.SecurityGroupRoleCache,
		),
	)
	if err != nil {
		return nil, err
	}

	resources[subnetGroupResourceName(r.Name)] = &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/elasticache/subnetGroup"},
		Spec: core.MappingNodeFields(
			"cacheSubnetGroupName", subnetGroupName,
			"description", core.MappingNodeFromString(fmt.Sprintf("Subnets for Celerity cache %s", name)),
			"subnetIds", subnetIdsRef,
		),
		Metadata: infraMeta(r.Name),
	}
	// Reference the emitted subnet group's name so a dependency edge is created,
	// rather than repeating the plain string (which would leave no ordering edge
	// and let the replication group be created before its subnet group exists).
	subnetGroupNameRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf(
			"${resources.%s.spec.cacheSubnetGroupName}",
			subnetGroupResourceName(r.Name),
		),
	)
	if err != nil {
		return nil, err
	}

	rgSpec.Fields["cacheSubnetGroupName"] = subnetGroupNameRef
	rgSpec.Fields["securityGroupIds"] = core.MappingNodeItems(sgRef)
	return nil, nil
}

// applyCacheAuth provisions the auth resources and returns the replication
// group's link selector. transitEncryption is already set on the RG (required
// for both Redis AUTH and RBAC): iam mode provisions RBAC user groups; password
// mode provisions an auth-token secret and selects it by label.
func applyCacheAuth(
	r *ResolvedCache,
	name, engine string,
	physicalName *core.MappingNode,
	rgSpec *core.MappingNode,
	resources map[string]*schema.Resource,
) (*schema.LinkSelector, error) {
	if cacheAuthMode(r) != "iam" {
		resources[authSecretResourceName(r.Name)] = buildAuthSecret(r, name, physicalName)
		// Activate the replicationGroup::secret link: the RG selects the auth
		// secret by its distinctive label (merged with any existing selector).
		return mergeLinkSelectorLabel(r.Resource.LinkSelector, cacheAuthLabelKey, name), nil
	}

	// iam mode: ElastiCache RBAC. Emit an IAM-authenticated user and a user group
	// that contains it plus the managed "default" user (required in every RBAC
	// user group), then attach the group to the replication group. No stored
	// secret is needed, the client generates a short-lived SigV4 token from IAM
	// credentials. The handler that links to the cache stamps
	// aws.lambda.elasticache.<rg>.authMode=iam so the function::replicationGroup
	// link creates the elasticache:Connect grant.
	userRes, groupRes, err := buildIAMUserAndGroup(r, physicalName, engine)
	if err != nil {
		return nil, err
	}
	resources[iamUserResourceName(r.Name)] = userRes
	resources[userGroupResourceName(r.Name)] = groupRes

	userGroupRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.userGroupId}", userGroupResourceName(r.Name)))
	if err != nil {
		return nil, err
	}
	rgSpec.Fields["userGroupIds"] = core.MappingNodeItems(userGroupRef)
	return r.Resource.LinkSelector, nil
}

func elasticacheConfigInt(run *transformutils.Run, name, suffix string, fallback int) int {
	if run != nil {
		if node, ok := sharedaws.ResolveDeployConfigNode(run.TransformContext, "aws.elasticache", name, suffix); ok {
			return core.IntValue(node)
		}
	}
	return fallback
}

func elasticacheConfigString(
	run *transformutils.Run,
	name, suffix, fallback string,
) string {
	if run != nil {
		if node, ok := sharedaws.ResolveDeployConfigNode(
			run.TransformContext, "aws.elasticache", name, suffix,
		); ok {
			if value := core.StringValue(node); value != "" {
				return value
			}
		}
	}
	return fallback
}

func elasticacheConfigBool(
	run *transformutils.Run,
	name, suffix string,
	fallback bool,
) bool {
	if run != nil {
		if node, ok := sharedaws.ResolveDeployConfigNode(
			run.TransformContext, "aws.elasticache", name, suffix,
		); ok && !core.IsNilMappingNode(node) {
			return core.BoolValue(node)
		}
	}
	return fallback
}

func cacheHostValue(name string, clusterMode bool) (*schema.Value, error) {
	endpointField := "primaryEndPoint"
	if clusterMode {
		endpointField = "configurationEndPoint"
	}
	return shared.SubstitutionBlueprintValue(
		fmt.Sprintf("${resources.%s.spec.%s.address}", replicationGroupResourceName(name), endpointField),
	)
}

// Must equal <rg>_host: the property map's "spec.host" ValueRef resolves to
// concreteName + "_host".
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

// The secret's generated random value is stored as the raw secret string, which
// is what the replicationGroup::secret link reads as the Redis AUTH token. The
// secret carries the cache's own labels so a handler that links to the cache
// also links to the secret (injecting it as a SECRET_<name> env var), plus the
// distinctive auth label the RG selects on.
func buildAuthSecret(r *ResolvedCache, name string, physicalName *core.MappingNode) *schema.Resource {
	labels := map[string]string{}
	if r.Resource.Metadata != nil && r.Resource.Metadata.Labels != nil {
		maps.Copy(labels, r.Resource.Metadata.Labels.Values)
	}
	// Assigned last so a user label reusing this key cannot displace the auth selector.
	labels[cacheAuthLabelKey] = name
	meta := infraMeta(r.Name)
	meta.Labels = &schema.StringMap{Values: labels}

	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/secretsmanager/secret"},
		Spec: core.MappingNodeFields(
			"name", shared.DerivedName(physicalName, "-cache-auth"),
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

// byLabel semantics are a union across labels, so appending the auth label
// preserves every existing edge while adding the RG -> auth-secret edge.
func mergeLinkSelectorLabel(existing *schema.LinkSelector, key, value string) *schema.LinkSelector {
	values := map[string]string{}
	var exclude *schema.StringList
	if existing != nil {
		if existing.ByLabel != nil {
			maps.Copy(values, existing.ByLabel.Values)
		}
		exclude = existing.Exclude
	}
	// Assigned last so an existing selector reusing this key cannot displace the value.
	values[key] = value
	return &schema.LinkSelector{
		ByLabel: &schema.StringMap{Values: values},
		Exclude: exclude,
	}
}

func authSecretResourceName(name string) string {
	return fmt.Sprintf("%s_cache_auth_secret", name)
}

// The RBAC user group must contain the managed "default" user in addition to
// the IAM user.
func buildIAMUserAndGroup(
	r *ResolvedCache,
	physicalName *core.MappingNode,
	engine string,
) (*schema.Resource, *schema.Resource, error) {
	userID := shared.DerivedName(physicalName, "-iam")
	user := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/elasticache/user"},
		Spec: core.MappingNodeFields(
			"engine", core.MappingNodeFromString(engine),
			"userId", userID,
			"userName", userID,
			// Full access; scope down in a follow-up if per-command ACLs are needed.
			"accessString", core.MappingNodeFromString("on ~* +@all"),
			"authenticationMode", core.MappingNodeFields(
				"type", core.MappingNodeFromString("iam"),
			),
		),
		Metadata: infraMeta(r.Name),
	}

	userIDRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.userId}", iamUserResourceName(r.Name)))
	if err != nil {
		return nil, nil, err
	}
	group := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/elasticache/userGroup"},
		Spec: core.MappingNodeFields(
			"engine", core.MappingNodeFromString(engine),
			"userGroupId", shared.DerivedName(physicalName, "-users"),
			"userIds", core.MappingNodeItems(
				core.MappingNodeFromString("default"),
				userIDRef,
			),
		),
		Metadata: infraMeta(r.Name),
	}
	return user, group, nil
}

func iamUserResourceName(name string) string {
	return fmt.Sprintf("%s_cache_iam_user", name)
}

func userGroupResourceName(name string) string {
	return fmt.Sprintf("%s_cache_user_group", name)
}

func replicationGroupResourceName(name string) string {
	return fmt.Sprintf("%s_elasticache_rg", name)
}

func subnetGroupResourceName(name string) string {
	return fmt.Sprintf("%s_elasticache_subnet_group", name)
}
