package awslambda

import (
	"maps"
	"path"
	"strings"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

type EnvInput struct {
	Platform     string
	DeployTarget string
	HandlerID    *core.MappingNode
	// Runtime is the concrete Lambda runtime identifier (e.g. "nodejs22.x"). It
	// decides how the app module is located at cold start, which differs per
	// language.
	Runtime       string
	EventSource   string
	RoutingTag    string
	HasRoutingTag bool
	// RoutingKey is the message-body field a routed consumer selects its handler
	// on. Set only for a function that serves a whole consumer rather than one of
	// its handlers, which is the only case where anything has to be routed.
	RoutingKey string
	Tracing    bool
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
	// Namespace is the store's name as authored, which is the name handlers
	// resolve it by. It is carried explicitly because EnvNamespace is lossy:
	// upper-casing and replacing anything outside [A-Z0-9_] means the original
	// cannot be recovered from it.
	Namespace string
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

// ConfigStoreNamespaceEnvVarName returns the name of the env var carrying a config
// store's name as authored.
//
// Env-var names cannot hold the name itself as they are upper-cased and stripped of
// anything outside [A-Z0-9_], so "appConfig" becomes "APPCONFIG" and "my-config"
// becomes "MY_CONFIG". An SDK reading only the env-var name has to guess the original
// back, and lower-casing is the only guess available, which resolves "appConfig" to
// "appconfig" and registers the config namespace under a name no handler asks for.
// The failure is at cold start, with the namespace simply absent.
//
// Passing the authored name explicitly removes the guess.
func ConfigStoreNamespaceEnvVarName(envNamespace string) string {
	return "CELERITY_CONFIG_" + envNamespace + "_NAMESPACE"
}

func BuildEnvironmentVariables(input *EnvInput) *core.MappingNode {
	vars := map[string]*core.MappingNode{
		"CELERITY_PLATFORM":      core.MappingNodeFromString(input.Platform),
		"CELERITY_DEPLOY_TARGET": core.MappingNodeFromString(input.DeployTarget),
		"CELERITY_HANDLER_ID":    input.HandlerID,
		"CELERITY_HANDLER_TYPE":  core.MappingNodeFromString(input.EventSource),
	}

	// Where the SDK imports the application's root module from at cold start.
	// Without it the adapter loads and then has nothing to bootstrap, failing
	// every invocation with "Cannot discover module". The dev container sets this
	// for the local runtime; a deployed function needs it just as much.
	if modulePath := appModulePath(input.Runtime, input.HandlerID); modulePath != "" {
		vars["CELERITY_MODULE_PATH"] = core.MappingNodeFromString(modulePath)
	}

	if input.HasRoutingTag {
		vars["CELERITY_HANDLER_TAG"] = core.MappingNodeFromString(input.RoutingTag)
	}

	// Which handler to invoke is addressed by the tag above, as for every handler
	// type. This says how to read a route out of a message, which the tag cannot
	// carry because it comes from the consumer's declaration rather than from the
	// handler.
	if input.RoutingKey != "" {
		vars["CELERITY_ROUTING_KEY"] = core.MappingNodeFromString(input.RoutingKey)
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
		vars[ConfigStoreNamespaceEnvVarName(store.EnvNamespace)] =
			core.MappingNodeFromString(store.Namespace)
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

// Where Lambda unpacks the function bundle, and the working
// directory the runtime starts in.
const lambdaTaskRoot = "/var/task"

// Locates the application's root module inside the deployed
// bundle.
//
// The handler id is "<module>.<Class>.<method>", so its first segment is the
// module file's base name, and the build places the compiled sources at the root
// of the archive. An id carrying an unresolved substitution yields an empty
// string here rather than a path built from one, a wrong path fails at cold
// start with the module missing, which is far from the reference that could not
// be read.
//
// Only Node is handled. Other language adapters imports their modules differently, and
// guessing a path for it would be worse than leaving it to that adapter.
func appModulePath(runtime string, handlerID *core.MappingNode) string {
	if !strings.HasPrefix(runtime, "nodejs") {
		return ""
	}

	id := core.StringValue(handlerID)
	if id == "" {
		return ""
	}

	module, _, found := strings.Cut(id, ".")
	if !found || module == "" {
		return ""
	}

	return path.Join(lambdaTaskRoot, module+".js")
}
