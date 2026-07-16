package bucket

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedBucket carries the abstract bucket through to the emit phase. Labels
// are preserved verbatim so a handler's linkSelector can match the concrete
// bucket, so the resolved form is a thin wrapper over the resource.
type ResolvedBucket struct {
	Name     string
	Resource *schema.Resource
}

func (b *ResolvedBucket) ResourceName() string {
	return b.Name
}

func (b *ResolvedBucket) ResourceType() string {
	return "celerity/bucket"
}

func resolveBucket(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedBucket{
		Name:     name,
		Resource: resource,
	}, nil
}
