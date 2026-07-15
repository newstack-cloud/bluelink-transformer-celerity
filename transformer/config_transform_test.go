//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type ConfigTransformTestSuite struct {
	suite.Suite
}

func TestConfigTransformTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigTransformTestSuite))
}

func (s *ConfigTransformTestSuite) Test_all_secret_values_emit_a_secretsmanager_secret() {
	spec := core.MappingNodeFields(
		"name", core.MappingNodeFromString("appConfig"),
		"values", core.MappingNodeFields(
			"dbPassword", core.MappingNodeFromString("s3cr3t"),
			"apiKey", core.MappingNodeFromString("abc123"),
		),
	)

	out := s.transformConfig(spec, configContext("orders"))
	resources := out.TransformedBlueprint.Resources.Values

	secret, ok := resources["myConfig_config_secret"]
	s.Require().True(ok, "expected a Secrets Manager secret when no plaintext keys are set")
	s.Equal("aws/secretsmanager/secret", secret.Type.Value)
	s.Equal("/celerity/orders/appConfig", core.StringValue(secret.Spec.Fields["name"]))
	s.Require().NotNil(secret.Spec.Fields["secretString"], "expected a secretString blob")

	// tags is a LIST of {key, value} for aws/secretsmanager/secret.
	tags := secret.Spec.Fields["tags"]
	s.Require().NotNil(tags)
	s.Require().Len(tags.Items, 1)
	s.Equal("team", core.StringValue(tags.Items[0].Fields["key"]))
	s.Equal("payments", core.StringValue(tags.Items[0].Fields["value"]))

	// framework annotations, infrastructure category.
	s.Equal("celerity/config", annotationLiteral(secret.Metadata.Annotations, transformutils.AnnotationSourceAbstractType))
	s.Equal("infrastructure", annotationLiteral(secret.Metadata.Annotations, transformutils.AnnotationResourceCategory))

	// No Parameter Store resources in the all-secret case.
	s.NotContains(resources, "myConfig_config_param_path")
}

func (s *ConfigTransformTestSuite) Test_plaintext_keys_emit_an_ssm_parameter_tree() {
	spec := core.MappingNodeFields(
		"name", core.MappingNodeFromString("appConfig"),
		"values", core.MappingNodeFields(
			"logLevel", core.MappingNodeFromString("info"),
			"dbPassword", core.MappingNodeFromString("s3cr3t"),
		),
		"plaintext", &core.MappingNode{
			Items: []*core.MappingNode{core.MappingNodeFromString("logLevel")},
		},
	)

	out := s.transformConfig(spec, configContext("orders"))
	resources := out.TransformedBlueprint.Resources.Values

	tree, ok := resources["myConfig_config_param_tree"]
	s.Require().True(ok, "expected a single SSM parameterTree for a mixed store")
	s.Equal("aws/ssm/parameterTree", tree.Type.Value)
	s.Equal("/celerity/orders/appConfig", core.StringValue(tree.Spec.Fields["path"]))

	// Plaintext key -> values map (String parameters).
	vals := tree.Spec.Fields["values"]
	s.Require().NotNil(vals, "expected a values map for the plaintext key")
	s.Equal("info", core.StringValue(vals.Fields["logLevel"]))
	s.Nil(vals.Fields["dbPassword"], "secret key must not appear in values")

	// Non-plaintext key -> secureValues map (SecureString parameters).
	secure := tree.Spec.Fields["secureValues"]
	s.Require().NotNil(secure, "expected a secureValues map for the secret key")
	s.Equal("s3cr3t", core.StringValue(secure.Fields["dbPassword"]))
	s.Nil(secure.Fields["logLevel"], "plaintext key must not appear in secureValues")

	// tags is a MAP of string -> string on the tree.
	s.Equal("payments", core.StringValue(tree.Spec.Fields["tags"].Fields["team"]))
	s.Equal("infrastructure", annotationLiteral(tree.Metadata.Annotations, transformutils.AnnotationResourceCategory))

	// No Secrets Manager secret, and no legacy parameterPath / per-key parameter resources.
	s.NotContains(resources, "myConfig_config_secret")
	s.NotContains(resources, "myConfig_config_param_path")
	s.NotContains(resources, "myConfig_config_param_logLevel")
	s.NotContains(resources, "myConfig_config_param_dbPassword")
}

func (s *ConfigTransformTestSuite) Test_spec_id_reference_rewrites_to_the_config_derived_value() {
	storeIDRef, err := shared.SubstitutionMappingNode("${myConfig.spec.id}")
	s.Require().NoError(err)

	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myConfig": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/config"},
					Spec: core.MappingNodeFields(
						"name", core.MappingNodeFromString("appConfig"),
						"values", core.MappingNodeFields(
							"apiKey", core.MappingNodeFromString("abc123"),
						),
					),
				},
				"myHandler": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
					Spec: core.MappingNodeFields(
						"handlerName", core.MappingNodeFromString("myHandler"),
						"handler", core.MappingNodeFromString("handlers.save"),
						"runtime", core.MappingNodeFromString("nodejs24.x"),
						"environmentVariables", core.MappingNodeFields(
							"STORE_ID", storeIDRef,
						),
					),
				},
			},
		},
	}

	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)

	env := out.TransformedBlueprint.Resources.Values["myHandler_lambda_func"].
		Spec.Fields["environment"].Fields["variables"].Fields
	s.Equal("myConfig_config_store_id", valueRefName(env["STORE_ID"]))

	s.Require().NotNil(out.TransformedBlueprint.Values)
	s.Contains(
		out.TransformedBlueprint.Values.Values,
		"myConfig_config_store_id",
		"expected the config to emit the derived value the reference resolves to",
	)
}

func (s *ConfigTransformTestSuite) Test_replicate_is_reported_as_an_unsupported_diagnostic() {
	spec := core.MappingNodeFields(
		"name", core.MappingNodeFromString("appConfig"),
		"values", core.MappingNodeFields(
			"apiKey", core.MappingNodeFromString("abc123"),
		),
		"replicate", core.MappingNodeFromBool(true),
	)

	out := s.transformConfig(spec, configContext("orders"))

	s.Require().NotEmpty(out.Diagnostics, "expected a diagnostic for replicate: true")
	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelError &&
			strings.Contains(d.Message, "replication") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic mentioning replication")

	// No store is emitted when replication is requested.
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myConfig_config_secret")
	s.NotContains(out.TransformedBlueprint.Resources.Values, "myConfig_config_param_tree")
}

func (s *ConfigTransformTestSuite) transformConfig(
	spec *core.MappingNode,
	ctx transform.Context,
) *transform.SpecTransformerTransformOutput {
	bp := &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myConfig": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/config"},
					Spec: spec,
					Metadata: &schema.Metadata{
						Labels: &schema.StringMap{
							Values: map[string]string{"team": "payments"},
						},
					},
				},
			},
		},
	}

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
	return out
}

func configContext(appName string) transform.Context {
	return &fakeTransformContext{
		contextVars: map[string]*core.ScalarValue{
			"deployTarget":              core.ScalarFromString(shared.AWSServerless),
			shared.AppNameContextVarKey: core.ScalarFromString(appName),
		},
	}
}

func annotationLiteral(annos *schema.StringOrSubstitutionsMap, key string) string {
	if annos == nil {
		return ""
	}
	v, ok := annos.Values[key]
	if !ok || v == nil || len(v.Values) == 0 || v.Values[0].StringValue == nil {
		return ""
	}
	return *v.Values[0].StringValue
}

func valueRefName(node *core.MappingNode) string {
	if node == nil || node.StringWithSubstitutions == nil ||
		len(node.StringWithSubstitutions.Values) != 1 {
		return ""
	}
	sub := node.StringWithSubstitutions.Values[0].SubstitutionValue
	if sub == nil || sub.ValueReference == nil {
		return ""
	}
	return sub.ValueReference.ValueName
}
