package schedule

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

type ResolvedSchedule struct {
	Name     string
	Resource *schema.Resource
}

func (s *ResolvedSchedule) ResourceName() string {
	return s.Name
}

func (s *ResolvedSchedule) ResourceType() string {
	return "celerity/schedule"
}

func resolveSchedule(
	_ context.Context,
	_ *transformutils.Run,
	name string,
	resource *schema.Resource,
	_ linktypes.DeclaredLinkGraph,
	_ *schema.Blueprint,
) (transformutils.ResolvedResource, error) {
	return &ResolvedSchedule{Name: name, Resource: resource}, nil
}
