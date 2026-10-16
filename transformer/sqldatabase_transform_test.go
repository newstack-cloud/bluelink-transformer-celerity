//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type SQLDatabaseTransformTestSuite struct {
	suite.Suite
}

func TestSQLDatabaseTransformTestSuite(t *testing.T) {
	suite.Run(t, new(SQLDatabaseTransformTestSuite))
}

// When the author omits spec.name, every deployed identifier derives from an
// app-scoped base (<app>-<resourceName>) so two apps sharing an account never
// collide on generated names, while the engine-internal dbName and blueprint
// RESOURCE names stay app-agnostic. Validation contexts use the placeholder
// app segment.
func (s *SQLDatabaseTransformTestSuite) Test_omitted_name_app_scopes_deployed_identifiers() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"engine", core.MappingNodeFromString("postgres"),
		),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	inst := resources["myDb_rds_instance"]
	s.Require().NotNil(inst)
	s.Equal("placeholder-app-myDb",
		core.StringValue(inst.Spec.Fields["dbInstanceIdentifier"]))
	s.Equal("myDb", core.StringValue(inst.Spec.Fields["dbName"]),
		"the engine-internal database name stays logical")

	sng := resources["myDb_rds_subnet_group"]
	s.Require().NotNil(sng)
	s.Equal("placeholder-app-myDb-db-subnets",
		core.StringValue(sng.Spec.Fields["dbSubnetGroupName"]))

	proxy := resources["myDb_rds_proxy"]
	s.Require().NotNil(proxy)
	s.Equal("placeholder-app-myDb",
		core.StringValue(proxy.Spec.Fields["dbProxyName"]))

	role := resources["myDb_rds_proxy_role"]
	s.Require().NotNil(role)
	s.Equal("placeholder-app-myDb-proxy-role",
		core.StringValue(role.Spec.Fields["roleName"]))
}

// Every deployed identifier derives from the name, so a name set from a
// variable has to survive as a reference through all of them. Resolving it with
// core.StringValue produced an empty name, which app-scoped the identifiers as
// though no name had been given.
func (s *SQLDatabaseTransformTestSuite) Test_name_set_from_a_variable_reaches_the_deployed_identifiers() {
	nameRef, err := shared.SubstitutionMappingNode("${variables.dbName}")
	s.Require().NoError(err)

	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"engine", core.MappingNodeFromString("postgres"),
			"name", nameRef,
		),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	inst := resources["myDb_rds_instance"]
	s.Require().NotNil(inst)
	s.Require().NotNil(inst.Spec.Fields["dbInstanceIdentifier"].StringWithSubstitutions)

	// The engine-internal database name stays logical, falling back to the
	// resource name: it is read while transforming, so it cannot be a reference.
	s.Equal("myDb", core.StringValue(inst.Spec.Fields["dbName"]))

	for resourceName, field := range map[string]string{
		"myDb_rds_subnet_group": "dbSubnetGroupName",
		"myDb_rds_proxy":        "dbProxyName",
		"myDb_rds_proxy_role":   "roleName",
	} {
		res := resources[resourceName]
		s.Require().NotNil(res, resourceName)
		s.NotNil(
			res.Spec.Fields[field].StringWithSubstitutions,
			"%s.%s keeps the reference", resourceName, field,
		)
	}
}

func (s *SQLDatabaseTransformTestSuite) Test_database_in_a_managed_vpc_emits_instance_and_subnet_group() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		"standard",
		map[string]string{"app": "orders"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	inst := resources["myDb_rds_instance"]
	s.Require().NotNil(inst)
	s.Equal("aws/rds/dbInstance", inst.Type.Value)
	s.Equal("orders", core.StringValue(inst.Spec.Fields["dbInstanceIdentifier"]))
	s.Equal("postgres", core.StringValue(inst.Spec.Fields["engine"]))
	// Password mode -> RDS manages the master-user secret itself.
	s.True(core.BoolValue(inst.Spec.Fields["manageMasterUserPassword"]))
	s.Nil(inst.Spec.Fields["enableIAMDatabaseAuthentication"])
	// dbSubnetGroupName references the emitted subnet group so a dependency edge
	// is created (rather than a plain string that would leave no ordering edge).
	s.Equal("myDb_rds_subnet_group", resourceRefName(inst.Spec.Fields["dbSubnetGroupName"]))
	s.Require().NotNil(inst.Spec.Fields["vpcSecurityGroups"])
	s.Equal("orders", inst.Metadata.Labels.Values["app"])

	sng := resources["myDb_rds_subnet_group"]
	s.Require().NotNil(sng)
	s.Equal("aws/rds/dbSubnetGroup", sng.Type.Value)
	s.Require().NotNil(sng.Spec.Fields["subnetIds"])

	// An RDS Proxy pools handler connections. It carries the db's labels so a
	// handler that links to the sqlDatabase resolves to the proxy.
	proxy := resources["myDb_rds_proxy"]
	s.Require().NotNil(proxy)
	s.Equal("aws/rds/dbProxy", proxy.Type.Value)
	s.Equal("POSTGRESQL", core.StringValue(proxy.Spec.Fields["engineFamily"]))
	s.Require().NotNil(proxy.Spec.Fields["vpcSubnetIds"])
	s.Require().NotNil(proxy.Spec.Fields["roleArn"])
	s.Equal("orders", proxy.Metadata.Labels.Values["app"])
	// Password mode -> SECRETS auth, one registered secret per role the proxy
	// authenticates: the instance's RDS-managed master secret and the
	// application user's. The IAM_AUTH default scheme is iam-mode only.
	s.Nil(proxy.Spec.Fields["defaultAuthScheme"])
	authItems := proxy.Spec.Fields["auth"].Items
	s.Require().Len(authItems, 2)
	for _, entry := range authItems {
		s.Equal("SECRETS", core.StringValue(entry.Fields["authScheme"]))
		s.Require().NotNil(entry.Fields["secretArn"])
	}

	// The proxy's IAM role trusts rds.amazonaws.com and grants GetSecretValue.
	// The deployed role name derives from the physical base (the author-provided
	// spec.name "orders"), not the blueprint resource name.
	role := resources["myDb_rds_proxy_role"]
	s.Require().NotNil(role)
	s.Equal("aws/iam/role", role.Type.Value)
	s.Equal("orders-proxy-role", core.StringValue(role.Spec.Fields["roleName"]))
	s.Require().NotNil(role.Spec.Fields["policies"], "password mode grants secret access")

	// No target group resource. Registering the instance is done by the
	// aws/rds/dbProxy::aws/rds/dbInstance link instead, because
	// AWS::RDS::DBProxyTargetGroup blocks its create until the registered target
	// reports healthy, and the security group rule that makes it healthy is written
	// by that same link. As a resource it ran a whole phase before the rule it
	// depended on and spent its entire timeout waiting.
	s.NotContains(resources, "myDb_rds_proxy_target_group")
}

// An RDS proxy rejects a connection for any role it holds no credentials for,
// reporting "This RDS proxy has no credentials for the role celerity_app". That
// reads as a missing database role rather than a missing registration, and it is
// what every handler hit: the role was provisioned and granted, the secret
// existed, and the proxy had simply never been told about it.
func (s *SQLDatabaseTransformTestSuite) Test_password_auth_proxy_registers_both_the_master_and_application_secrets() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	proxy := resources["myDb_rds_proxy"]
	s.Require().NotNil(proxy)
	authItems := proxy.Spec.Fields["auth"].Items
	s.Require().Len(authItems, 2, "the master and the application user each need a registered secret")
	for _, entry := range authItems {
		s.Equal("SECRETS", core.StringValue(entry.Fields["authScheme"]))
		s.NotNil(entry.Fields["secretArn"])
	}
}

// A secret the proxy is registered with but cannot read fails at connection time
// just as completely as one that was never registered.
func (s *SQLDatabaseTransformTestSuite) Test_password_auth_proxy_role_can_read_both_secrets() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	role := resources["myDb_rds_proxy_role"]
	s.Require().NotNil(role)
	policies := role.Spec.Fields["policies"].Items
	s.Require().Len(policies, 1)
	stmt := policies[0].Fields["policyDocument"].Fields["statement"].Items[0].Fields
	s.Equal("secretsmanager:GetSecretValue", core.StringValue(stmt["action"]))
	s.Require().NotNil(stmt["resource"].Items, "the policy must cover every registered secret")
	s.Len(stmt["resource"].Items, 2)
}

func (s *SQLDatabaseTransformTestSuite) Test_iam_auth_proxy_uses_iam_auth_and_role_without_secret_policy() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
			"authMode", core.MappingNodeFromString("iam"),
		),
		"standard",
		nil,
	)
	resources := out.TransformedBlueprint.Resources.Values

	proxy := resources["myDb_rds_proxy"]
	s.Require().NotNil(proxy)
	// IAM_AUTH lets the secret-less iam-mode proxy authenticate to the database.
	s.Equal("IAM_AUTH", core.StringValue(proxy.Spec.Fields["defaultAuthScheme"]))
	authItems := proxy.Spec.Fields["auth"].Items
	s.Require().Len(authItems, 1)
	s.Equal("REQUIRED", core.StringValue(authItems[0].Fields["iamAuth"]))
	s.Nil(authItems[0].Fields["secretArn"])

	role := resources["myDb_rds_proxy_role"]
	s.Require().NotNil(role)
	// iam mode grants rds-db:connect (not secret access) so the proxy can
	// authenticate to the database with IAM.
	policies := role.Spec.Fields["policies"].Items
	s.Require().Len(policies, 1)
	stmt := policies[0].Fields["policyDocument"].Fields["statement"].Items[0].Fields
	s.Equal("rds-db:connect", core.StringValue(stmt["action"]))

	// The resource is a mixed ARN: literal segments around a substitution
	// referencing the instance's computed dbiResourceId.
	res := stmt["resource"]
	s.Require().NotNil(res.StringWithSubstitutions)
	var literal string
	foundDbiResourceID := false
	for _, v := range res.StringWithSubstitutions.Values {
		if v.StringValue != nil {
			literal += *v.StringValue
		}
		if v.SubstitutionValue != nil && v.SubstitutionValue.ResourceProperty != nil {
			for _, p := range v.SubstitutionValue.ResourceProperty.Path {
				if p.FieldName == "dbiResourceId" {
					foundDbiResourceID = true
				}
			}
		}
	}
	s.Contains(literal, "arn:aws:rds-db:*:*:dbuser:")
	s.Contains(literal, "/celerity")
	s.True(foundDbiResourceID, "the ARN references the instance's dbiResourceId")
}

func (s *SQLDatabaseTransformTestSuite) Test_aurora_serverless_v2_emits_cluster_and_serverless_instance() {
	out := s.transformDBWithConfig(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		map[string]*core.ScalarValue{
			"aws.aurora.orders.enabled": core.ScalarFromBool(true),
			"aws.aurora.orders.maxACU":  core.ScalarFromFloat(16),
		},
		map[string]string{"app": "orders"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	cluster := resources["myDb_rds_cluster"]
	s.Require().NotNil(cluster)
	s.Equal("aws/rds/dbCluster", cluster.Type.Value)
	s.Equal("aurora-postgresql", core.StringValue(cluster.Spec.Fields["engine"]))
	scaling := cluster.Spec.Fields["serverlessV2ScalingConfiguration"]
	s.Require().NotNil(scaling)
	s.Require().NotNil(scaling.Fields["minCapacity"])
	// The provider declares minCapacity/maxCapacity as floats, and the engine
	// normalises a whole number authored for the key before the transform sees it.
	s.Equal(
		float64(16),
		core.FloatValue(scaling.Fields["maxCapacity"]),
		"maxACU deploy-config override applied",
	)
	// Handlers link to the cluster (Aurora built-in pooling), so it keeps labels.
	s.Equal("orders", cluster.Metadata.Labels.Values["app"])

	inst := resources["myDb_rds_instance"]
	s.Require().NotNil(inst)
	s.Equal("db.serverless", core.StringValue(inst.Spec.Fields["dbInstanceClass"]))
	s.Require().NotNil(inst.Spec.Fields["dbClusterIdentifier"])

	// No standalone proxy in the Aurora path.
	s.NotContains(resources, "myDb_rds_proxy")
}

func (s *SQLDatabaseTransformTestSuite) Test_read_replicas_emit_a_reader_instance() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
			"readReplicas", core.MappingNodeFromBool(true),
		),
		"standard",
		nil,
	)
	reader := out.TransformedBlueprint.Resources.Values["myDb_rds_reader"]
	s.Require().NotNil(reader)
	s.Equal("aws/rds/dbInstance", reader.Type.Value)
	s.Equal("orders-reader", core.StringValue(reader.Spec.Fields["dbInstanceIdentifier"]))
	s.Require().NotNil(reader.Spec.Fields["sourceDBInstanceIdentifier"], "reader replicates the primary")
}

// A provisioned database is otherwise pinned to db.t3.micro with 20 GiB. The
// resource specifications document overrides for both, and nothing read them.
func (s *SQLDatabaseTransformTestSuite) Test_rds_deploy_config_overrides_the_instance_defaults() {
	out := s.transformDBWithConfig(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		map[string]*core.ScalarValue{
			"aws.rds.orders.instanceClass":    core.ScalarFromString("db.r6g.large"),
			"aws.rds.orders.allocatedStorage": core.ScalarFromInt(200),
		},
		map[string]string{"app": "orders"},
	)

	instance := out.TransformedBlueprint.Resources.Values["myDb_rds_instance"]
	s.Require().NotNil(instance)
	s.Equal("db.r6g.large", core.StringValue(instance.Spec.Fields["dbInstanceClass"]))
	// The provider's allocatedStorage is a string, so a number authored in deploy
	// config is rendered as one.
	s.Equal("200", core.StringValue(instance.Spec.Fields["allocatedStorage"]))
}

func (s *SQLDatabaseTransformTestSuite) Test_rds_instance_defaults_apply_without_deploy_config() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		"standard",
		nil,
	)

	instance := out.TransformedBlueprint.Resources.Values["myDb_rds_instance"]
	s.Require().NotNil(instance)
	s.Equal("db.t3.micro", core.StringValue(instance.Spec.Fields["dbInstanceClass"]))
	s.Equal("20", core.StringValue(instance.Spec.Fields["allocatedStorage"]))
}

// The reader takes its own override, so a replica can be sized independently of
// the primary.
func (s *SQLDatabaseTransformTestSuite) Test_read_replica_instance_class_override() {
	out := s.transformDBWithConfig(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
			"readReplicas", core.MappingNodeFromBool(true),
		),
		map[string]*core.ScalarValue{
			"aws.rds.orders.instanceClass":              core.ScalarFromString("db.r6g.large"),
			"aws.rds.orders.readReplicas.instanceClass": core.ScalarFromString("db.t4g.medium"),
		},
		map[string]string{"app": "orders"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	s.Equal(
		"db.r6g.large",
		core.StringValue(resources["myDb_rds_instance"].Spec.Fields["dbInstanceClass"]),
	)
	s.Equal(
		"db.t4g.medium",
		core.StringValue(resources["myDb_rds_reader"].Spec.Fields["dbInstanceClass"]),
	)
}

func (s *SQLDatabaseTransformTestSuite) Test_outputs_resolve_to_concrete_computed_attributes() {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myDb": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/sqlDatabase"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("orders"),
						"engine", core.MappingNodeFromString("postgres"),
						"readReplicas", core.MappingNodeFromBool(true),
					),
				},
				"myVpc": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("app-net"),
						"preset", core.MappingNodeFromString("standard"),
					),
				},
			},
		},
		Values: &schema.ValueMap{
			Values: map[string]*schema.Value{
				"host":         valueRefTo("${myDb.spec.host}"),
				"port":         valueRefTo("${myDb.spec.port}"),
				"id":           valueRefTo("${myDb.spec.id}"),
				"readHost":     valueRefTo("${myDb.spec.readHost}"),
				"databaseName": valueRefTo("${myDb.spec.databaseName}"),
			},
		},
	}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: bp,
			LinkGraph: placementLinkGraph{
				vpc:        "myVpc",
				target:     "myDb",
				targetType: "celerity/sqlDatabase",
			},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	vals := out.TransformedBlueprint.Values.Values

	// id and port are static renames onto the primary instance.
	s.assertValueRef(vals["id"], "myDb_rds_instance", "spec", "dbInstanceArn")
	s.assertValueRef(vals["port"], "myDb_rds_instance", "spec", "endpoint", "port")

	// host is an emit-time derived value: standalone + VPC emits an RDS Proxy, so
	// host connects via the proxy endpoint (not the raw instance endpoint).
	s.Equal("myDb_rds_instance_host", valueRefName(vals["host"].Value))
	s.assertValueRef(vals["myDb_rds_instance_host"], "myDb_rds_proxy", "spec", "endpoint")

	// readHost (standalone) resolves to the reader-instance endpoint.
	s.Equal("myDb_rds_instance_read_host", valueRefName(vals["readHost"].Value))
	s.assertValueRef(vals["myDb_rds_instance_read_host"], "myDb_rds_reader", "spec", "endpoint", "address")

	// databaseName is a literal known at emit time.
	s.Equal("myDb_rds_instance_database_name", valueRefName(vals["databaseName"].Value))
	s.Equal("orders", core.StringValue(vals["myDb_rds_instance_database_name"].Value))
}

func (s *SQLDatabaseTransformTestSuite) Test_aurora_outputs_resolve_to_cluster_endpoints() {
	out := s.transformDBWithConfigAndValues(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
			"readReplicas", core.MappingNodeFromBool(true),
		),
		map[string]*core.ScalarValue{
			"aws.aurora.orders.enabled": core.ScalarFromBool(true),
		},
		map[string]*schema.Value{
			"host":     valueRefTo("${myDb.spec.host}"),
			"readHost": valueRefTo("${myDb.spec.readHost}"),
		},
	)
	vals := out.TransformedBlueprint.Values.Values

	// Aurora emits no proxy: host is the cluster writer endpoint.
	s.Equal("myDb_rds_instance_host", valueRefName(vals["host"].Value))
	s.assertValueRef(vals["myDb_rds_instance_host"], "myDb_rds_cluster", "spec", "endpoint", "address")

	// readHost is the load-balanced Aurora cluster reader endpoint.
	s.Equal("myDb_rds_instance_read_host", valueRefName(vals["readHost"].Value))
	s.assertValueRef(vals["myDb_rds_instance_read_host"], "myDb_rds_cluster", "spec", "readEndpoint", "address")
}

func (s *SQLDatabaseTransformTestSuite) Test_iam_auth_enables_iam_database_authentication() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
			"authMode", core.MappingNodeFromString("iam"),
		),
		"standard",
		nil,
	)
	inst := out.TransformedBlueprint.Resources.Values["myDb_rds_instance"]
	s.Require().NotNil(inst)
	s.True(core.BoolValue(inst.Spec.Fields["enableIAMDatabaseAuthentication"]))
	s.Nil(inst.Spec.Fields["manageMasterUserPassword"])
}

func (s *SQLDatabaseTransformTestSuite) Test_single_az_light_preset_is_rejected() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		"light", // single-AZ: unsuitable for RDS (needs >= 2 AZs)
		nil,
	)

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelError &&
			strings.Contains(d.Message, "at least two availability zones") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic about needing two availability zones")
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myDb_rds_instance")
}

// A database with no VPC link cannot be placed, so the emit errors and produces
// no database resources rather than an incomplete instance.
func (s *SQLDatabaseTransformTestSuite) Test_missing_vpc_errors_and_emits_nothing() {
	dbRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/sqlDatabase"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
	}
	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{"myDb": dbRes}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelError && strings.Contains(d.Message, "require VPC placement") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic about required VPC placement")
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myDb_rds_instance")
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myDb_rds_proxy")
}

// Spelled out rather than imported from the sqldatabase package: this is the label key
// the emitted blueprint carries, so the test should fail if it is renamed.
const proxyTargetLabel = "celerity.internal.sqldatabase.proxyTarget"

// Registering the instance in the proxy's target group is not enough on its own: the
// target stays unavailable until a security group rule permits the connection, which
// the aws/rds/dbProxy::aws/rds/dbInstance link writes. That link only exists if the
// proxy selects the instance.
func (s *SQLDatabaseTransformTestSuite) Test_proxy_selects_the_instance_it_fronts() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		"standard",
		map[string]string{"app": "orders"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	proxy := resources["myDb_rds_proxy"]
	s.Require().NotNil(proxy)
	s.Require().NotNil(proxy.LinkSelector)
	s.Require().NotNil(proxy.LinkSelector.ByLabel)
	s.Equal("orders", proxy.LinkSelector.ByLabel.Values[proxyTargetLabel])

	inst := resources["myDb_rds_instance"]
	s.Require().NotNil(inst)
	s.Equal("orders", inst.Metadata.Labels.Values[proxyTargetLabel])
}

// byLabel matching is a union, so the selector must not also reach the other resources
// this database emits. They carry the database's own labels, which is why the proxy
// selects on a key of its own rather than on those.
func (s *SQLDatabaseTransformTestSuite) Test_proxy_selector_reaches_only_the_instance() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
			"readReplicas", core.MappingNodeFromBool(true),
		),
		"standard",
		map[string]string{"app": "orders"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	for _, name := range []string{
		"myDb_rds_proxy",
		"myDb_rds_proxy_role",
		"myDb_rds_proxy_target_group",
		"myDb_rds_subnet_group",
		// The target group registers the primary, so a read replica is not on the far
		// end of the proxy's connection.
		"myDb_rds_reader",
	} {
		resource, ok := resources[name]
		if !ok || resource.Metadata == nil || resource.Metadata.Labels == nil {
			continue
		}
		s.NotContains(
			resource.Metadata.Labels.Values,
			proxyTargetLabel,
			"%s must not be selectable as the proxy's target", name,
		)
	}
}

// The label map is shared across every resource the database emits, so adding to it in
// place would put the proxy-target label on all of them.
func (s *SQLDatabaseTransformTestSuite) Test_proxy_target_label_does_not_displace_db_labels() {
	out := s.transformDBWithVPC(
		core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"engine", core.MappingNodeFromString("postgres"),
		),
		"standard",
		map[string]string{"app": "orders", "tier": "data"},
	)
	resources := out.TransformedBlueprint.Resources.Values

	inst := resources["myDb_rds_instance"]
	s.Require().NotNil(inst)
	s.Equal("orders", inst.Metadata.Labels.Values["app"])
	s.Equal("data", inst.Metadata.Labels.Values["tier"])
	s.Equal("orders", inst.Metadata.Labels.Values[proxyTargetLabel])
}

func (s *SQLDatabaseTransformTestSuite) transformDBWithVPC(
	dbSpec *core.MappingNode,
	vpcPreset string,
	dbLabels map[string]string,
) *transform.SpecTransformerTransformOutput {
	dbRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/sqlDatabase"},
		Spec: dbSpec,
	}
	if dbLabels != nil {
		dbRes.Metadata = &schema.Metadata{Labels: &schema.StringMap{Values: dbLabels}}
	}
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myDb": dbRes,
				"myVpc": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("app-net"),
						"preset", core.MappingNodeFromString(vpcPreset),
					),
				},
			},
		},
	}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: bp,
			LinkGraph: placementLinkGraph{
				vpc:        "myVpc",
				target:     "myDb",
				targetType: "celerity/sqlDatabase",
			},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}

func (s *SQLDatabaseTransformTestSuite) transformDBWithConfig(
	dbSpec *core.MappingNode,
	configVars map[string]*core.ScalarValue,
	dbLabels map[string]string,
) *transform.SpecTransformerTransformOutput {
	dbRes := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/sqlDatabase"},
		Spec: dbSpec,
	}
	if dbLabels != nil {
		dbRes.Metadata = &schema.Metadata{Labels: &schema.StringMap{Values: dbLabels}}
	}
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myDb": dbRes,
				"myVpc": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("app-net"),
						"preset", core.MappingNodeFromString("standard"),
					),
				},
			},
		},
	}
	ctx := &fakeTransformContext{
		configVars: configVars,
		contextVars: map[string]*core.ScalarValue{
			core.ValidationContextVariableName: core.ScalarFromBool(true),
			"deployTarget":                     core.ScalarFromString(shared.AWSServerless),
		},
	}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: bp,
			LinkGraph: placementLinkGraph{
				vpc:        "myVpc",
				target:     "myDb",
				targetType: "celerity/sqlDatabase",
			},
			TransformerContext: ctx,
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}

// transformDBWithConfigAndValues transforms a database with deploy config plus
// top-level blueprint values that reference its outputs.
func (s *SQLDatabaseTransformTestSuite) transformDBWithConfigAndValues(
	dbSpec *core.MappingNode,
	configVars map[string]*core.ScalarValue,
	values map[string]*schema.Value,
) *transform.SpecTransformerTransformOutput {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myDb": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/sqlDatabase"},
					Spec: dbSpec,
				},
				"myVpc": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/vpc"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("app-net"),
						"preset", core.MappingNodeFromString("standard"),
					),
				},
			},
		},
		Values: &schema.ValueMap{Values: values},
	}
	ctx := &fakeTransformContext{
		configVars: configVars,
		contextVars: map[string]*core.ScalarValue{
			core.ValidationContextVariableName: core.ScalarFromBool(true),
			"deployTarget":                     core.ScalarFromString(shared.AWSServerless),
		},
	}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: bp,
			LinkGraph: placementLinkGraph{
				vpc:        "myVpc",
				target:     "myDb",
				targetType: "celerity/sqlDatabase",
			},
			TransformerContext: ctx,
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	s.Require().NotNil(out.TransformedBlueprint.Values)
	return out
}

func valueRefTo(expr string) *schema.Value {
	node, _ := shared.SubstitutionMappingNode(expr)
	return &schema.Value{
		Type:  &schema.ValueTypeWrapper{Value: schema.ValueTypeString},
		Value: node,
	}
}

// assertValueRef checks a transformed blueprint value resolves to a resource
// property reference on resName with the given field path.
func (s *SQLDatabaseTransformTestSuite) assertValueRef(
	v *schema.Value,
	resName string,
	fields ...string,
) {
	s.Require().NotNil(v)
	s.Require().NotNil(v.Value)
	s.Require().NotNil(v.Value.StringWithSubstitutions)
	parts := v.Value.StringWithSubstitutions.Values
	s.Require().Len(parts, 1)
	prop := parts[0].SubstitutionValue.ResourceProperty
	s.Require().NotNil(prop, "expected a resource-property reference")
	s.Equal(resName, prop.ResourceName)
	gotFields := []string{}
	for _, item := range prop.Path {
		gotFields = append(gotFields, item.FieldName)
	}
	s.Equal(fields, gotFields)
}

// placementLinkGraph is a minimal graph with a single vpc -> target placement edge.
type placementLinkGraph struct {
	vpc        string
	target     string
	targetType string
}

func (g placementLinkGraph) Edges() []*linktypes.ResolvedLink {
	return []*linktypes.ResolvedLink{g.edge()}
}

func (g placementLinkGraph) EdgesFrom(name string) []*linktypes.ResolvedLink {
	if name == g.vpc {
		return []*linktypes.ResolvedLink{g.edge()}
	}
	return nil
}

func (g placementLinkGraph) EdgesTo(name string) []*linktypes.ResolvedLink {
	if name == g.target {
		return []*linktypes.ResolvedLink{g.edge()}
	}
	return nil
}

func (placementLinkGraph) Resource(string) (*schema.Resource, linktypes.ResourceClass, bool) {
	return nil, "", false
}

func (g placementLinkGraph) edge() *linktypes.ResolvedLink {
	return &linktypes.ResolvedLink{
		Source:     g.vpc,
		Target:     g.target,
		SourceType: "celerity/vpc",
		TargetType: g.targetType,
	}
}
