package api

import (
	"context"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedAPI carries the abstract API plus the handlers it links to. The linked
// handlers are needed at emit time to back a custom auth guard's REQUEST
// authorizer with the concrete Lambda function that implements the guard.
type ResolvedAPI struct {
	Name     string
	Resource *schema.Resource
	// Handlers is the set of celerity/handler resources this API links to (by
	// label). Route wiring is stamped on the handler side; the API only needs
	// these to resolve custom-guard authorizer targets.
	Handlers []*types.LinkedResource
}

func (a *ResolvedAPI) ResourceName() string {
	return a.Name
}

func (a *ResolvedAPI) ResourceType() string {
	return "celerity/api"
}

func resolveAPI(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	resolved := &ResolvedAPI{
		Name:     name,
		Resource: resource,
	}

	for _, edge := range linkGraph.EdgesFrom(name) {
		if edge.TargetType != "celerity/handler" {
			continue
		}
		resolved.Handlers = append(resolved.Handlers, &types.LinkedResource{
			Name:     edge.Target,
			Resource: shared.GetResourceByName(blueprint, edge.Target),
			Edge:     edge,
		})
	}

	return resolved, nil
}
