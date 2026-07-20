package awslambda

import (
	"maps"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

type EnvInput struct {
	Platform      string
	DeployTarget  string
	HandlerID     *core.MappingNode
	EventSource   string
	RoutingTag    string
	HasRoutingTag bool
	Tracing       bool
	// ResourceLinksStorePath is the SSM Parameter Store path prefix of the internal
	// resources namespace config store, set for handlers that link to at least one
	// backing resource the SDK resolves through the store. Empty otherwise.
	ResourceLinksStorePath string
	// UserConfigStores describes the user-declared celerity/config stores linked
	// to the handler; one CELERITY_CONFIG_<NS>_STORE_ID/_STORE_KIND pair is
	// stamped per store. The internal resources namespace store is wired through
	// ResourceLinksStorePath instead, never through this list.
	UserConfigStores []UserConfigStore
	UserEnv          map[string]*core.MappingNode
}

// UserConfigStore describes one linked user celerity/config store for env-var
// stamping.
type UserConfigStore struct {
	// EnvNamespace is the sanitized upper-case <NS> segment of the
	// CELERITY_CONFIG_<NS>_* env-var names.
	EnvNamespace string
	// StoreID is the store-identifier value. The handler passes the abstract
	// ${resources.<config>.spec.id} reference so the resource-property rewriter
	// resolves it to the emitted store's derived id value (SSM path prefix or
	// secret ARN).
	StoreID *core.MappingNode
	// Kind is the backend kind literal ("parameter-store" or "secrets-manager").
	Kind string
}

// ResourceLinksStoreKind is the config-store kind vocabulary value for the internal
// resources namespace store; it is always AWS Systems Manager Parameter Store.
const ResourceLinksStoreKind = "parameter-store"

// ConfigStoreIDEnvVarName returns the name of the env var carrying a config
// store's identifier for the given namespace segment. The same name is used as
// the store link's envVarName annotation value so the provider link maintains
// the variable the transformer stamps.
func ConfigStoreIDEnvVarName(envNamespace string) string {
	return "CELERITY_CONFIG_" + envNamespace + "_STORE_ID"
}

// ConfigStoreKindEnvVarName returns the name of the env var naming a config
// store's backend kind for the given namespace segment.
func ConfigStoreKindEnvVarName(envNamespace string) string {
	return "CELERITY_CONFIG_" + envNamespace + "_STORE_KIND"
}

func BuildEnvironmentVariables(input *EnvInput) *core.MappingNode {
	vars := map[string]*core.MappingNode{
		"CELERITY_PLATFORM":      core.MappingNodeFromString(input.Platform),
		"CELERITY_DEPLOY_TARGET": core.MappingNodeFromString(input.DeployTarget),
		"CELERITY_HANDLER_ID":    input.HandlerID,
		"CELERITY_HANDLER_TYPE":  core.MappingNodeFromString(input.EventSource),
	}

	if input.HasRoutingTag {
		vars["CELERITY_HANDLER_TAG"] = core.MappingNodeFromString(input.RoutingTag)
	}

	// Internal resources namespace config store discovery: the SDK reads the store
	// at CELERITY_CONFIG_RESOURCES_STORE_ID (the path prefix) using the backend named
	// by CELERITY_CONFIG_RESOURCES_STORE_KIND to resolve linked resources' physical
	// ids at runtime. Only set for handlers with backing resource links.
	if input.ResourceLinksStorePath != "" {
		vars["CELERITY_CONFIG_RESOURCES_STORE_ID"] = core.MappingNodeFromString(input.ResourceLinksStorePath)
		vars["CELERITY_CONFIG_RESOURCES_STORE_KIND"] = core.MappingNodeFromString(ResourceLinksStoreKind)
	}

	// User celerity/config store discovery: one CELERITY_CONFIG_<NS>_STORE_ID/_KIND
	// pair per linked store. The kind must always be explicit because the SDK
	// defaults an unset namespace kind to secrets-manager, not to the target's
	// default.
	for _, store := range input.UserConfigStores {
		vars[ConfigStoreIDEnvVarName(store.EnvNamespace)] = store.StoreID
		vars[ConfigStoreKindEnvVarName(store.EnvNamespace)] = core.MappingNodeFromString(store.Kind)
	}

	if input.Tracing {
		vars["CELERITY_TELEMETRY_ENABLED"] = core.MappingNodeFromString("true")
	}

	_, userSetLogFormat := input.UserEnv["CELERITY_LOG_FORMAT"]

	maps.Copy(vars, input.UserEnv)
	if !userSetLogFormat {
		vars["CELERITY_LOG_FORMAT"] = core.MappingNodeFromString("json")
	}

	return &core.MappingNode{
		Fields: vars,
	}
}
