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
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedDatastore{
		Name:     name,
		Resource: resource,
	}, nil
}
