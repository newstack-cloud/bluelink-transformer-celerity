package cache

import (
	"context"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedCache carries the abstract cache plus the placement VPC discovered
// from the link graph. Caches require VPC placement; the emit references the
// concrete flex VPC's computed subnets/security-groups and validates the VPC's
// preset provides private subnets.
type ResolvedCache struct {
	Name     string
	Resource *schema.Resource

	// VPCName is the abstract name of the placement VPC (the source of the
	// inbound vpc->cache link), or "" when no VPC is linked.
	VPCName string
	// VPCPreset is the placement VPC's preset (managed mode only), for
	// preset-suitability validation.
	VPCPreset string
	// VPCReferenced is true when the placement VPC is in referenced mode, where
	// topology is unknown at transform time and preset validation is skipped.
	VPCReferenced bool
}

func (c *ResolvedCache) ResourceName() string {
	return c.Name
}

func (c *ResolvedCache) ResourceType() string {
	return "celerity/cache"
}

func resolveCache(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	resolved := &ResolvedCache{
		Name:     name,
		Resource: resource,
	}

	// The VPC declares the placement link (vpc -> cache), so it is an inbound
	// edge to the cache.
	for _, edge := range linkGraph.EdgesTo(name) {
		if edge.SourceType != "celerity/vpc" {
			continue
		}
		resolved.VPCName = edge.Source
		vpcRes := shared.GetResourceByName(blueprint, edge.Source)
		if vpcRes == nil {
			break
		}
		mode, _ := pluginutils.GetValueByPath("$.mode", vpcRes.Spec)
		resolved.VPCReferenced = core.StringValue(mode) == "referenced"
		preset := core.StringValue(mustGetSpec(vpcRes, "$.preset"))
		if preset == "" {
			preset = "standard"
		}
		resolved.VPCPreset = preset
		break
	}

	return resolved, nil
}

func mustGetSpec(res *schema.Resource, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, res.Spec)
	return node
}
