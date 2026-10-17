package vpc

import (
	"context"
	"sort"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Placement is one resource placed in this VPC, in abstract terms. Each deploy
// target derives from it whatever network identities it has to provision: the
// placed resource is reached over the network, so it needs an identity of its
// own rather than sharing the VPC's.
type Placement struct {
	// ResourceName is the abstract resource placed in the VPC.
	ResourceName string
	// ResourceType is its celerity resource type.
	ResourceType string
}

// ResolvedVPC carries the abstract VPC through to the emit phase. Labels and
// linkSelector are preserved verbatim so placement links (flex/vpc -> handler,
// cache, sqlDatabase) resolve against the concrete resources.
type ResolvedVPC struct {
	Name     string
	Resource *schema.Resource
	// Placements are the resources placed in this VPC, in a stable order.
	// Resolved here because the link graph is only available to this phase.
	Placements []Placement
}

func (v *ResolvedVPC) ResourceName() string {
	return v.Name
}

func (v *ResolvedVPC) ResourceType() string {
	return "celerity/vpc"
}

func resolveVPC(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedVPC{
		Name:       name,
		Resource:   resource,
		Placements: placements(name, linkGraph),
	}, nil
}

// Sorted so anything derived from this is stable: an order that varied with
// graph traversal would show as a change on every stage.
func placements(vpcName string, linkGraph linktypes.DeclaredLinkGraph) []Placement {
	if linkGraph == nil {
		return nil
	}

	placed := []Placement{}
	for _, edge := range linkGraph.EdgesFrom(vpcName) {
		if edge == nil {
			continue
		}
		placed = append(placed, Placement{
			ResourceName: edge.Target,
			ResourceType: edge.TargetType,
		})
	}

	sort.Slice(placed, func(i, j int) bool {
		if placed[i].ResourceName != placed[j].ResourceName {
			return placed[i].ResourceName < placed[j].ResourceName
		}

		return placed[i].ResourceType < placed[j].ResourceType
	})

	return placed
}
