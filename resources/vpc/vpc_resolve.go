package vpc

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedVPC carries the abstract VPC through to the emit phase. Labels and
// linkSelector are preserved verbatim so placement links (flex/vpc -> handler,
// cache, sqlDatabase) resolve against the concrete resources.
type ResolvedVPC struct {
	Name     string
	Resource *schema.Resource
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
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedVPC{
		Name:     name,
		Resource: resource,
	}, nil
}
