package config

import (
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

// Backend kind vocabulary for user celerity/config stores on aws-serverless.
// The internal resources namespace store uses awslambda.ResourceLinksStoreKind
// and is wired separately (direct env vars + direct IAM, never through the link mechanism below).
const (
	StoreKindParameterStore = "parameter-store"
	StoreKindSecretsManager = "secrets-manager"
)

// StoreWiring carries the facts a linked handler needs to wire a user
// celerity/config store: the CELERITY_CONFIG_<NS>_* env-var namespace segment,
// the backend kind, and the provider link-annotation coordinates.
type StoreWiring struct {
	// EnvNamespace is the sanitized upper-case <NS> segment used in the
	// CELERITY_CONFIG_<NS>_STORE_ID / _STORE_KIND env-var names, derived from the
	// store name (spec.name, falling back to the blueprint resource name).
	EnvNamespace string
	// Kind is the backend kind literal selected by the store's plaintext keys:
	// at least one plaintext key selects parameter-store, none selects
	// secrets-manager.
	Kind string
	// LinkAnnotationService is the <service> segment of the concrete link's
	// aws.lambda.<service>.* annotation keys ("ssm" or "secretsmanager").
	LinkAnnotationService string
	// ConcreteResourceName is the emitted concrete store resource's name, which
	// keys the link annotations (the provider resolves annotation keys against
	// the target resource's blueprint name).
	ConcreteResourceName string
}

// StoreWiringFor derives the wiring facts for the celerity/config resource with
// the given blueprint name, applying the same backend-selection rule the
// emitter uses.
func StoreWiringFor(configName string, resource *schema.Resource) StoreWiring {
	var spec *core.MappingNode
	if resource != nil {
		spec = resource.Spec
	}

	wiring := StoreWiring{
		EnvNamespace:          envNamespaceSegment(storeNameFor(configName, spec)),
		Kind:                  StoreKindParameterStore,
		LinkAnnotationService: "ssm",
		ConcreteResourceName:  paramTreeResourceName(configName),
	}

	if len(shared.StringSet("$.plaintext", spec)) == 0 {
		wiring.Kind = StoreKindSecretsManager
		wiring.LinkAnnotationService = "secretsmanager"
		wiring.ConcreteResourceName = secretResourceName(configName)
	}

	return wiring
}

func storeNameFor(configName string, spec *core.MappingNode) string {
	if spec == nil {
		return configName
	}
	specNameNode, ok := pluginutils.GetValueByPath("$.name", spec)
	specName := core.StringValue(specNameNode)
	if ok && specName != "" {
		return specName
	}
	return configName
}

// Env-var names are constrained to [A-Z0-9_], so the store name is upper-cased
// with every other character mapped to an underscore.
func envNamespaceSegment(storeName string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			return r
		default:
			return '_'
		}
	}, storeName)
}
