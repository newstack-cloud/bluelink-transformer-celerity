//go:build unit

package awslambda

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/assert"
)

// Without this variable the SDK adapter loads and then has nothing to bootstrap,
// so every invocation fails with "Cannot discover module". It was set for the
// local dev container and never for a deployed function, which is why handlers
// provisioned cleanly and could not serve a single request.
func TestBuildEnvironmentVariables_setsTheAppModulePath(t *testing.T) {
	vars := BuildEnvironmentVariables(&EnvInput{
		Platform:     "aws",
		DeployTarget: "aws-serverless",
		Runtime:      "nodejs22.x",
		HandlerID:    core.MappingNodeFromString("app-module.HealthController.check"),
		EventSource:  "http",
	})

	assert.Equal(t,
		"/var/task/app-module.js",
		core.StringValue(vars.Fields["CELERITY_MODULE_PATH"]),
	)
}

// The Python adapter locates its module differently, and a guessed path would be
// worse than none.
func TestBuildEnvironmentVariables_leavesPythonAlone(t *testing.T) {
	vars := BuildEnvironmentVariables(&EnvInput{
		Runtime:     "python3.13",
		HandlerID:   core.MappingNodeFromString("app_module.HealthController.check"),
		EventSource: "http",
	})

	assert.NotContains(t, vars.Fields, "CELERITY_MODULE_PATH")
}

// A path built from an unresolved reference fails at cold start with the module
// missing, which is a long way from the reference that could not be read.
func TestBuildEnvironmentVariables_omitsThePathWhenTheHandlerIDIsNotReadable(t *testing.T) {
	for name, handlerID := range map[string]*core.MappingNode{
		"nil":         nil,
		"empty":       core.MappingNodeFromString(""),
		"no segments": core.MappingNodeFromString("appmodule"),
	} {
		vars := BuildEnvironmentVariables(&EnvInput{
			Runtime:     "nodejs22.x",
			HandlerID:   handlerID,
			EventSource: "http",
		})

		assert.NotContains(t, vars.Fields, "CELERITY_MODULE_PATH", name)
	}
}
