package vpc

import (
	"context"
	"fmt"

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

func emitVPC(
	_ context.Context,
	run *transformutils.Run,
	r *ResolvedVPC,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	ctx := run.TransformContext
	name := core.StringValue(specGet(r, "$.name"))

	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{
		"name": core.MappingNodeFromString(name),
	}}

	if core.StringValue(specGet(r, "$.mode")) == modeReferenced {
		// Reference an existing Celerity-managed VPC by name. The provider forbids
		// preset/cidrBlock/region in reference mode (ConflictsWith), so only the
		// name is carried.
		spec.Fields["mode"] = core.MappingNodeFromString(flexModeReference)
		return vpcResult(r, spec, rw), nil
	}

	// Managed -> provision the VPC from its preset and deploy-config networking.
	spec.Fields["mode"] = core.MappingNodeFromString(flexModeCreate)

	preset := core.StringValue(specGet(r, "$.preset"))
	if preset == "" {
		preset = defaultPreset
	}
	spec.Fields["preset"] = core.MappingNodeFromString(preset)

	cidrBlock := defaultCIDRBlock
	if v, ok := sharedaws.ResolveDeployConfig(ctx, "aws.vpc", name, "cidrBlock"); ok && v.StringValue != nil {
		cidrBlock = *v.StringValue
	}
	spec.Fields["cidrBlock"] = core.MappingNodeFromString(cidrBlock)

	if v, ok := sharedaws.ResolveDeployConfigNode(ctx, "aws.vpc", name, "enableDNSSupport"); ok {
		spec.Fields["enableDNSSupport"] = v
	}
	if v, ok := sharedaws.ResolveDeployConfigNode(ctx, "aws.vpc", name, "enableDNSHostnames"); ok {
		spec.Fields["enableDNSHostnames"] = v
	}

	if region, ok := deploymentRegion(ctx); ok {
		spec.Fields["region"] = core.MappingNodeFromString(region)
	} else if !transformutils.IsValidationContext(ctx) {
		return &transformutils.EmitResult{
			Diagnostics: []*core.Diagnostic{
				{
					Level: core.DiagnosticLevelError,
					Message: "a managed celerity/vpc requires a deployment region; set \"aws.region\" " +
						"in the deploy configuration",
				},
			},
		}, nil
	}

	return vpcResult(r, spec, rw), nil
}

func vpcResult(
	r *ResolvedVPC,
	spec *core.MappingNode,
	rw transformutils.ResourcePropertyRewriter,
) *transformutils.EmitResult {
	spec = subwalk.WalkMappingNode(spec, transformutils.RewriteResourcePropertyRefs(rw))

	res := &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/flex/vpc"},
		Spec:         spec,
		Metadata:     vpcMetadata(r),
		LinkSelector: r.Resource.LinkSelector,
	}

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			ConcreteResourceName(r.Name): res,
		},
	}
}

func vpcMetadata(r *ResolvedVPC) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/vpc",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
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

func specGet(r *ResolvedVPC, path string) *core.MappingNode {
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
