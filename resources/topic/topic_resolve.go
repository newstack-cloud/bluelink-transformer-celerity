package topic

import (
	"context"

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
	return &ResolvedTopic{
		Name:     name,
		Resource: resource,
	}, nil
}
