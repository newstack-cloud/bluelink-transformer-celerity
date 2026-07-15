package queue

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedQueue carries the abstract queue through to the emit phase. A queue
// has no inbound/outbound link data the emit needs beyond its own metadata
// (labels + linkSelector are preserved as-is), so the resolved form is a thin
// wrapper over the resource.
type ResolvedQueue struct {
	Name     string
	Resource *schema.Resource
}

func (q *ResolvedQueue) ResourceName() string {
	return q.Name
}

func (q *ResolvedQueue) ResourceType() string {
	return "celerity/queue"
}

func resolveQueue(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedQueue{
		Name:     name,
		Resource: resource,
	}, nil
}
