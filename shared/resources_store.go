package shared

import (
	"strconv"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// The internal resources namespace config store: one entry per backing resource
// a handler links to, which the SDKs read at cold start to resolve a resource
// they were given the name of rather than the address of.
//
// This file is the portable half. The key vocabulary below is the contract every
// SDK reads on every target, so it lives here and not with any one target's
// emit. What a given target writes against those keys is its own, supplied
// through ResourcesStoreTarget.

const (
	// ResourcesStoreResourceName is the fixed concrete name of the store, one
	// per blueprint.
	ResourcesStoreResourceName = "celerityResourcesConfigStore"

	// ResourcesStoreSharedParentKey groups the store's contributions.
	ResourcesStoreSharedParentKey = "resources-store"
)

// Suffixes the SDKs append to a configKey. A target writes values against these;
// it does not get to choose them, because the SDK reading them is the same code
// on every target.
const (
	// StoreRegionSuffix is the region an external resource lives in. The SDKs
	// treat it as absent meaning the application's own region. Written whenever
	// the region is known rather than only when it differs, so a handler
	// addresses the resource where it actually is instead of inferring it.
	StoreRegionSuffix = "_region"

	storeHostSuffix        = "_host"
	storeReadHostSuffix    = "_readHost"
	storePortSuffix        = "_port"
	storeAuthModeSuffix    = "_authMode"
	storeUserSuffix        = "_user"
	storeTLSSuffix         = "_tls"
	storeClusterModeSuffix = "_clusterMode"
	storeDatabaseSuffix    = "_database"
	storeEngineSuffix      = "_engine"
	storeSSLSuffix         = "_ssl"

	// Both secret keys are store-neutral by name. The SDK treats the value as an
	// opaque reference and hands it to whichever secret store its platform
	// resolves to; that the reference is an ARN on AWS is a detail the SDK has no
	// reason to know.
	storeAuthTokenSecretIDSuffix   = "_authTokenSecretId"
	storeCredentialsSecretIDSuffix = "_credentialsSecretId"
)

// BackingIdentifier is how a handler addresses a backing resource that one
// identifier describes: a queue, a topic, a data store or a bucket.
type BackingIdentifier struct {
	// Value is a reference to the emitted resource's physical-identifier output
	// for a managed resource, or the literal identifier for an external one.
	Value *core.MappingNode
	// Region is set when the target knows where an external resource lives.
	Region string
}

// CacheConnection is what a handler needs in order to reach a linked cache.
//
// A cache takes several keys rather than one identifier, because an address
// alone does not describe a connection.
type CacheConnection struct {
	Host        *core.MappingNode
	Port        int
	TLS         bool
	ClusterMode bool
	AuthMode    string
	// AuthTokenSecretID is a reference, never a copied value. The token is
	// generated at deploy time and can rotate; a copy in the store would keep
	// working until the first rotation and then fail in a way that looks nothing
	// like its cause. Set only when the auth mode needs one.
	AuthTokenSecretID *core.MappingNode
	// User is the identity the SDK signs a connection token for, set only for an
	// identity-based auth mode.
	User *core.MappingNode
}

// DatabaseConnection is what a handler needs in order to reach a linked SQL
// database, for the same reason a cache takes several keys.
type DatabaseConnection struct {
	Host     *core.MappingNode
	ReadHost *core.MappingNode
	Database *core.MappingNode
	Port     int
	Engine   string
	User     string
	SSL      bool
	AuthMode string
	// CredentialsSecretID is a reference for the same reason
	// CacheConnection.AuthTokenSecretID is.
	CredentialsSecretID *core.MappingNode
}

// ResourcesStoreTarget is how one deploy target realises the store: the concrete
// resource that holds the entries, and the values it writes for each kind of
// backing resource.
//
// A second target implements this rather than forking the assembly: the
// traversal of a handler's links, the configKey derivation and the key
// vocabulary are all portable, and only the values are not.
type ResourcesStoreTarget interface {
	// StoreResourceType is the concrete resource type holding the entries.
	StoreResourceType() string

	// StoreSeedSpec is that resource's spec, given the entries to carry.
	StoreSeedSpec(appName, storePath string, values *core.MappingNode) *core.MappingNode

	// BackingIdentifier addresses one single-identifier backing resource. A
	// false second return means this target registers no backing of that
	// celerity type, so nothing is written for it.
	BackingIdentifier(
		celerityType string,
		abstractName string,
		resource *schema.Resource,
	) (BackingIdentifier, bool)

	// CacheConnection and DatabaseConnection describe a linked cache or
	// database. A false second return leaves the resource out of the store.
	CacheConnection(abstractName string, resource *schema.Resource) (CacheConnection, bool)
	DatabaseConnection(abstractName string, resource *schema.Resource) (DatabaseConnection, bool)
}

// ResourcesStoreEntries accumulates a store's contents, keeping the first entry
// written for a key.
//
// A duplicate configKey means an invalid blueprint, which the CLI rejects with a
// fatal check before deploy. First-wins is deterministic as long as the caller
// traverses handlers in a stable order.
type ResourcesStoreEntries struct {
	values map[string]*core.MappingNode
}

func NewResourcesStoreEntries() *ResourcesStoreEntries {
	return &ResourcesStoreEntries{values: map[string]*core.MappingNode{}}
}

func (e *ResourcesStoreEntries) Empty() bool {
	return len(e.values) == 0
}

func (e *ResourcesStoreEntries) has(key string) bool {
	_, seen := e.values[key]

	return seen
}

func (e *ResourcesStoreEntries) set(key string, value *core.MappingNode) {
	if value != nil {
		e.values[key] = value
	}
}

func (e *ResourcesStoreEntries) setString(key, value string) {
	e.values[key] = core.MappingNodeFromString(value)
}

// AddBackingIdentifier writes the one key a single-identifier backing resource
// needs, plus its region when the target supplied one.
func (e *ResourcesStoreEntries) AddBackingIdentifier(
	configKey string,
	identifier BackingIdentifier,
) {
	if e.has(configKey) {
		return
	}

	e.set(configKey, identifier.Value)
	if identifier.Region != "" {
		e.setString(configKey+StoreRegionSuffix, identifier.Region)
	}
}

// AddCacheConnection writes a cache's connection keys.
func (e *ResourcesStoreEntries) AddCacheConnection(
	configKey string,
	connection CacheConnection,
) {
	if e.has(configKey + storeHostSuffix) {
		return
	}

	e.set(configKey+storeHostSuffix, connection.Host)
	e.setString(configKey+storePortSuffix, strconv.Itoa(connection.Port))
	e.setString(configKey+storeTLSSuffix, strconv.FormatBool(connection.TLS))
	e.setString(configKey+storeClusterModeSuffix, strconv.FormatBool(connection.ClusterMode))
	e.setString(configKey+storeAuthModeSuffix, connection.AuthMode)
	e.set(configKey+storeAuthTokenSecretIDSuffix, connection.AuthTokenSecretID)
	e.set(configKey+storeUserSuffix, connection.User)
}

// AddDatabaseConnection writes a database's connection keys.
func (e *ResourcesStoreEntries) AddDatabaseConnection(
	configKey string,
	connection DatabaseConnection,
) {
	if e.has(configKey + storeHostSuffix) {
		return
	}

	e.set(configKey+storeHostSuffix, connection.Host)
	e.set(configKey+storeDatabaseSuffix, connection.Database)
	e.setString(configKey+storePortSuffix, strconv.Itoa(connection.Port))
	e.setString(configKey+storeEngineSuffix, connection.Engine)
	e.setString(configKey+storeUserSuffix, connection.User)
	e.setString(configKey+storeSSLSuffix, strconv.FormatBool(connection.SSL))
	e.setString(configKey+storeAuthModeSuffix, connection.AuthMode)
	e.set(configKey+storeReadHostSuffix, connection.ReadHost)
	e.set(configKey+storeCredentialsSecretIDSuffix, connection.CredentialsSecretID)
}

// SharedParent renders the accumulated entries as the store resource, or nil
// when nothing was written and no store is needed.
func (e *ResourcesStoreEntries) SharedParent(
	target ResourcesStoreTarget,
	appName string,
	storePath string,
) *transformutils.SharedParent {
	if e.Empty() {
		return nil
	}

	values := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	for key, value := range e.values {
		values.Fields[key] = value
	}

	return &transformutils.SharedParent{
		Key:          ResourcesStoreSharedParentKey,
		ResourceName: ResourcesStoreResourceName,
		ResourceType: target.StoreResourceType(),
		Annotations: core.MappingNodeFields(
			transformutils.AnnotationSourceAbstractName,
			core.MappingNodeFromString(ResourcesStoreResourceName),
			transformutils.AnnotationSourceAbstractType,
			core.MappingNodeFromString("celerity/config"),
			transformutils.AnnotationResourceCategory,
			core.MappingNodeFromString(transformutils.ResourceCategoryInfrastructure),
		),
		SeedSpec: target.StoreSeedSpec(appName, storePath, values),
	}
}

// StoreConfigKey derives the store key for a linked resource: its spec.name,
// falling back to its blueprint logical name. This mirrors the CLI's configKey
// derivation so the SDK's routing-file lookups resolve to these entries.
func StoreConfigKey(abstractName string, resource *schema.Resource) string {
	if resource != nil {
		if node, ok := pluginutils.GetValueByPath("$.name", resource.Spec); ok {
			if name := core.StringValue(node); name != "" {
				return name
			}
		}
	}

	return abstractName
}
