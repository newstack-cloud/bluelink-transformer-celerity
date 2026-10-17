package handler

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Resource defines the abstract resource for the Celerity handler. Each deploy
// target's implementation is passed in by the registry rather than referenced
// here, so this package stays free of any one target.
func Resource(
	implementations ...shared.TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	return shared.ApplyTargetImplementations(&transformerv1.AbstractResourceDefinition{
		Type:  "celerity/handler",
		Label: "Celerity Handler",
		PlainTextSummary: "A handler that processes requests, messages or events in a Celerity " +
			"application.",
		FormattedSummary: "A handler that processes requests, messages or events in a Celerity " +
			"application.",
		PlainTextDescription: "The abstract resource type used to define a handler that can process HTTP " +
			"requests, WebSocket messages, events from queues and message brokers, " +
			"scheduled events or events from cloud services, along with steps in a " +
			"workflow.",
		FormattedDescription: "The abstract resource type used to define a handler that can process HTTP " +
			"requests, WebSocket messages, events from queues and message brokers, " +
			"scheduled events or events from cloud services, along with steps in a " +
			"workflow.\n\n" +
			"See the " +
			"[`celerity/handler` specification](https://www.celerityframework.io/docs/framework/applications/resources/celerity-handler) " +
			"for more information.",
		Schema:  handlerResourceSchema(),
		Resolve: resolveHandler,
	}, implementations)
}
