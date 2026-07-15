package config

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func configResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "ConfigDefinition",
		Description: "A secrets and configuration store to be used by a Celerity application, " +
			"backed by a platform-specific service in the target environment " +
			"(e.g. AWS Secrets Manager or SSM Parameter Store).",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "A unique name to use for the secret and configuration store. " +
					"If a name is not provided, a unique name will be generated based on the blueprint " +
					"that the store is defined in.",
			},
			"values": {
				Type: provider.ResourceDefinitionsSchemaTypeMap,
				Description: "A map of key/value pairs to be stored in the secret and configuration store. " +
					"All values are stored as encrypted secrets unless the key is included in the " +
					"`plaintext` field. Avoid the `CELERITY_APP_` prefix in keys as it is reserved for " +
					"secrets and configuration that Celerity auto-generates for linked resources; " +
					"clashing keys will be overwritten.",
				MapValues: &provider.ResourceDefinitionsSchema{
					Type: provider.ResourceDefinitionsSchemaTypeString,
				},
			},
			"plaintext": {
				Type: provider.ResourceDefinitionsSchemaTypeArray,
				Description: "A list of keys that do not hold sensitive values and should be stored as " +
					"plain text configuration values. Depending on the target environment, these values " +
					"may be stored as plain text or included in a JSON-encoded string of key/value pairs " +
					"that is stored as an encrypted secret.",
				Items: &provider.ResourceDefinitionsSchema{
					Type: provider.ResourceDefinitionsSchemaTypeString,
				},
			},
			"replicate": {
				Type: provider.ResourceDefinitionsSchemaTypeBoolean,
				Description: "Whether the secret and configuration store should be replicated across " +
					"multiple regions. Celerity will attempt replication if the target environment " +
					"supports it; the regions must be specified in the app deploy configuration.",
			},
			"encryptionKeyId": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The ID of the encryption key to use for encrypting secrets in the store. " +
					"A default, platform-specific encryption key will be used if this field is not provided.",
			},
			"rotation": {
				Type: provider.ResourceDefinitionsSchemaTypeObject,
				Description: "Secret rotation configuration for automatically populated secrets in the " +
					"store that are managed by Celerity. This only applies to secrets auto-populated for " +
					"links between handlers or application resource types and infrastructure resources. " +
					"If not set, secrets managed by Celerity will not be rotated automatically. " +
					"Planned for v1; rotation functionality may become available in a future v0 evolution.",
			},
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The ID of the created secret and configuration store in the target " +
					"environment (e.g. an AWS Secrets Manager secret ARN or an SSM parameter prefix).",
			},
		},
	}
}
