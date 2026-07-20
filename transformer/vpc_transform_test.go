//go:build unit

package transformer

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type VPCTransformTestSuite struct {
	suite.Suite
}

func TestVPCTransformTestSuite(t *testing.T) {
	suite.Run(t, new(VPCTransformTestSuite))
}

func (s *VPCTransformTestSuite) Test_managed_vpc_emits_a_create_mode_flex_vpc() {
	v := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("app-network"),
			"preset", core.MappingNodeFromString("isolated"),
		),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}},
		},
	}
	ctx := deployConfigContext(map[string]*core.ScalarValue{
		"aws.region":                 core.ScalarFromString("eu-west-1"),
		"aws.vpc.cidrBlock":          core.ScalarFromString("10.1.0.0/16"),
		"aws.vpc.enableDNSHostnames": core.ScalarFromBool(true),
	})

	flex := s.transformVPC(map[string]*schema.Resource{"myVpc": v}, ctx)["myVpc_flex_vpc"]
	s.Require().NotNil(flex)
	s.Equal("aws/flex/vpc", flex.Type.Value)
	s.Equal("app-network", core.StringValue(flex.Spec.Fields["name"]))
	s.Equal("create", core.StringValue(flex.Spec.Fields["mode"]))
	s.Equal("isolated", core.StringValue(flex.Spec.Fields["preset"]))
	s.Equal("10.1.0.0/16", core.StringValue(flex.Spec.Fields["cidrBlock"]))
	s.Equal("eu-west-1", core.StringValue(flex.Spec.Fields["region"]))
	s.True(core.BoolValue(flex.Spec.Fields["enableDNSHostnames"]))

	// Labels preserved for placement links; infra category.
	s.Equal("orders", flex.Metadata.Labels.Values["app"])
	s.Equal("infrastructure", annotationLiteral(flex.Metadata.Annotations, transformutils.AnnotationResourceCategory))
}

func (s *VPCTransformTestSuite) Test_managed_vpc_defaults_preset_and_cidr() {
	v := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("app-network")),
	}

	// Validation context: no aws.region, no aws.vpc.* — defaults apply and no
	// region error is raised.
	flex := s.transformVPC(map[string]*schema.Resource{"myVpc": v}, validationContext())["myVpc_flex_vpc"]
	s.Require().NotNil(flex)
	s.Equal("create", core.StringValue(flex.Spec.Fields["mode"]))
	s.Equal("standard", core.StringValue(flex.Spec.Fields["preset"]))
	s.Equal("10.0.0.0/16", core.StringValue(flex.Spec.Fields["cidrBlock"]))
	s.Nil(flex.Spec.Fields["region"], "region is omitted in a validation context when aws.region is unset")
}

// Outside a validation context a managed vpc with no "aws.region" deploy config
// must ABORT the transform: cache/sqlDatabase emits reference the vpc's concrete
// resource by name, so emitting a diagnostic with no resources would leave the
// output blueprint with dangling references.
func (s *VPCTransformTestSuite) Test_managed_vpc_without_region_fails_the_transform_outside_validation() {
	v := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("app-network")),
	}
	ctx := &fakeTransformContext{
		contextVars: map[string]*core.ScalarValue{
			"deployTarget": core.ScalarFromString(shared.AWSServerless),
		},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{"myVpc": v}}}
	_, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: ctx,
		},
	)
	s.Require().Error(err, "a managed vpc without a deployment region must abort the transform")
	s.Contains(err.Error(), "requires a deployment region")
}

// Reference-mode vpcs carry no region, so a missing "aws.region" is not an
// error for them even outside validation contexts.
func (s *VPCTransformTestSuite) Test_referenced_vpc_without_region_still_transforms() {
	v := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("shared-network"),
			"mode", core.MappingNodeFromString("referenced"),
		),
	}
	ctx := &fakeTransformContext{
		contextVars: map[string]*core.ScalarValue{
			"deployTarget": core.ScalarFromString(shared.AWSServerless),
		},
	}

	flex := s.transformVPC(map[string]*schema.Resource{"myVpc": v}, ctx)["myVpc_flex_vpc"]
	s.Require().NotNil(flex)
	s.Equal("reference", core.StringValue(flex.Spec.Fields["mode"]))
}

func (s *VPCTransformTestSuite) Test_referenced_vpc_emits_reference_mode_with_only_name() {
	v := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("shared-network"),
			"mode", core.MappingNodeFromString("referenced"),
			// preset is set but must be dropped in reference mode.
			"preset", core.MappingNodeFromString("standard"),
		),
	}
	ctx := deployConfigContext(map[string]*core.ScalarValue{
		"aws.region":        core.ScalarFromString("eu-west-1"),
		"aws.vpc.cidrBlock": core.ScalarFromString("10.1.0.0/16"),
	})

	flex := s.transformVPC(map[string]*schema.Resource{"myVpc": v}, ctx)["myVpc_flex_vpc"]
	s.Require().NotNil(flex)
	s.Equal("shared-network", core.StringValue(flex.Spec.Fields["name"]))
	s.Equal("reference", core.StringValue(flex.Spec.Fields["mode"]))
	// preset/cidrBlock/region are forbidden by the provider in reference mode.
	s.Nil(flex.Spec.Fields["preset"])
	s.Nil(flex.Spec.Fields["cidrBlock"])
	s.Nil(flex.Spec.Fields["region"])
}

// A vpc -> handler placement stamps the subnet-type link annotation onto the
// function and must NOT emit a vpcConfig (the provider link populates it).
func (s *VPCTransformTestSuite) Test_vpc_placement_stamps_subnet_type_and_omits_vpc_config() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("apiHandler"),
			"handler", core.MappingNodeFromString("handlers.serve"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.vpc.subnetType", "public"),
			Labels:      &schema.StringMap{Values: map[string]string{"app": "api"}},
		},
	}
	vpcRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("app-net")),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"app": "api"}},
		},
	}

	out := s.runTransformVPC(
		map[string]*schema.Resource{"apiHandler": handlerRes, "appVpc": vpcRes},
		edges(edge("appVpc", "apiHandler", "celerity/vpc", "celerity/handler")),
	)
	resources := out.TransformedBlueprint.Resources.Values

	lambda := resources["apiHandler_lambda_func"]
	s.Require().NotNil(lambda)
	s.Equal("public", annotationLiteral(lambda.Metadata.Annotations, "aws.flexvpc.lambda.subnetType"))
	// vpcConfig is populated by the aws/flex/vpc::aws/lambda/function link, never emitted here.
	s.Nil(lambda.Spec.Fields["vpcConfig"])
}

// Without an explicit subnet-type annotation the placement defaults to private.
func (s *VPCTransformTestSuite) Test_vpc_placement_defaults_subnet_type_to_private() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("apiHandler"),
			"handler", core.MappingNodeFromString("handlers.serve"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
	}
	vpcRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("app-net")),
	}

	out := s.runTransformVPC(
		map[string]*schema.Resource{"apiHandler": handlerRes, "appVpc": vpcRes},
		edges(edge("appVpc", "apiHandler", "celerity/vpc", "celerity/handler")),
	)
	lambda := out.TransformedBlueprint.Resources.Values["apiHandler_lambda_func"]
	s.Require().NotNil(lambda)
	s.Equal("private", annotationLiteral(lambda.Metadata.Annotations, "aws.flexvpc.lambda.subnetType"))
	s.Nil(lambda.Spec.Fields["vpcConfig"])
}

func (s *VPCTransformTestSuite) runTransformVPC(
	resources map[string]*schema.Resource,
	lg linktypes.DeclaredLinkGraph,
) *transform.SpecTransformerTransformOutput {
	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: resources}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          lg,
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}

func (s *VPCTransformTestSuite) transformVPC(
	resources map[string]*schema.Resource,
	ctx transform.Context,
) map[string]*schema.Resource {
	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: resources}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: ctx,
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out.TransformedBlueprint.Resources.Values
}
