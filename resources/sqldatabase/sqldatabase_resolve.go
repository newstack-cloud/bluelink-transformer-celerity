package sqldatabase

import (
	"maps"

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

	// VPCSelectorLabels are the labels the VPC selects members by.
	//
	// Anything the transformer emits that must sit inside this VPC needs these
	// and, importantly, only these. Copying a resource's full label set instead
	// picks up the generated celerity.handler.uses.<db> label that every handler
	// selects on, which silently forms links to whatever carries it.
	VPCSelectorLabels map[string]string
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
		resolved.VPCSelectorLabels = linkSelectorLabels(vpcRes)
		break
	}

	return resolved, nil
}

// linkSelectorLabels returns the labels a resource selects link targets by.
func linkSelectorLabels(res *schema.Resource) map[string]string {
	if res == nil || res.LinkSelector == nil || res.LinkSelector.ByLabel == nil {
		return nil
	}
	labels := map[string]string{}
	maps.Copy(labels, res.LinkSelector.ByLabel.Values)
	return labels
}

func vpcSpec(res *schema.Resource, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, res.Spec)
	return node
}
