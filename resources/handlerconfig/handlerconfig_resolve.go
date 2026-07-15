package handlerconfig

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

type ResolvedHandlerConfig struct {
	Name     string
	Resource *schema.Resource
}

func (h *ResolvedHandlerConfig) ResourceName() string {
	return h.Name
}

func (h *ResolvedHandlerConfig) ResourceType() string {
	return "celerity/handlerConfig"
}

func resolveHandlerConfig(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedHandlerConfig{
		Name:     name,
		Resource: resource,
	}, nil
}
