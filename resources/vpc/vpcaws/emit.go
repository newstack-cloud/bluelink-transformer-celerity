package vpcaws

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	defaultPreset    = "standard"
	defaultCIDRBlock = "10.0.0.0/16"

	// Celerity abstract modes.
	modeReferenced = "referenced"

	// aws/flex/vpc concrete modes.
	flexModeCreate    = "create"
	flexModeReference = "reference"
)

// Emit builds the VPC a celerity/vpc maps to, for the named AWS deploy target.
func Emit(
	_ context.Context,
	run *transformutils.Run,
	target string,
	r *vpc.ResolvedVPC,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	if diagnostics := shared.DeferredValueErrors(
		target,
		"vpc", r.Name, r.Resource,
		shared.StructuralField{Name: "mode", Value: specGet(r, "$.mode")},
		shared.StructuralField{Name: "preset", Value: specGet(r, "$.preset")},
	); len(diagnostics) > 0 {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	spec, err := buildVPCSpec(run, target, r)
	if err != nil {
		return nil, err
	}

	return vpcResult(r, spec, rw), nil
}

// The VPC's own shape.
//
// Reference mode carries the name alone: the provider forbids
// preset/cidrBlock/region alongside it (ConflictsWith), because it resolves an
// existing Celerity-managed VPC rather than provisioning one. Managed mode
// provisions from the preset, the deploy-config networking, and the security
// groups the resolve recorded as placed in it.
func buildVPCSpec(
	run *transformutils.Run,
	target string,
	r *vpc.ResolvedVPC,
) (*core.MappingNode, error) {
	ctx := run.TransformContext

	// The emitted name is carried through as written, so it may be a reference:
	// the engine resolves it before the provider creates the VPC, and reference
	// mode matches on the resolved value at deploy time. Deploy-config lookups
	// below are keyed by name while the blueprint is transformed, which is too
	// early for that, so they fall back to the resource's own name. Other
	// resources locate their VPC by resource name (VPCName is the link edge's
	// source), so nothing else depends on this being a literal.
	nameNode := specGet(r, "$.name")
	if nameNode == nil {
		nameNode = core.MappingNodeFromString("")
	}

	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{
		"name": nameNode,
	}}

	if core.StringValue(specGet(r, "$.mode")) == modeReferenced {
		spec.Fields["mode"] = core.MappingNodeFromString(flexModeReference)

		return spec, nil
	}

	configName := core.StringValue(nameNode)
	if shared.IsSubstitution(nameNode) {
		configName = r.Name
	}

	spec.Fields["mode"] = core.MappingNodeFromString(flexModeCreate)
	applyPresetAndCIDR(spec, ctx, r, configName)
	applySecurityGroups(spec, ctx, target, r)
	applyDNSConfig(spec, ctx, configName)

	if err := applyRegion(spec, ctx, r); err != nil {
		return nil, err
	}

	// The flex VPC applies spec.tags to the VPC and every resource it provisions
	// alongside it, so stamping them here is what gets subnets, route tables and
	// gateways attributable without the flex VPC knowing anything about Celerity.
	if tags := sharedaws.MapTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      shared.ResolveAppName(run),
		ResourceName: r.Name,
		ResourceType: "celerity/vpc",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	return spec, nil
}

func applyPresetAndCIDR(
	spec *core.MappingNode,
	ctx transform.Context,
	r *vpc.ResolvedVPC,
	configName string,
) {
	preset := core.StringValue(specGet(r, "$.preset"))
	if preset == "" {
		preset = defaultPreset
	}
	spec.Fields["preset"] = core.MappingNodeFromString(preset)

	cidrBlock := defaultCIDRBlock
	if v, ok := sharedaws.ResolveDeployConfig(
		ctx, "aws.vpc", configName, "cidrBlock",
	); ok && v.StringValue != nil {
		cidrBlock = *v.StringValue
	}
	spec.Fields["cidrBlock"] = core.MappingNodeFromString(cidrBlock)
}

// One empty group per in-VPC resource that workloads reach over the network.
// The VPC only prepares them; the rules come from the links between a workload
// and the resource, which pair the workload's group with this one. Without a
// group of its own a resource cannot be paired against at all: the provider
// refuses to open a path to a group it did not mint, since that group is shared
// and the path would reach everything else in it.
func applySecurityGroups(
	spec *core.MappingNode,
	ctx transform.Context,
	target string,
	r *vpc.ResolvedVPC,
) {
	names := placedSecurityGroupNames(target, ctx, r.Placements)
	if len(names) == 0 {
		return
	}

	groupNames := make([]*core.MappingNode, 0, len(names))
	for _, name := range names {
		groupNames = append(groupNames, core.MappingNodeFromString(name))
	}
	spec.Fields["securityGroups"] = core.MappingNodeItems(groupNames...)
}

func applyDNSConfig(spec *core.MappingNode, ctx transform.Context, configName string) {
	if v, ok := sharedaws.ResolveDeployConfigNode(
		ctx, "aws.vpc", configName, "enableDNSSupport",
	); ok {
		spec.Fields["enableDNSSupport"] = v
	}
	if v, ok := sharedaws.ResolveDeployConfigNode(
		ctx, "aws.vpc", configName, "enableDNSHostnames",
	); ok {
		spec.Fields["enableDNSHostnames"] = v
	}
}

func applyRegion(spec *core.MappingNode, ctx transform.Context, r *vpc.ResolvedVPC) error {
	if region, ok := deploymentRegion(ctx); ok {
		spec.Fields["region"] = core.MappingNodeFromString(region)

		return nil
	}

	if transformutils.IsValidationContext(ctx) {
		return nil
	}

	// Cache and sqlDatabase emits reference this VPC's concrete resource by
	// name; a diagnostic with no emitted resources would leave those
	// references dangling in the output blueprint, so a missing region
	// aborts the transform instead.
	return fmt.Errorf(
		"a managed celerity/vpc (%q) requires a deployment region; set \"aws.region\" "+
			"in the deploy configuration",
		r.Name,
	)
}

func vpcResult(
	r *vpc.ResolvedVPC,
	spec *core.MappingNode,
	rw transformutils.ResourcePropertyRewriter,
) *transformutils.EmitResult {
	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			ConcreteResourceName(r.Name): {
				Type: &schema.ResourceTypeWrapper{Value: "aws/flex/vpc"},
				Spec: subwalk.WalkMappingNode(
					spec, transformutils.RewriteResourcePropertyRefs(rw),
				),
				Metadata:     vpcMetadata(r),
				LinkSelector: r.Resource.LinkSelector,
			},
		},
	}
}

func vpcMetadata(r *vpc.ResolvedVPC) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/vpc",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
				// Everything in the application sits inside the VPC and links
				// out of it, so those links describe the environment rather
				// than the application's own wiring.
				ResourceRole: transformutils.ResourceRoleAmbient,
			},
		),
	}
	if r.Resource.Metadata != nil {
		meta.Labels = r.Resource.Metadata.Labels
	}
	return meta
}

func deploymentRegion(ctx transform.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.TransformerConfigVariable("aws.region")
	if !ok || v == nil || v.StringValue == nil || *v.StringValue == "" {
		return "", false
	}
	return *v.StringValue, true
}

func specGet(r *vpc.ResolvedVPC, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, r.Resource.Spec)
	return node
}

// ConcreteResourceName is the emitted aws/flex/vpc resource name for a
// celerity/vpc of the given abstract name. Exported so resources placed into a
// VPC (cache, sqlDatabase) can reference its computed subnet/security-group
// outputs, e.g. ${resources.<ConcreteResourceName>.spec.privateSubnetIds}.
func ConcreteResourceName(name string) string {
	return fmt.Sprintf("%s_flex_vpc", name)
}
