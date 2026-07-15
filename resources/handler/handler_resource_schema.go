package handler

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
)

func handlerResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "HandlerDefinition",
		Description: "A handler that can carry out a step in a workflow, process HTTP requests, " +
			"WebSocket messages, or events from queues/message brokers, scheduled events, " +
			"or cloud services.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"handlerName": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The name to identify the handler that will be loaded by the runtime. " +
					"In FaaS target environments this will be the name of the function resource " +
					"in the cloud provider.",
			},
			"handler": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The name of the handler function that will be loaded by the runtime. " +
					"In an OS-only runtime, this is expected to be the name of the handler binary.",
			},
			// codeLocation and runtime are inheritable, so they are not required
			// on the handler resource itself; an inherited or (for runtime) absent
			// value is resolved later in the inheritance chain.
			"codeLocation": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The location of the handler code that will be loaded by the runtime; " +
					"this can be a directory or a file path without the file extension. In an OS-only " +
					"runtime, this is expected to be a directory containing the handler binary. " +
					"This field can be inherited from a linked `celerity/handlerConfig` or " +
					"`metadata.sharedHandlerConfig` and must be provided at some level.",
			},
			"runtime": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The Celerity runtime identifier for this handler (e.g. `nodejs24.x`, " +
					"`python3.13.x`). This is a portable identifier: the CLI maps it to a local runtime " +
					"image for development, and the Celerity deploy engine maps it to the target " +
					"platform's concrete runtime. This field can be inherited from a linked " +
					"`celerity/handlerConfig` or `metadata.sharedHandlerConfig`.",
			},
			"memory": {
				Type:    provider.ResourceDefinitionsSchemaTypeInteger,
				Default: core.MappingNodeFromInt(512),
				Description: "The amount of memory (in MB) available to the handler at runtime. " +
					"In containerised or custom server environments, the highest value across all " +
					"handlers is used as a guide to configure the memory available to the runtime. " +
					"Defaults to 512MB.",
			},
			"timeout": {
				Type:    provider.ResourceDefinitionsSchemaTypeInteger,
				Default: core.MappingNodeFromInt(30),
				Description: "The maximum amount of time in seconds that the handler can run for " +
					"before being terminated. Defaults to 30 seconds.",
			},
			"tracingEnabled": {
				Type:    provider.ResourceDefinitionsSchemaTypeBoolean,
				Default: core.MappingNodeFromBool(false),
				Description: "Whether or not to enable tracing for the handler. The tracing behaviour " +
					"will vary depending on the target environment. Tracing is not enabled by default.",
			},
			"environmentVariables": {
				Type: provider.ResourceDefinitionsSchemaTypeMap,
				Description: "A mapping of environment variables that will be available to the handler " +
					"at runtime. When deployed to containerised or custom server environments, " +
					"environment variables shared between functions will be merged and made available " +
					"to the runtime.",
				MapValues: &provider.ResourceDefinitionsSchema{
					Type: provider.ResourceDefinitionsSchemaTypeString,
				},
			},
			// id is the handler output; declaring it as computed lets
			// ${resources.<handler>.spec.id} validate pre-transform.
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The unique identifier of the handler resource. For serverless " +
					"environments, this will be a unique ID for a function such as an AWS Lambda " +
					"function ARN. For containerised or custom server environments where handlers " +
					"are loaded into the runtime in a single process, this will be the same value " +
					"as the `handlerName` field.",
			},
		},
		// Only handlerName and handler are non-inheritable; every other field can
		// be supplied through the inheritance chain.
		Required: []string{"handlerName", "handler"},
	}
}
