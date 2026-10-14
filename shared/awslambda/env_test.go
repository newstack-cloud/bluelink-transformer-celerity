//go:build unit

package awslambda

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/suite"
)

type BuildEnvVarsSuite struct {
	suite.Suite
}

func (s *BuildEnvVarsSuite) Test_bootstrap_routing_and_default_log_format() {
	result := BuildEnvironmentVariables(
		&EnvInput{
			Platform:     "aws",
			DeployTarget: "aws-serverless",
			HandlerID:    core.MappingNodeFromString("handlers.save"),
			EventSource:  "http",
		},
	)

	expected := baseExpectedEnvVars()

	for key, expectedValue := range expected {
		actualValue, ok := envStr(result, key)
		s.True(ok, "Expected key %s not found in result", key)
		s.Equal(expectedValue, actualValue, "Value for key %s does not match", key)
	}

	_, hasTag := envStr(result, "CELERITY_HANDLER_TAG")
	s.False(hasTag, "Did not expect CELERITY_HANDLER_TAG to be set")
	_, hasTelemetry := envStr(result, "CELERITY_TELEMETRY_ENABLED")
	s.False(hasTelemetry, "Did not expect CELERITY_TELEMETRY_ENABLED to be set")
}

func (s *BuildEnvVarsSuite) Test_includes_routing_tag_and_tracing() {
	result := BuildEnvironmentVariables(
		&EnvInput{
			Platform:      "aws",
			DeployTarget:  "aws-serverless",
			HandlerID:     core.MappingNodeFromString("handlers.save"),
			EventSource:   "http",
			RoutingTag:    "v1",
			HasRoutingTag: true,
			Tracing:       true,
		},
	)

	expected := baseExpectedEnvVars()
	expected["CELERITY_HANDLER_TAG"] = "v1"
	expected["CELERITY_TELEMETRY_ENABLED"] = "true"

	for key, expectedValue := range expected {
		actualValue, ok := envStr(result, key)
		s.True(ok, "Expected key %s not found in result", key)
		s.Equal(expectedValue, actualValue, "Value for key %s does not match", key)
	}
}

func (s *BuildEnvVarsSuite) Test_stamps_the_routing_key_for_a_whole_consumer_function() {
	// The tag names the consumer rather than one of its handlers, so the function
	// routes the batch itself and needs to know which field carries the route.
	result := BuildEnvironmentVariables(
		&EnvInput{
			Platform:      "aws",
			DeployTarget:  "aws-serverless",
			HandlerID:     core.MappingNodeFromString("app-module.UserEventsConsumer"),
			EventSource:   "consumer",
			RoutingTag:    "userEventsConsumer",
			HasRoutingTag: true,
			RoutingKey:    "type",
		},
	)

	tag, ok := envStr(result, "CELERITY_HANDLER_TAG")
	s.True(ok)
	s.Equal("userEventsConsumer", tag)

	key, ok := envStr(result, "CELERITY_ROUTING_KEY")
	s.True(ok, "a whole-consumer function must be told the routing field")
	s.Equal("type", key)
}

func (s *BuildEnvVarsSuite) Test_omits_the_routing_key_for_a_single_handler_function() {
	// Nothing to route: the tag already selected the handler.
	result := BuildEnvironmentVariables(
		&EnvInput{
			Platform:      "aws",
			DeployTarget:  "aws-serverless",
			HandlerID:     core.MappingNodeFromString("app-module.TaskConsumer.handle"),
			EventSource:   "consumer",
			RoutingTag:    "taskConsumer::handle",
			HasRoutingTag: true,
		},
	)

	_, ok := envStr(result, "CELERITY_ROUTING_KEY")
	s.False(ok, "a function serving one handler must not be told to route")
}

func (s *BuildEnvVarsSuite) Test_user_env_overrides_defaults() {
	result := BuildEnvironmentVariables(
		&EnvInput{
			Platform:     "aws",
			DeployTarget: "aws-serverless",
			HandlerID:    core.MappingNodeFromString("handlers.save"),
			EventSource:  "http",
			UserEnv: map[string]*core.MappingNode{
				"CELERITY_LOG_FORMAT": core.MappingNodeFromString("text"),
				"CUSTOM_VAR":          core.MappingNodeFromString("customValue"),
			},
		},
	)

	expected := baseExpectedEnvVars()
	expected["CELERITY_LOG_FORMAT"] = "text"
	expected["CUSTOM_VAR"] = "customValue"

	for key, expectedValue := range expected {
		actualValue, ok := envStr(result, key)
		s.True(ok, "Expected key %s not found in result", key)
		s.Equal(expectedValue, actualValue, "Value for key %s does not match", key)
	}
}

func (s *BuildEnvVarsSuite) Test_stamps_user_config_store_id_and_kind_per_namespace() {
	result := BuildEnvironmentVariables(
		&EnvInput{
			Platform:     "aws",
			DeployTarget: "aws-serverless",
			HandlerID:    core.MappingNodeFromString("handlers.save"),
			EventSource:  "http",
			UserConfigStores: []UserConfigStore{
				{
					EnvNamespace: "APPCONFIG",
					Namespace:    "appConfig",
					StoreID:      core.MappingNodeFromString("/celerity/app/appConfig"),
					Kind:         "parameter-store",
				},
				{
					EnvNamespace: "SECRETS",
					Namespace:    "secrets",
					StoreID:      core.MappingNodeFromString("arn:aws:secretsmanager:secret"),
					Kind:         "secrets-manager",
				},
			},
		},
	)

	expected := map[string]string{
		"CELERITY_CONFIG_APPCONFIG_STORE_ID":   "/celerity/app/appConfig",
		"CELERITY_CONFIG_APPCONFIG_STORE_KIND": "parameter-store",
		"CELERITY_CONFIG_APPCONFIG_NAMESPACE":  "appConfig",
		"CELERITY_CONFIG_SECRETS_STORE_ID":     "arn:aws:secretsmanager:secret",
		"CELERITY_CONFIG_SECRETS_STORE_KIND":   "secrets-manager",
		"CELERITY_CONFIG_SECRETS_NAMESPACE":    "secrets",
	}
	for key, expectedValue := range expected {
		actualValue, ok := envStr(result, key)
		s.True(ok, "Expected key %s not found in result", key)
		s.Equal(expectedValue, actualValue, "Value for key %s does not match", key)
	}

	// The user-store list must never stamp the internal resources namespace vars.
	_, hasInternal := envStr(result, "CELERITY_CONFIG_RESOURCES_STORE_ID")
	s.False(hasInternal, "internal resources store vars are wired separately")
}

func envStr(node *core.MappingNode, key string) (string, bool) {
	value, ok := node.Fields[key]
	if !ok {
		return "", false
	}

	return core.StringValue(value), true
}

func baseExpectedEnvVars() map[string]string {
	return map[string]string{
		"CELERITY_PLATFORM":      "aws",
		"CELERITY_DEPLOY_TARGET": "aws-serverless",
		"CELERITY_HANDLER_ID":    "handlers.save",
		"CELERITY_HANDLER_TYPE":  "http",
		"CELERITY_LOG_FORMAT":    "json",
	}
}

// The env-var name cannot carry the namespace: it is upper-cased and stripped to
// [A-Z0-9_]. An SDK left to infer the original can only lower-case, which resolves
// "appConfig" to "appconfig" and "orders-config" to "orders_config", registering the
// namespace under a name no handler asks for. That fails at cold start with the
// namespace simply missing, which is why the authored name is stamped explicitly.
func (s *BuildEnvVarsSuite) Test_stamps_the_authored_namespace_that_the_env_var_name_cannot_carry() {
	cases := []struct {
		name         string
		envNamespace string
		namespace    string
	}{
		{
			name:         "camel case, which lower-casing would break",
			envNamespace: "APPCONFIG",
			namespace:    "appConfig",
		},
		{
			name:         "a hyphen, which sanitization replaces with an underscore",
			envNamespace: "ORDERS_CONFIG",
			namespace:    "orders-config",
		},
		{
			name:         "already lower case, where inference would have worked",
			envNamespace: "RESOURCES",
			namespace:    "resources",
		},
	}

	for _, testCase := range cases {
		s.Run(testCase.name, func() {
			result := BuildEnvironmentVariables(
				&EnvInput{
					Platform:     "aws",
					DeployTarget: "aws-serverless",
					HandlerID:    core.MappingNodeFromString("handlers.save"),
					EventSource:  "http",
					UserConfigStores: []UserConfigStore{
						{
							EnvNamespace: testCase.envNamespace,
							Namespace:    testCase.namespace,
							StoreID:      core.MappingNodeFromString("/celerity/app/store"),
							Kind:         "parameter-store",
						},
					},
				},
			)

			actual, ok := envStr(result, "CELERITY_CONFIG_"+testCase.envNamespace+"_NAMESPACE")
			s.Require().True(ok, "no namespace env var stamped for %s", testCase.envNamespace)
			s.Equal(testCase.namespace, actual)
		})
	}
}

func TestBuildEnvVarsSuite(t *testing.T) {
	suite.Run(t, new(BuildEnvVarsSuite))
}
