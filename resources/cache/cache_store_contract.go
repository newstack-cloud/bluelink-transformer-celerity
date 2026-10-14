package cache

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

// What a deployed handler needs in order to open a connection to this cache.
//
// The SDK's CacheLayer reads these from the internal resources config namespace
// at cold start, keyed by the cache's configKey. They are exported from here
// rather than rebuilt by the store builder so that the naming stays owned by the
// package that emits the resources being named: a store entry pointing at a
// concrete resource this package never emitted resolves to nothing, and the
// handler fails at cold start with a missing config key rather than at deploy.

// ConcreteResourceName is the replication group a handler connects to, and the
// resource other packages reference this cache by.
func ConcreteResourceName(abstractName string) string {
	return replicationGroupResourceName(abstractName)
}

// AuthSecretResourceName is the Secrets Manager secret holding the generated
// Redis AUTH token.
//
// The token stays in the secret and the resources namespace carries only a
// reference to it. Copying the value into the parameter store would put a second
// copy of a rotatable credential somewhere nothing updates, and it would fail
// silently at the next rotation.
func AuthSecretResourceName(abstractName string) string {
	return authSecretResourceName(abstractName)
}

// IAMUserResourceName is the ElastiCache RBAC user a handler authenticates as
// under iam auth.
//
// The user is emitted by this package, so the store entry naming it is derived
// here rather than rebuilt by the store builder: an entry pointing at a user
// this package never emitted resolves to nothing, and the handler fails at cold
// start on a missing config key rather than at deploy.
func IAMUserResourceName(abstractName string) string {
	return iamUserResourceName(abstractName)
}

// HostValueKey is the blueprint value holding the cache endpoint address. It is
// a derived value because the endpoint differs by topology — the configuration
// endpoint when clustered, the primary endpoint otherwise.
func HostValueKey(abstractName string) string {
	return cacheHostKey(abstractName)
}

// Port is the port the replication group listens on.
func Port() int {
	return defaultCachePort
}

// TLSEnabled reports whether transit encryption is on, which the client needs to
// know to connect.
//
// Constant, because the emit sets transitEncryptionEnabled unconditionally: both
// auth modes require TLS, so there is no cache this can be false for. Stated as
// a function rather than inlined at the call site so the store and the emit
// still have one place to disagree if that ever stops being true.
func TLSEnabled() bool {
	return true
}

// AuthMode reads the auth mode from an abstract celerity/cache resource,
// defaulting to password. Mirrors cacheAuthMode, which takes a ResolvedCache the
// store builder does not have.
func AuthMode(resource *schema.Resource) string {
	if mode := specString(resource, "$.authMode"); mode != "" {
		return mode
	}
	return "password"
}

// ClusterModeEnabled reports whether the cache runs in cluster mode, which the
// client has to be told about separately from the endpoint it connects to.
func ClusterModeEnabled(resource *schema.Resource) bool {
	if resource == nil {
		return false
	}
	node, ok := pluginutils.GetValueByPath("$.clusterMode", resource.Spec)
	if !ok {
		return false
	}
	return core.BoolValue(node)
}

func specString(resource *schema.Resource, path string) string {
	if resource == nil {
		return ""
	}
	node, ok := pluginutils.GetValueByPath(path, resource.Spec)
	if !ok {
		return ""
	}
	return core.StringValue(node)
}
