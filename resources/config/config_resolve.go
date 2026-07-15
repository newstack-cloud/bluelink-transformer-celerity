package config

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

type ResolvedConfig struct {
	Name      string
	StoreName string
	Resource  *schema.Resource
}

func (c *ResolvedConfig) ResourceName() string {
	return c.Name
}

func (c *ResolvedConfig) ResourceType() string {
	return "celerity/config"
}

func resolveConfig(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	storeName := name
	specNameNode, ok := pluginutils.GetValueByPath("$.name", resource.Spec)
	specName := core.StringValue(specNameNode)
	if ok && specName != "" {
		storeName = specName
	}

	return &ResolvedConfig{
		Name:      name,
		StoreName: storeName,
		Resource:  resource,
	}, nil
}
