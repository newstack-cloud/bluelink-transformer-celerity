package consumer

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedConsumer is the resolved form of a celerity/consumer. The consumer is
// contributory-only on aws-serverless: the handler it links to absorbs it and
// wires up the concrete event-source trigger, so this carries just enough for the
// aggregator to identify and drop it.
type ResolvedConsumer struct {
	Name     string
	Resource *schema.Resource
}

func (c *ResolvedConsumer) ResourceName() string {
	return c.Name
}

func (c *ResolvedConsumer) ResourceType() string {
	return "celerity/consumer"
}

// resolveConsumer is a thin pass-through: the source-to-handler wiring is resolved
// on the handler side (which owns the link graph and emits the triggers).
func resolveConsumer(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedConsumer{Name: name, Resource: resource}, nil
}
