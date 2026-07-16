package sqldatabase

import (
	"context"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedSQLDatabase carries the abstract database plus the placement VPC found
// from the link graph. Databases require VPC placement; the emit references the
// concrete flex VPC's computed subnets/security-groups and validates the VPC's
// preset provides enough private subnets (RDS needs at least two AZs).
type ResolvedSQLDatabase struct {
	Name     string
	Resource *schema.Resource

	VPCName       string
	VPCPreset     string
	VPCReferenced bool
}

func (d *ResolvedSQLDatabase) ResourceName() string {
	return d.Name
}

func (d *ResolvedSQLDatabase) ResourceType() string {
	return "celerity/sqlDatabase"
}

func resolveSQLDatabase(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	resolved := &ResolvedSQLDatabase{
		Name:     name,
		Resource: resource,
	}

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
		preset := core.StringValue(vpcSpec(vpcRes, "$.preset"))
		if preset == "" {
			preset = "standard"
		}
		resolved.VPCPreset = preset
		break
	}

	return resolved, nil
}

func vpcSpec(res *schema.Resource, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, res.Spec)
	return node
}
