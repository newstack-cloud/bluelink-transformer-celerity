package handler

import (
	"fmt"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

// What each managed VPC preset's topology provides, which is what decides whether
// a placement can be honoured at all.
//
// These are properties of the abstract preset rather than of any one cloud, so the
// checks built on them hold for every deploy target. Anything reasoning from how a
// particular platform attaches compute to a network belongs beside that target's
// emit path instead.
type presetTopology struct {
	privateSubnets bool
	publicSubnets  bool
	// An outbound path for a workload in a private subnet. Public subnets never
	// carry one: the workload reaches out through the internet gateway instead.
	natGateway bool
}

var presetTopologies = map[string]presetTopology{
	"standard":     {privateSubnets: true, publicSubnets: true, natGateway: true},
	"light":        {privateSubnets: true, publicSubnets: true, natGateway: true},
	"public":       {privateSubnets: false, publicSubnets: true, natGateway: false},
	"light-public": {privateSubnets: false, publicSubnets: true, natGateway: false},
	"isolated":     {privateSubnets: true, publicSubnets: false, natGateway: false},
}

const defaultVPCPreset = "standard"

// Reports the placements a managed VPC's topology cannot honour, before the
// deploy target fails on them in terms of its own primitives.
//
// Silent on a referenced VPC as its topology belongs to another blueprint and is
// not knowable here, so every check would be a guess.
func VPCPlacementDiagnostics(r *ResolvedHandler) []*core.Diagnostic {
	preset, known := resolvedPresetTopology(r)
	if !known {
		return nil
	}

	diagnostics := []*core.Diagnostic{}

	// The tier is checked first and short-circuits the rest. When the VPC has no
	// subnets of the requested tier there is no placement to reason about, and a
	// second diagnostic about its egress would only describe a consequence of the
	// first.
	if diagnostic := subnetTierDiagnostic(r, preset); diagnostic != nil {
		return append(diagnostics, diagnostic)
	}

	if diagnostic := noOutboundPathDiagnostic(r, preset); diagnostic != nil {
		diagnostics = append(diagnostics, diagnostic)
	}

	return diagnostics
}

// Rejects a placement into a subnet tier the preset does not provide.
//
// This fires for the default tier as well as an explicit one, unlike the egress
// checks. The asymmetry is deliberate and runs the other way here: an unset
// egress means "give me what the network has", which is always satisfiable, while
// an unset subnet type still resolves to private, and a private placement into a
// VPC with no private subnets cannot be honoured however it was arrived at. The
// message names the annotation so the fix is visible either way.
func subnetTierDiagnostic(r *ResolvedHandler, preset presetTopology) *core.Diagnostic {
	if r.VPCSubnetType == SubnetTypePrivate && !preset.privateSubnets {
		return placementDiagnostic(r, fmt.Sprintf(
			"celerity/handler %q is placed in celerity/vpc %q, whose %q preset provides no "+
				"private subnets, but its subnet type resolves to %q; set %s to %q on the handler, "+
				"or use a preset with private subnets such as \"standard\"",
			r.Name, r.VPC.Name, presetName(r), SubnetTypePrivate,
			AnnotationKeyVPCSubnetType, SubnetTypePublic,
		))
	}

	if r.VPCSubnetType == SubnetTypePublic && !preset.publicSubnets {
		return placementDiagnostic(r, fmt.Sprintf(
			"celerity/handler %q is placed in celerity/vpc %q, whose %q preset provides no "+
				"public subnets, but its subnet type resolves to %q; set %s to %q on the handler, "+
				"or use a preset with public subnets such as \"standard\"",
			r.Name, r.VPC.Name, presetName(r), SubnetTypePublic,
			AnnotationKeyVPCSubnetType, SubnetTypePrivate,
		))
	}

	return nil
}

// Rejects egress the network has no path to deliver at all.
//
// Only when the author asked for it. Left unset the reach resolves to whatever
// the topology provides, which on a network with no outbound path is nothing, and
// that is a fine answer to a question nobody asked.
func noOutboundPathDiagnostic(r *ResolvedHandler, preset presetTopology) *core.Diagnostic {
	if r.VPCEgress == "" || r.VPCEgress == VPCEgressNone {
		return nil
	}
	if outboundPathExists(r, preset) {
		return nil
	}

	return placementDiagnostic(r, fmt.Sprintf(
		"celerity/handler %q requests %s of %q, but it is placed in celerity/vpc %q, whose %q "+
			"preset provides no outbound path for a handler in a %q subnet; remove the "+
			"annotation, set it to %q, or use a preset that provides one",
		r.Name, AnnotationKeyVPCEgress, r.VPCEgress, r.VPC.Name, presetName(r),
		r.VPCSubnetType, VPCEgressNone,
	))
}

// Whether the network can carry the handler's traffic outwards at all, matching
// how the deploy target resolves the same question from the deployed topology: a
// private subnet reaches out through a NAT gateway, a public one through the
// internet gateway that comes with having public subnets.
func outboundPathExists(r *ResolvedHandler, preset presetTopology) bool {
	if r.VPCSubnetType == SubnetTypePublic {
		return preset.publicSubnets
	}

	return preset.natGateway
}

// The topology of the preset the handler is placed into, and whether it is
// knowable at all. Unknown for a handler that is not placed, for a referenced VPC,
// and for a preset this build does not recognise, which is a schema-level error
// reported elsewhere rather than something to guess at here.
func resolvedPresetTopology(r *ResolvedHandler) (presetTopology, bool) {
	if r.VPC == nil || r.VPC.Resource == nil {
		return presetTopology{}, false
	}
	if vpcSpecString(r.VPC.Resource, "$.mode") == "referenced" {
		return presetTopology{}, false
	}

	preset, known := presetTopologies[presetName(r)]

	return preset, known
}

func presetName(r *ResolvedHandler) string {
	preset := vpcSpecString(r.VPC.Resource, "$.preset")
	if preset == "" {
		return defaultVPCPreset
	}

	return preset
}

func vpcSpecString(resource *schema.Resource, path string) string {
	node, _ := pluginutils.GetValueByPath(path, resource.Spec)

	return strings.TrimSpace(core.StringValue(node))
}

func placementDiagnostic(r *ResolvedHandler, message string) *core.Diagnostic {
	return &core.Diagnostic{
		Level:   core.DiagnosticLevelError,
		Message: message,
		Range:   DiagnosticRangeFor(r),
	}
}

func DiagnosticRangeFor(r *ResolvedHandler) *core.DiagnosticRange {
	return shared.DiagnosticRange(shared.ResourceMeta(r.Resource))
}
