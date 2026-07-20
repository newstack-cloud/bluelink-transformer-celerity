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
					StoreID:      core.MappingNodeFromString("/celerity/app/appConfig"),
					Kind:         "parameter-store",
				},
				{
					EnvNamespace: "SECRETS",
					StoreID:      core.MappingNodeFromString("arn:aws:secretsmanager:secret"),
					Kind:         "secrets-manager",
				},
			},
		},
	)

	expected := map[string]string{
		"CELERITY_CONFIG_APPCONFIG_STORE_ID":   "/celerity/app/appConfig",
		"CELERITY_CONFIG_APPCONFIG_STORE_KIND": "parameter-store",
		"CELERITY_CONFIG_SECRETS_STORE_ID":     "arn:aws:secretsmanager:secret",
		"CELERITY_CONFIG_SECRETS_STORE_KIND":   "secrets-manager",
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

func TestBuildEnvVarsSuite(t *testing.T) {
	suite.Run(t, new(BuildEnvVarsSuite))
}
