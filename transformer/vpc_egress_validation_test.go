//go:build unit

package transformer

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type VPCEgressValidationTestSuite struct {
	suite.Suite
}

func TestVPCEgressValidationTestSuite(t *testing.T) {
	suite.Run(t, new(VPCEgressValidationTestSuite))
}

// An isolated network routes neither address family outwards, so asking for the
// internet is a request it can never satisfy.
func (s *VPCEgressValidationTestSuite) Test_internet_egress_on_the_isolated_preset_is_an_error() {
	out := s.placed("isolated", annotationMap(
		"celerity.handler.vpc.egress", "internet",
	))

	s.True(
		hasErrorContaining(out.Diagnostics, "provides no outbound path"),
		"expected an error naming the missing outbound path, got: %s", diagnosticSummary(out.Diagnostics),
	)
}

func (s *VPCEgressValidationTestSuite) Test_cidr_egress_on_the_isolated_preset_is_an_error() {
	out := s.placed("isolated", annotationMap(
		"celerity.handler.vpc.egress", "10.0.0.0/8",
	))

	s.True(hasErrorContaining(out.Diagnostics, "provides no outbound path"))
}

func (s *VPCEgressValidationTestSuite) Test_unset_egress_on_the_isolated_preset_is_not_an_error() {
	out := s.placed("isolated", nil)

	s.Empty(
		errorDiagnostics(out.Diagnostics),
		"unset egress must not error, got: %s", diagnosticSummary(out.Diagnostics),
	)
}

// Explicitly closing egress is always satisfiable, whatever the topology.
func (s *VPCEgressValidationTestSuite) Test_none_egress_on_the_isolated_preset_is_not_an_error() {
	out := s.placed("isolated", annotationMap(
		"celerity.handler.vpc.egress", "none",
	))

	s.Empty(errorDiagnostics(out.Diagnostics))
}

// A private-subnet handler on the standard preset reaches IPv4 through the NAT
// gateway, so nothing is wrong with any of this.
func (s *VPCEgressValidationTestSuite) Test_internet_egress_on_the_standard_preset_is_clean() {
	out := s.placed("standard", annotationMap(
		"celerity.handler.vpc.egress", "internet",
	))

	s.Empty(out.Diagnostics, "expected no diagnostics, got: %s", diagnosticSummary(out.Diagnostics))
}

// A public-subnet function has no public IPv4 address, so "internet" is honoured
// over IPv6 only. Warned rather than refused: the deployment still works, and for
// an IPv6-reachable destination it works completely.
func (s *VPCEgressValidationTestSuite) Test_internet_egress_from_a_public_subnet_warns_about_ipv6() {
	out := s.placed("public", annotationMap(
		"celerity.handler.vpc.subnetType", "public",
		"celerity.handler.vpc.egress", "internet",
	))

	s.True(
		hasWarningContaining(out.Diagnostics, "IPv6 destinations only"),
		"expected the IPv6-only warning, got: %s", diagnosticSummary(out.Diagnostics),
	)
	s.Empty(errorDiagnostics(out.Diagnostics), "an IPv6-reachable deployment must stay deployable")
}

// An IPv4-only destination list from a public subnet opens nothing at all: the
// deploy target drops every entry, so the author would get silence rather than a
// rule. That is worth an error even though "internet" only warns.
func (s *VPCEgressValidationTestSuite) Test_ipv4_only_cidrs_from_a_public_subnet_are_an_error() {
	out := s.placed("public", annotationMap(
		"celerity.handler.vpc.subnetType", "public",
		"celerity.handler.vpc.egress", "10.0.0.0/8,192.168.0.0/16",
	))

	s.True(
		hasErrorContaining(out.Diagnostics, "no outbound rule would be created"),
		"expected an error about the rule opening nothing, got: %s",
		diagnosticSummary(out.Diagnostics),
	)
}

// A mixed list does open something, so only the inert half is reported.
func (s *VPCEgressValidationTestSuite) Test_mixed_cidrs_from_a_public_subnet_warn_about_the_ipv4_half() {
	out := s.placed("public", annotationMap(
		"celerity.handler.vpc.subnetType", "public",
		"celerity.handler.vpc.egress", "10.0.0.0/8,2001:db8::/32",
	))

	s.True(
		hasWarningContaining(out.Diagnostics, "only the IPv6 destinations will be opened"),
		"expected a warning naming the dropped IPv4 entries, got: %s",
		diagnosticSummary(out.Diagnostics),
	)
	s.True(hasWarningContaining(out.Diagnostics, "10.0.0.0/8"))
	s.Empty(errorDiagnostics(out.Diagnostics))
}

// IPv6 destinations are reachable from a public subnet, so there is nothing to say.
func (s *VPCEgressValidationTestSuite) Test_ipv6_cidrs_from_a_public_subnet_are_clean() {
	out := s.placed("public", annotationMap(
		"celerity.handler.vpc.subnetType", "public",
		"celerity.handler.vpc.egress", "2001:db8::/32",
	))

	s.Empty(out.Diagnostics, "expected no diagnostics, got: %s", diagnosticSummary(out.Diagnostics))
}

// The IPv4 reasoning is about public subnets only. A private-subnet handler
// reaches IPv4 through the NAT gateway.
func (s *VPCEgressValidationTestSuite) Test_ipv4_cidrs_from_a_private_subnet_are_clean() {
	out := s.placed("standard", annotationMap(
		"celerity.handler.vpc.egress", "10.0.0.0/8",
	))

	s.Empty(out.Diagnostics, "expected no diagnostics, got: %s", diagnosticSummary(out.Diagnostics))
}

// The subnet tier resolves to private by default, so a preset with no private
// subnets cannot honour the placement however the author arrived at it. This is
// the one check that fires on a default rather than an explicit choice.
func (s *VPCEgressValidationTestSuite) Test_default_private_tier_on_a_public_preset_is_an_error() {
	out := s.placed("public", nil)

	s.True(
		hasErrorContaining(out.Diagnostics, "provides no private subnets"),
		"expected an error about the missing private subnets, got: %s",
		diagnosticSummary(out.Diagnostics),
	)
	// The message has to name the way out, since the author never wrote the value
	// being rejected.
	s.True(hasErrorContaining(out.Diagnostics, "celerity.handler.vpc.subnetType"))
}

func (s *VPCEgressValidationTestSuite) Test_explicit_private_tier_on_a_light_public_preset_is_an_error() {
	out := s.placed("light-public", annotationMap(
		"celerity.handler.vpc.subnetType", "private",
	))

	s.True(hasErrorContaining(out.Diagnostics, "provides no private subnets"))
}

// The mirror image: the isolated preset has no public subnets to place into.
func (s *VPCEgressValidationTestSuite) Test_public_tier_on_the_isolated_preset_is_an_error() {
	out := s.placed("isolated", annotationMap(
		"celerity.handler.vpc.subnetType", "public",
	))

	s.True(
		hasErrorContaining(out.Diagnostics, "provides no public subnets"),
		"expected an error about the missing public subnets, got: %s",
		diagnosticSummary(out.Diagnostics),
	)
}

// When the tier itself cannot be honoured there is no placement to reason about,
// so the egress diagnostic would only restate a consequence of the first problem.
func (s *VPCEgressValidationTestSuite) Test_an_unplaceable_tier_reports_once_rather_than_twice() {
	out := s.placed("public", annotationMap(
		"celerity.handler.vpc.egress", "internet",
	))

	s.Len(
		errorDiagnostics(out.Diagnostics), 1,
		"expected a single error, got: %s", diagnosticSummary(out.Diagnostics),
	)
	s.True(hasErrorContaining(out.Diagnostics, "provides no private subnets"))
}

// A referenced VPC's topology belongs to another blueprint and is not knowable
// here, so every preset-level check has to stay quiet rather than guess.
func (s *VPCEgressValidationTestSuite) Test_a_referenced_vpc_is_never_validated_against_a_preset() {
	vpcRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("shared-net"),
			"mode", core.MappingNodeFromString("referenced"),
			// Present but meaningless in referenced mode, and must not be read.
			"preset", core.MappingNodeFromString("isolated"),
		),
	}

	out := s.runWith(vpcRes, annotationMap("celerity.handler.vpc.egress", "internet"))

	s.Empty(
		errorDiagnostics(out.Diagnostics),
		"a referenced VPC must not be judged against a preset, got: %s",
		diagnosticSummary(out.Diagnostics),
	)
}

// A handler with no placement has no topology to be judged against.
func (s *VPCEgressValidationTestSuite) Test_an_unplaced_handler_is_not_validated() {
	handlerRes := egressValidationHandler(annotationMap(
		"celerity.handler.vpc.egress", "internet",
		"celerity.handler.vpc.subnetType", "private",
	))

	out := s.transform(map[string]*schema.Resource{"apiHandler": handlerRes}, edges())

	s.Empty(out.Diagnostics, "expected no diagnostics, got: %s", diagnosticSummary(out.Diagnostics))
}

// Validation reads the tier and reach the cascade produced, not only a handler's
// own annotations, so a placement declared once on a component is judged too.
func (s *VPCEgressValidationTestSuite) Test_inherited_egress_is_validated_on_the_handler() {
	out := s.transform(
		map[string]*schema.Resource{
			"ordersApi": vpcPlacedAPI(annotationMap(
				"celerity.api.vpc.egress", "internet",
			)),
			"createOrder": vpcCascadeHandler(nil),
			"appVpc":      isolatedSelectingNetwork(),
		},
		edges(
			edgeWithLabels(
				"appVpc", "ordersApi", "celerity/vpc", "celerity/api",
				map[string]string{"network": "app-network"},
			),
			edge("ordersApi", "createOrder", "celerity/api", "celerity/handler"),
		),
	)

	s.True(
		hasErrorContaining(out.Diagnostics, "provides no outbound path"),
		"an inherited egress must be validated against the topology, got: %s",
		diagnosticSummary(out.Diagnostics),
	)
	s.True(
		hasErrorContaining(out.Diagnostics, "createOrder"),
		"the diagnostic must name the handler the placement landed on",
	)
}

// Deploys a handler directly linked to a managed VPC of the given preset.
func (s *VPCEgressValidationTestSuite) placed(
	preset string,
	annotations *schema.StringOrSubstitutionsMap,
) *transform.SpecTransformerTransformOutput {
	s.T().Helper()

	vpcRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("app-net"),
			"preset", core.MappingNodeFromString(preset),
		),
	}

	return s.runWith(vpcRes, annotations)
}

func (s *VPCEgressValidationTestSuite) runWith(
	vpcRes *schema.Resource,
	annotations *schema.StringOrSubstitutionsMap,
) *transform.SpecTransformerTransformOutput {
	s.T().Helper()

	return s.transform(
		map[string]*schema.Resource{
			"apiHandler": egressValidationHandler(annotations),
			"appVpc":     vpcRes,
		},
		edges(edge("appVpc", "apiHandler", "celerity/vpc", "celerity/handler")),
	)
}

func (s *VPCEgressValidationTestSuite) transform(
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

func egressValidationHandler(
	annotations *schema.StringOrSubstitutionsMap,
) *schema.Resource {
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("apiHandler"),
			"handler", core.MappingNodeFromString("handlers.serve"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		Metadata: &schema.Metadata{Annotations: annotations},
	}
}

// The cascade fixtures' VPC, on a preset with no outbound path.
func isolatedSelectingNetwork() *schema.Resource {
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("app-network"),
			"preset", core.MappingNodeFromString("isolated"),
		),
		LinkSelector: &schema.LinkSelector{
			ByLabel: &schema.StringMap{Values: map[string]string{"network": "app-network"}},
		},
	}
}

func hasErrorContaining(diags []*core.Diagnostic, substr string) bool {
	for _, d := range diags {
		if d.Level == core.DiagnosticLevelError && strings.Contains(d.Message, substr) {
			return true
		}
	}

	return false
}

func errorDiagnostics(diags []*core.Diagnostic) []*core.Diagnostic {
	errors := []*core.Diagnostic{}
	for _, d := range diags {
		if d.Level == core.DiagnosticLevelError {
			errors = append(errors, d)
		}
	}

	return errors
}

// Renders diagnostics for assertion failure messages, so a failing test says what
// was reported rather than only that the expectation was not met.
func diagnosticSummary(diags []*core.Diagnostic) string {
	if len(diags) == 0 {
		return "(none)"
	}

	rendered := []string{}
	for _, d := range diags {
		rendered = append(rendered, fmt.Sprintf("level %d: %s", d.Level, d.Message))
	}

	return strings.Join(rendered, " | ")
}
