package topic

import (
	"context"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolvedTopic carries the abstract topic through to the emit phase. Labels are
// preserved verbatim so a handler's (or bucket's) linkSelector can match the
// concrete topic, so the resolved form is a thin wrapper over the resource.
type ResolvedTopic struct {
	Name     string
	Resource *schema.Resource
	// External is set when the resource points at a topic the target environment
	// already has and Celerity does not own, in which case no concrete resource
	// is emitted for it. Nil for a managed resource, and nil when the mode could
	// not be resolved, which the emit reports.
	External *shared.External
}

func (t *ResolvedTopic) ResourceName() string {
	return t.Name
}

func (t *ResolvedTopic) ResourceType() string {
	return "celerity/topic"
}

func resolveTopic(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	external, _ := shared.ResolvedExternal("celerity/topic", name, resource)

	return &ResolvedTopic{
		Name:     name,
		Resource: resource,
		External: external,
	}, nil
}
