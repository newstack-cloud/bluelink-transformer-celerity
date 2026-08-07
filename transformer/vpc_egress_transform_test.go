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
	"github.com/stretchr/testify/suite"
)

// The provider annotation the transformer stamps the resolved reach onto.
const egressAnnotation = "aws.flexvpc.lambda.egress"

type VPCEgressTransformTestSuite struct {
	suite.Suite
}

func TestVPCEgressTransformTestSuite(t *testing.T) {
	suite.Run(t, new(VPCEgressTransformTestSuite))
}

func (s *VPCEgressTransformTestSuite) Test_handler_egress_annotation_is_stamped_on_the_function() {
	lambda := s.placedHandler(annotationMap(
		"celerity.handler.vpc.egress", "internet",
	))

	s.Equal("internet", annotationLiteral(lambda.Metadata.Annotations, egressAnnotation))
}

// The distinction the whole design rests on: saying nothing must reach the
// provider as an absent annotation, so it resolves the reach from the VPC's
// topology rather than reading a value the author never wrote.
func (s *VPCEgressTransformTestSuite) Test_unset_egress_stamps_no_annotation_at_all() {
	lambda := s.placedHandler(nil)

	s.False(
		hasAnnotation(lambda.Metadata.Annotations, egressAnnotation),
		"an unset egress annotation must not be stamped, not even as an empty value",
	)
	// The placement itself still happened.
	s.Equal("private", annotationLiteral(lambda.Metadata.Annotations, "aws.flexvpc.lambda.subnetType"))
}

// "none" is a value the author chose, so unlike unset it is passed through.
func (s *VPCEgressTransformTestSuite) Test_none_is_stamped_rather_than_treated_as_unset() {
	lambda := s.placedHandler(annotationMap(
		"celerity.handler.vpc.egress", "none",
	))

	s.True(hasAnnotation(lambda.Metadata.Annotations, egressAnnotation))
	s.Equal("none", annotationLiteral(lambda.Metadata.Annotations, egressAnnotation))
}

// A CIDR list is opaque to the transformer: the provider parses and validates it,
// so it must arrive exactly as written.
func (s *VPCEgressTransformTestSuite) Test_cidr_list_is_passed_through_verbatim() {
	lambda := s.placedHandler(annotationMap(
		"celerity.handler.vpc.egress", "10.0.0.0/8,192.168.4.0/24",
	))

	s.Equal(
		"10.0.0.0/8,192.168.4.0/24",
		annotationLiteral(lambda.Metadata.Annotations, egressAnnotation),
	)
}

// Surrounding whitespace is the author's formatting rather than part of the value,
// and an all-whitespace annotation is nothing said at all.
func (s *VPCEgressTransformTestSuite) Test_surrounding_whitespace_is_trimmed() {
	lambda := s.placedHandler(annotationMap(
		"celerity.handler.vpc.egress", "  none  ",
	))

	s.Equal("none", annotationLiteral(lambda.Metadata.Annotations, egressAnnotation))
}

func (s *VPCEgressTransformTestSuite) Test_whitespace_only_egress_is_treated_as_unset() {
	lambda := s.placedHandler(annotationMap(
		"celerity.handler.vpc.egress", "   ",
	))

	s.False(
		hasAnnotation(lambda.Metadata.Annotations, egressAnnotation),
		"an annotation holding only whitespace says nothing and must not be stamped",
	)
}

// A handler that is not placed in a VPC has no outbound reach to narrow, so the
// annotation is inert rather than emitted onto a function with no vpcConfig.
func (s *VPCEgressTransformTestSuite) Test_egress_is_not_stamped_without_a_vpc_placement() {
	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("apiHandler"),
			"handler", core.MappingNodeFromString("handlers.serve"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{
			Annotations: annotationMap("celerity.handler.vpc.egress", "none"),
		},
	}

	out := s.runTransform(map[string]*schema.Resource{"apiHandler": handlerRes}, edges())
	lambda := out.TransformedBlueprint.Resources.Values["apiHandler_lambda_func"]
	s.Require().NotNil(lambda)
	s.False(hasAnnotation(lambda.Metadata.Annotations, egressAnnotation))
}

// Egress cascades from an application component on the same terms as the subnet
// tier, so a placement declared once covers the handlers it fans out to.
func (s *VPCEgressTransformTestSuite) Test_egress_cascades_from_an_api_to_its_handlers() {
	lambda := s.cascadedHandler(
		annotationMap("celerity.api.vpc.egress", "none"),
		nil,
	)

	s.Equal("none", annotationLiteral(lambda.Metadata.Annotations, egressAnnotation))
}

func (s *VPCEgressTransformTestSuite) Test_handler_egress_overrides_the_inherited_egress() {
	lambda := s.cascadedHandler(
		annotationMap("celerity.api.vpc.egress", "none"),
		annotationMap("celerity.handler.vpc.egress", "internet"),
	)

	s.Equal("internet", annotationLiteral(lambda.Metadata.Annotations, egressAnnotation))
}

// The cascade carries subnet tier and egress independently: a component may set
// one without the other, and the unset one stays unset.
func (s *VPCEgressTransformTestSuite) Test_inherited_subnet_type_does_not_imply_an_egress_value() {
	lambda := s.cascadedHandler(
		annotationMap("celerity.api.vpc.subnetType", "public"),
		nil,
	)

	s.Equal("public", annotationLiteral(lambda.Metadata.Annotations, "aws.flexvpc.lambda.subnetType"))
	s.False(hasAnnotation(lambda.Metadata.Annotations, egressAnnotation))
}

// Deploys a handler linked directly to a VPC and returns the emitted function.
func (s *VPCEgressTransformTestSuite) placedHandler(
	annotations *schema.StringOrSubstitutionsMap,
) *schema.Resource {
	s.T().Helper()

	handlerRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("apiHandler"),
			"handler", core.MappingNodeFromString("handlers.serve"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{Annotations: annotations},
	}
	vpcRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields("name", core.MappingNodeFromString("app-net")),
	}

	out := s.runTransform(
		map[string]*schema.Resource{"apiHandler": handlerRes, "appVpc": vpcRes},
		edges(edge("appVpc", "apiHandler", "celerity/vpc", "celerity/handler")),
	)

	lambda := out.TransformedBlueprint.Resources.Values["apiHandler_lambda_func"]
	s.Require().NotNil(lambda)

	return lambda
}

// Deploys an API placed in a VPC with a handler inheriting that placement, and
// returns the handler's emitted function.
func (s *VPCEgressTransformTestSuite) cascadedHandler(
	apiAnnotations *schema.StringOrSubstitutionsMap,
	handlerAnnotations *schema.StringOrSubstitutionsMap,
) *schema.Resource {
	s.T().Helper()

	out := s.runTransform(
		map[string]*schema.Resource{
			"ordersApi":   vpcPlacedAPI(apiAnnotations),
			"createOrder": vpcCascadeHandler(handlerAnnotations),
			"appVpc":      vpcSelectingNetwork(),
		},
		edges(
			edgeWithLabels(
				"appVpc", "ordersApi", "celerity/vpc", "celerity/api",
				map[string]string{"network": "app-network"},
			),
			edge("ordersApi", "createOrder", "celerity/api", "celerity/handler"),
		),
	)

	lambda := out.TransformedBlueprint.Resources.Values["createOrder_lambda_func"]
	s.Require().NotNil(lambda)

	return lambda
}

func (s *VPCEgressTransformTestSuite) runTransform(
	resources map[string]*schema.Resource,
	lg linktypes.DeclaredLinkGraph,
) *transform.SpecTransformerTransformOutput {
	s.T().Helper()

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

// Reports whether the annotation is present at all, which annotationLiteral
// cannot answer. It returns the empty string for absent and empty alike, and the
// difference between those two is exactly what these tests are about.
func hasAnnotation(annos *schema.StringOrSubstitutionsMap, key string) bool {
	if annos == nil {
		return false
	}
	_, ok := annos.Values[key]

	return ok
}
