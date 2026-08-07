package handler

import (
	"fmt"
	"slices"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Keyed by the components that can carry a VPC placement; the value sets the
// subnet tier for the handlers inheriting it.
var parentSubnetTypeAnnotations = map[string]string{
	"celerity/api":      AnnotationKeyAPIVPCSubnetType,
	"celerity/consumer": AnnotationKeyConsumerVPCSubnetType,
	"celerity/schedule": AnnotationKeyScheduleVPCSubnetType,
}

// The same components' outbound-reach annotations, cascading on the same terms.
var parentEgressAnnotations = map[string]string{
	"celerity/api":      AnnotationKeyAPIVPCEgress,
	"celerity/consumer": AnnotationKeyConsumerVPCEgress,
	"celerity/schedule": AnnotationKeyScheduleVPCEgress,
}

type inheritedVPCPlacement struct {
	vpc *types.LinkedResource
	// The application component the placement was inherited from.
	parentName string
	// Empty when the component sets no subnet-type annotation.
	subnetType string
	// Empty when the component sets no egress annotation, which is distinct from
	// it setting "none".
	egress string
	// The label key/value pairs the VPC's link selector matched the component
	// on, carried onto the emitted function so the concrete VPC-to-compute link
	// selects it too.
	selectorLabels map[string]string
}

// On containerised targets a celerity/api, celerity/consumer or celerity/schedule
// is itself the unit of compute and the VPC places it directly; on serverless
// targets the handler is the compute, so a placement on the component cascades to
// its handlers. A direct celerity/vpc -> celerity/handler link always wins, and
// inheriting different VPCs from several components is unresolvable.
func resolveInheritedVPCPlacement(
	target *ResolvedHandler,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) error {
	if target.VPC != nil {
		return nil
	}

	placements := collectInheritedPlacements(target, linkGraph, blueprint)
	if len(placements) == 0 {
		return nil
	}

	if err := conflictingPlacementsError(target.Name, placements); err != nil {
		return err
	}

	placement := placements[0]
	target.VPC = placement.vpc
	target.InheritedVPCFrom = placement.parentName
	target.inheritedVPCSubnetType = placement.subnetType
	target.inheritedVPCEgress = placement.egress
	target.inheritedVPCSelectorLabels = placement.selectorLabels

	return nil
}

func collectInheritedPlacements(
	target *ResolvedHandler,
	linkGraph linktypes.DeclaredLinkGraph,
	blueprint *schema.Blueprint,
) []*inheritedVPCPlacement {
	placements := []*inheritedVPCPlacement{}
	for _, parent := range vpcPlacementParents(target) {
		for _, edge := range linkGraph.EdgesTo(parent.Name) {
			if edge.SourceType != "celerity/vpc" {
				continue
			}
			placements = append(placements, newInheritedPlacement(parent, edge, blueprint))
		}
	}

	// Edge order from the link graph is not guaranteed, so the winning placement
	// and any conflict message must not depend on it.
	slices.SortFunc(placements, func(a, b *inheritedVPCPlacement) int {
		return strings.Compare(a.parentName, b.parentName)
	})

	return placements
}

func newInheritedPlacement(
	parent *types.LinkedResource,
	edge *linktypes.ResolvedLink,
	blueprint *schema.Blueprint,
) *inheritedVPCPlacement {
	return &inheritedVPCPlacement{
		vpc: &types.LinkedResource{
			Name:     edge.Source,
			Resource: shared.GetResourceByName(blueprint, edge.Source),
			Edge:     edge,
		},
		parentName:     parent.Name,
		subnetType:     parentAnnotation(parent, parentSubnetTypeAnnotations),
		egress:         parentAnnotation(parent, parentEgressAnnotations),
		selectorLabels: selectorLabels(edge),
	}
}

func vpcPlacementParents(target *ResolvedHandler) []*types.LinkedResource {
	parents := []*types.LinkedResource{}
	if target.APILink != nil {
		parents = append(parents, target.APILink)
	}
	parents = append(parents, target.Consumers...)
	parents = append(parents, target.Schedules...)

	return parents
}

// Reads one of the cascading VPC annotations off the component the placement was
// inherited from, empty when the component does not carry it.
func parentAnnotation(parent *types.LinkedResource, annotationKeys map[string]string) string {
	if parent.Resource == nil || parent.Edge == nil {
		return ""
	}

	annotationKey, ok := annotationKeys[parent.Edge.SourceType]
	if !ok {
		return ""
	}

	value, ok := transformutils.GetAnnotation(parent.Resource, annotationKey, "")
	if !ok {
		return ""
	}

	return core.StringValue(value)
}

// Selector keys take the form "label::<key>:<value>" as produced by the link
// graph; anything else is ignored rather than guessed at.
func selectorLabels(edge *linktypes.ResolvedLink) map[string]string {
	labels := map[string]string{}
	for _, selectorKey := range edge.SelectorKeys {
		labelPair, found := strings.CutPrefix(selectorKey, "label::")
		if !found {
			continue
		}
		key, value, found := strings.Cut(labelPair, ":")
		if !found || key == "" {
			continue
		}
		labels[key] = value
	}

	return labels
}

func conflictingPlacementsError(handlerName string, placements []*inheritedVPCPlacement) error {
	sources := map[string]string{}
	for _, placement := range placements {
		sources[placement.vpc.Name] = placement.parentName
	}
	if len(sources) < 2 {
		return nil
	}

	descriptions := []string{}
	for vpcName, parentName := range sources {
		descriptions = append(descriptions, fmt.Sprintf("%q (from %q)", vpcName, parentName))
	}
	slices.Sort(descriptions)

	return fmt.Errorf(
		"celerity/handler %q inherits conflicting VPC placements: %s; a handler belongs to at "+
			"most one celerity/vpc, so link the handler to the intended celerity/vpc directly to "+
			"resolve the conflict",
		handlerName,
		strings.Join(descriptions, ", "),
	)
}
