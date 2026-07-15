package links

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

func HandlerToHandlerConfigLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/handler",
		ResourceTypeB:    "celerity/handlerConfig",
		PlainTextSummary: "Applies shared configuration defaults to a handler.",
		FormattedSummary: "Applies shared defaults from a `celerity/handlerConfig` to a `celerity/handler`.",
		PlainTextDescription: "The handler inherits configuration defaults such as runtime, memory, " +
			"timeout, tracing and environment variables from the linked handlerConfig. Values set directly " +
			"on the handler take precedence. A handler inherits from at most one handlerConfig.",
		FormattedDescription: "The handler inherits configuration defaults such as runtime, memory, " +
			"timeout, tracing and environment variables from the linked `celerity/handlerConfig`. Values " +
			"set directly on the handler take precedence. A handler inherits from at most one " +
			"`celerity/handlerConfig`.",
		// A handler inherits from at most one handlerConfig.
		CardinalityA: provider.LinkCardinality{Min: 0, Max: 1},
	}
}
