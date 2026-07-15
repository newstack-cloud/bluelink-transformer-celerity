package awslambda

import (
	"maps"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

type EnvInput struct {
	Platform      string
	DeployTarget  string
	HandlerID     *core.MappingNode
	EventSource   string
	RoutingTag    string
	HasRoutingTag bool
	Tracing       bool
	UserEnv       map[string]*core.MappingNode
}

func BuildEnvironmentVariables(input *EnvInput) *core.MappingNode {
	vars := map[string]*core.MappingNode{
		"CELERITY_PLATFORM":      core.MappingNodeFromString(input.Platform),
		"CELERITY_DEPLOY_TARGET": core.MappingNodeFromString(input.DeployTarget),
		"CELERITY_HANDLER_ID":    input.HandlerID,
		"CELERITY_HANDLER_TYPE":  core.MappingNodeFromString(input.EventSource),
	}

	if input.HasRoutingTag {
		vars["CELERITY_HANDLER_TAG"] = core.MappingNodeFromString(input.RoutingTag)
	}

	// 3. Config-store discovery — Stage 5 (needs aws.configStore.* keys + outbound-link gating)

	if input.Tracing {
		vars["CELERITY_TELEMETRY_ENABLED"] = core.MappingNodeFromString("true")
	}

	_, userSetLogFormat := input.UserEnv["CELERITY_LOG_FORMAT"]

	maps.Copy(vars, input.UserEnv)
	if !userSetLogFormat {
		// default unless user has overridden
		vars["CELERITY_LOG_FORMAT"] = core.MappingNodeFromString("json")
	}

	return &core.MappingNode{
		Fields: vars,
	}
}
