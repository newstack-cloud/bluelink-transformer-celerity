package handlerconfig

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Handler Config.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:    "celerity/handlerConfig",
		Label:   "Celerity Handler Config",
		Schema:  handlerConfigResourceSchema(),
		Resolve: resolveHandlerConfig,
		// This is contributory-only, the handler reads this resource's spec directly
		// during inheritance (resolveInheritedSpec).
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitHandlerConfig),
		},
	}
}
