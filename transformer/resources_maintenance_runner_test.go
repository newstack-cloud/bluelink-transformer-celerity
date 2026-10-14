//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/stretchr/testify/suite"
)

type MaintenanceRunnerTestSuite struct {
	suite.Suite
}

func TestMaintenanceRunnerTestSuite(t *testing.T) {
	suite.Run(t, new(MaintenanceRunnerTestSuite))
}

// A database and a handler that uses it, which is the arrangement every one of
// these assertions is about.
func runnerTransform(s *suite.Suite, dbSpec *core.MappingNode) map[string]*schema.Resource {
	dbRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/sqlDatabase"},
		Spec: dbSpec,
		// The label set a real merged blueprint carries: the author's own, plus
		// the celerity.handler.uses.<db> label the CLI generates for handlers to
		// select the database by.
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{
			"network":                        "app-network",
			"celerity.handler.uses.ordersDb": "ordersDb",
		}}},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"ordersDb": dbRes,
		"myVpc": {
			Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
			Spec: core.MappingNodeFields(
				"name", core.MappingNodeFromString("app-net"),
				"preset", core.MappingNodeFromString("standard"),
			),
			LinkSelector: &schema.LinkSelector{
				ByLabel: &schema.StringMap{Values: map[string]string{"network": "app-network"}},
			},
		},
	}}}

	// A database is only emitted when it is placed in a VPC, which is also the
	// only arrangement in which a runner makes sense.
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: bp,
			LinkGraph: placementLinkGraph{
				vpc:        "myVpc",
				target:     "ordersDb",
				targetType: "celerity/sqlDatabase",
			},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	return out.TransformedBlueprint.Resources.Values
}

func passwordDatabase() *core.MappingNode {
	return core.MappingNodeFields(
		"name", core.MappingNodeFromString("orders"),
		"engine", core.MappingNodeFromString("postgres"),
		"authMode", core.MappingNodeFromString("password"),
	)
}

// Without the runner, a password-mode database deploys with a credential that
// authenticates as a role nothing ever created, and no way to create it: the
// database is in private subnets and nothing outside can reach it.
func (s *MaintenanceRunnerTestSuite) Test_a_database_gets_a_maintenance_runner() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	s.Require().NotNil(runner, "a database needs a runner to provision its application user")
	s.Equal("aws/lambda/function", runner.Type.Value)
	s.NotNil(resources[sqldatabase.MaintenanceRunnerRoleResourceName("ordersDb")])
}

// Lambda defaults to x86_64 when architectures is absent, while the CLI
// cross-compiles the runner for arm64. The mismatch is not caught at deploy time:
// the function is created happily and fails on first invocation with
// "Runtime exited with error: exit status 126", which names neither the
// architecture nor the binary.
func (s *MaintenanceRunnerTestSuite) Test_the_runner_declares_the_architecture_its_binary_is_built_for() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	s.Require().NotNil(runner)

	architectures, ok := pluginutils.GetValueByPath("$.architectures", runner.Spec)
	s.Require().True(ok, "the runner must declare an architecture rather than take Lambda's default")
	s.Require().Len(architectures.Items, 1)
	s.Equal("arm64", core.StringValue(architectures.Items[0]))
}

// The CLI locates the runner in deployed state by this annotation. A naming
// convention would mean two repositories agreeing on a string with nothing to
// enforce it.
func (s *MaintenanceRunnerTestSuite) Test_the_runner_is_findable_in_deployed_state() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	annotation := runner.Metadata.Annotations.Values[sqldatabase.AnnotationKeyMaintenanceRunner]
	s.Require().NotNil(annotation)
	s.Equal("true", renderedSubstitutions(&core.MappingNode{StringWithSubstitutions: annotation}))
}

// The runner has to be in the VPC to reach the database, and the placement
// annotation is what the aws/flex/vpc::aws/lambda/function link reads to build
// its vpcConfig. The transformer never writes vpcConfig itself.
func (s *MaintenanceRunnerTestSuite) Test_the_runner_is_placed_in_private_subnets() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	placement := runner.Metadata.Annotations.Values["aws.flexvpc.lambda.subnetType"]
	s.Require().NotNil(placement, "without a placement the runner has no path to the database")
	s.Nil(runner.Spec.Fields["vpcConfig"], "vpcConfig is the link's to write, not the transformer's")

	// Exactly what the VPC selects members by, so the runner lands in the same
	// VPC as the database.
	s.Equal("app-network", runner.Metadata.Labels.Values["network"])
}

// Found by staging, not by a unit test: the runner originally carried the
// database's whole label set, which includes the generated
// celerity.handler.uses.<db> label every handler using that database selects
// on. The result was a link from all eight handlers to the runner, giving code
// that handles user requests permission to invoke the one function that can
// read the master credential.
func (s *MaintenanceRunnerTestSuite) Test_handlers_do_not_select_the_runner() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	s.Require().NotNil(runner.Metadata.Labels)
	s.NotContains(runner.Metadata.Labels.Values, "celerity.handler.uses.ordersDb",
		"a handler-uses label on the runner makes every handler link to it")
	s.Len(runner.Metadata.Labels.Values, 1, "only the labels the VPC selects by")
}

// The runner selects a dedicated internal label rather than the database's
// labels, which are on every resource the database emits — selecting those
// would link it to the subnet group and the role as well.
func (s *MaintenanceRunnerTestSuite) Test_the_runner_links_only_to_what_it_connects_to() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	s.Require().NotNil(runner.LinkSelector)
	s.Require().NotNil(runner.LinkSelector.ByLabel)
	s.Equal("ordersDb",
		runner.LinkSelector.ByLabel.Values["celerity.internal.sqldatabase.maintenanceTarget"])
	s.Len(runner.LinkSelector.ByLabel.Values, 1)

	proxy := resources["ordersDb_rds_proxy"]
	s.Require().NotNil(proxy, "a standalone database fronts its instance with a proxy")
	s.Equal("ordersDb",
		proxy.Metadata.Labels.Values["celerity.internal.sqldatabase.maintenanceTarget"],
		"the runner connects through the proxy, so the proxy must be selectable by it")
}

// A request names a database and carries nothing else, so whoever can invoke
// the runner cannot point it at a database of their choosing. Everything needed
// to connect is stamped in here at deploy time.
func (s *MaintenanceRunnerTestSuite) Test_the_runner_carries_its_own_connection_details() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	targets := runner.Spec.Fields["environment"].Fields["variables"].
		Fields["CELERITY_MAINTENANCE_TARGETS"]
	s.Require().NotNil(targets)

	rendered := renderedSubstitutions(targets)
	s.Contains(rendered, `"ordersDb"`)
	s.Contains(rendered, `"appUser":"celerity_app"`)
	s.Contains(rendered, `"engine":"postgres"`)
	// Endpoints and secrets are only known at deploy, so they interpolate.
	s.Contains(rendered, "${values.ordersDb_rds_instance_host}")
	s.Contains(rendered, "masterUserSecret.secretArn")
	s.Contains(rendered, "ordersDb_sqldb_app_user_secret")
}

// The grant handlers must never have. Provisioning grants privileges over the
// owner's objects, which only the owner can do; a handler holding the same
// credential would have the whole instance, which is why the application user
// exists at all.
func (s *MaintenanceRunnerTestSuite) Test_only_the_runner_may_read_the_master_credential() {
	resources := runnerTransform(&s.Suite, passwordDatabase())

	role := resources[sqldatabase.MaintenanceRunnerRoleResourceName("ordersDb")]
	s.Require().NotNil(role)
	s.Equal("aws/iam/role", role.Type.Value)

	policies := role.Spec.Fields["policies"]
	s.Require().NotNil(policies)
	rendered := renderedSubstitutions(policies)
	s.Contains(rendered, "secretsmanager:GetSecretValue")
	s.Contains(rendered, "masterUserSecret.secretArn")
	s.Contains(rendered, "ordersDb_sqldb_app_user_secret")

	// Every other role except the proxy's. The proxy legitimately holds the
	// master credential — authScheme SECRETS is how it authenticates to the
	// database on the application's behalf — but it is infrastructure, not
	// something running user code. A handler role reaching it would be the
	// defect this whole design exists to avoid.
	for name, resource := range resources {
		if resource.Type.Value != "aws/iam/role" ||
			name == sqldatabase.MaintenanceRunnerRoleResourceName("ordersDb") ||
			strings.Contains(name, "proxy") {
			continue
		}
		s.NotContains(renderedSubstitutions(resource.Spec), "masterUserSecret.secretArn",
			"role %q runs user code and must not be able to read the master credential", name)
	}
}

// IAM mode stores no credential to read, but the role still has to exist in the
// database and hold rds_iam, so the runner is emitted either way.
func (s *MaintenanceRunnerTestSuite) Test_iam_mode_still_needs_a_runner() {
	resources := runnerTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("orders"),
		"engine", core.MappingNodeFromString("postgres"),
		"authMode", core.MappingNodeFromString("iam"),
	))

	runner := resources[sqldatabase.MaintenanceRunnerResourceName("ordersDb")]
	s.Require().NotNil(runner)

	role := resources[sqldatabase.MaintenanceRunnerRoleResourceName("ordersDb")]
	rendered := renderedSubstitutions(role.Spec)
	s.Contains(rendered, "rds-db:connect")
	s.NotContains(rendered, "secretsmanager:GetSecretValue",
		"IAM mode distributes no credential, so there is nothing to grant read on")

	targets := renderedSubstitutions(runner.Spec.Fields["environment"].
		Fields["variables"].Fields["CELERITY_MAINTENANCE_TARGETS"])
	s.Contains(targets, `"appUserTokenAuth":true`)
}

// renderedSubstitutions flattens a mapping node to text, including the
// substitutions inside it, so an assertion can check what a spec references
// without walking the tree by hand.
func renderedSubstitutions(node *core.MappingNode) string {
	var b strings.Builder
	writeNode(&b, node)
	return b.String()
}

// substitutionText renders the two reference kinds these specs use back into
// their source form, so an assertion reads like the blueprint it checks.
func substitutionText(sub *substitutions.Substitution) string {
	if sub == nil {
		return ""
	}
	if sub.ResourceProperty != nil {
		parts := []string{"resources", sub.ResourceProperty.ResourceName}
		for _, item := range sub.ResourceProperty.Path {
			if item.FieldName != "" {
				parts = append(parts, item.FieldName)
			}
		}
		return "${" + strings.Join(parts, ".") + "}"
	}
	if sub.ValueReference != nil {
		return "${values." + sub.ValueReference.ValueName + "}"
	}
	return ""
}

func writeNode(b *strings.Builder, node *core.MappingNode) {
	if node == nil {
		return
	}
	if node.Scalar != nil {
		b.WriteString(node.Scalar.ToString())
	}
	if node.StringWithSubstitutions != nil {
		for _, value := range node.StringWithSubstitutions.Values {
			if value.StringValue != nil {
				b.WriteString(*value.StringValue)
			}
			if value.SubstitutionValue != nil {
				b.WriteString(substitutionText(value.SubstitutionValue))
			}
		}
	}
	for _, field := range node.Fields {
		writeNode(b, field)
		b.WriteString(" ")
	}
	for _, item := range node.Items {
		writeNode(b, item)
		b.WriteString(" ")
	}
}
