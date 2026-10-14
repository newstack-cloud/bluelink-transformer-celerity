package datastore

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedDatastore carries the abstract data store through to the emit phase.
// Labels are preserved verbatim so a handler's linkSelector can match the
// concrete table, so the resolved form is a thin wrapper over the resource.
type ResolvedDatastore struct {
	Name     string
	Resource *schema.Resource
	// Consumed reports whether a celerity/consumer reads this data store's change
	// stream, which is what decides if the table is created with streams on.
	Consumed bool
}

func (d *ResolvedDatastore) ResourceName() string {
	return d.Name
}

func (d *ResolvedDatastore) ResourceType() string {
	return "celerity/datastore"
}

func resolveDatastore(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedDatastore{
		Name:     name,
		Resource: resource,
		Consumed: hasStreamConsumer(name, linkGraph),
	}, nil
}

// Reports whether a celerity/consumer is linked to the data store, meaning something
// reads its change stream.
//
// The stream has to exist before any link reads its ARN, and a link cannot wait on a
// capability it establishes itself through a contribution, so streams are turned on
// when the table is created rather than contributed to it afterwards.
func hasStreamConsumer(name string, linkGraph linktypes.DeclaredLinkGraph) bool {
	if linkGraph == nil {
		return false
	}

	for _, edge := range linkGraph.EdgesFrom(name) {
		if edge != nil && edge.TargetType == "celerity/consumer" {
			return true
		}
	}

	return false
}
