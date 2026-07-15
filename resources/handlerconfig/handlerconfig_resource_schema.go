package handlerconfig

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func handlerConfigResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "HandlerConfigDefinition",
		Description: "Shared configuration for a set of handlers, useful for sharing configuration " +
			"between multiple handlers in a blueprint. Handlers linked to this resource inherit any " +
			"fields they do not set themselves.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"runtime": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The Celerity runtime identifier for handlers that inherit from this " +
					"config (e.g. `nodejs24.x`, `python3.13.x`). This is a portable identifier: the CLI " +
					"maps it to a local runtime image for development, and the Celerity deploy engine " +
					"maps it to the target platform's concrete runtime.",
			},
			"codeLocation": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The location of the handler code that will be loaded by the runtime; " +
					"this can be a directory or a file path without the file extension. In an OS-only " +
					"runtime, this is expected to be a directory containing the handler binary.",
			},
			"memory": {
				Type: provider.ResourceDefinitionsSchemaTypeInteger,
				Description: "The amount of memory (in MB) available to handlers at runtime. " +
					"In containerised or custom server environments, the highest value across all " +
					"handlers is used as a guide to configure the memory available to the runtime.",
			},
			"timeout": {
				Type: provider.ResourceDefinitionsSchemaTypeInteger,
				Description: "The maximum amount of time in seconds that handlers can run for " +
					"before being terminated.",
			},
			"tracingEnabled": {
				Type: provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "Whether or not to enable tracing for handlers. The tracing behaviour " +
					"will vary depending on the target environment.",
			},
			"environmentVariables": {
				Type: provider.ResourceDefinitionsSchemaTypeMap,
				Description: "A mapping of environment variables that will be available to handlers " +
					"at runtime. When deployed to containerised or custom server environments, " +
					"environment variables shared between functions will be merged and made available " +
					"to the runtime.",
				MapValues: &provider.ResourceDefinitionsSchema{
					Type: provider.ResourceDefinitionsSchemaTypeString,
				},
			},
		},
		// No required fields and no defaults, every field is an optional inheritance source;
		// defaults are defined in the handler schema.
	}
}
