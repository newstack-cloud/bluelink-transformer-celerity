package bucket

import (
	"context"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"

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
	// External is set when the resource points at a bucket the target environment
	// already has and Celerity does not own, in which case no concrete resource
	// is emitted for it.
	External *shared.External
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
	external, _ := shared.ResolvedExternal("celerity/bucket", name, resource)

	return &ResolvedBucket{
		Name:     name,
		Resource: resource,
		External: external,
	}, nil
}
