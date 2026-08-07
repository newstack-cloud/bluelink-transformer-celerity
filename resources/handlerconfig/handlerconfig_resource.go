package handlerconfig

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Handler Config.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:             "celerity/handlerConfig",
		Label:            "Celerity Handler Config",
		PlainTextSummary: "Shared configuration for a set of handlers in a Celerity application.",
		FormattedSummary: "Shared configuration for a set of handlers in a Celerity application.",
		PlainTextDescription: "The abstract resource type used to define shared configuration for a set of " +
			"handlers, such as runtime, memory, timeout and environment variables that " +
			"would otherwise be repeated across each handler.",
		FormattedDescription: "The abstract resource type used to define shared configuration for a set of " +
			"`celerity/handler` resources, such as runtime, memory, timeout and environment " +
			"variables that would otherwise be repeated across each handler.\n\n" +
			"See the " +
			"[`celerity/handlerConfig` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-handler-config) " +
			"for more information.",
		Schema:  handlerConfigResourceSchema(),
		Resolve: resolveHandlerConfig,
		// This is contributory-only, the handler reads this resource's spec directly
		// during inheritance (resolveInheritedSpec).
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitHandlerConfig),
		},
	}
}
