package consumer

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Consumer.
func Resource() *transformerv1.AbstractResourceDefinition {
	return &transformerv1.AbstractResourceDefinition{
		Type:    "celerity/consumer",
		Label:   "Celerity Consumer",
		Schema:  consumerResourceSchema(),
		Resolve: resolveConsumer,
		// Contributory-only: the handler absorbs the consumer and emits the
		// concrete event-source trigger. The framework still requires an Emitters
		// entry wherever a declarative pipeline field such as Resolve is set, so
		// this one emits nothing.
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitConsumer),
		},
	}
}
