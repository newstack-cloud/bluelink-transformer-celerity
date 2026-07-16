package bucket

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

func bucketResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "BucketDefinition",
		Description: "A managed object storage bucket that handlers read from and write to. On " +
			"AWS this maps to an S3 bucket.",
		Attributes: map[string]*provider.ResourceDefinitionsSchema{
			"name": {
				Type: provider.ResourceDefinitionsSchemaTypeString,
				Description: "The unique name of the bucket. If a name is not provided, a unique name is " +
					"generated based on the blueprint the bucket is defined in. On AWS this maps " +
					"to the S3 bucket name (create-only).",
			},
			"encryption": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Description: "Server-side encryption for objects stored in the bucket.",
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"encryptionKeyId": {
						Type:        provider.ResourceDefinitionsSchemaTypeString,
						Description: "The ID of the KMS key used to encrypt objects. When set, KMS encryption is used.",
					},
					"encryptionAlgorithm": {
						Type: provider.ResourceDefinitionsSchemaTypeString,
						Description: "The encryption algorithm. On AWS this is the S3 SSE algorithm " +
							"(for example \"AES256\" or \"aws:kms\"). Defaults to \"aws:kms\" when an " +
							"encryptionKeyId is set, otherwise \"AES256\".",
					},
				},
			},
			"cors": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Description: "Cross-origin resource sharing (CORS) rules for the bucket.",
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"corsRules": {
						Type:        provider.ResourceDefinitionsSchemaTypeArray,
						Description: "The CORS rules to apply to the bucket.",
						Items: &provider.ResourceDefinitionsSchema{
							Type: provider.ResourceDefinitionsSchemaTypeObject,
							Attributes: map[string]*provider.ResourceDefinitionsSchema{
								"id": {
									Type:        provider.ResourceDefinitionsSchemaTypeString,
									Description: "An optional identifier for the rule.",
								},
								"allowedOrigins": {
									Type:        provider.ResourceDefinitionsSchemaTypeArray,
									Description: "The origins allowed to access the bucket.",
									Items:       &provider.ResourceDefinitionsSchema{Type: provider.ResourceDefinitionsSchemaTypeString},
								},
								"allowedHeaders": {
									Type:        provider.ResourceDefinitionsSchemaTypeArray,
									Description: "The headers allowed in a preflight request.",
									Items:       &provider.ResourceDefinitionsSchema{Type: provider.ResourceDefinitionsSchemaTypeString},
								},
								"allowedMethods": {
									Type:        provider.ResourceDefinitionsSchemaTypeArray,
									Description: "The HTTP methods allowed across origins.",
									Items:       &provider.ResourceDefinitionsSchema{Type: provider.ResourceDefinitionsSchemaTypeString},
								},
								"exposedHeaders": {
									Type:        provider.ResourceDefinitionsSchemaTypeArray,
									Description: "The response headers exposed to the browser.",
									Items:       &provider.ResourceDefinitionsSchema{Type: provider.ResourceDefinitionsSchemaTypeString},
								},
								"maxAge": {
									Type:        provider.ResourceDefinitionsSchemaTypeInteger,
									Description: "The time in seconds a browser may cache a preflight response.",
								},
							},
						},
					},
				},
			},
			"versioning": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Description: "Object versioning configuration for the bucket.",
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"status": {
						Type:        provider.ResourceDefinitionsSchemaTypeString,
						Description: "The versioning status (for example \"Enabled\" or \"Suspended\").",
					},
				},
			},
			"website": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Description: "Static website hosting configuration for the bucket.",
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"mainPageDocument": {
						Type:        provider.ResourceDefinitionsSchemaTypeString,
						Description: "The index document served for the website. Maps to the S3 index document.",
					},
					"notFoundDocument": {
						Type:        provider.ResourceDefinitionsSchemaTypeString,
						Description: "The error document served for the website. Maps to the S3 error document.",
					},
				},
			},
			"logging": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Description: "Server access logging configuration for the bucket.",
				Attributes: map[string]*provider.ResourceDefinitionsSchema{
					"destinationBucket": {
						Type:        provider.ResourceDefinitionsSchemaTypeString,
						Description: "The bucket that receives access logs. Maps to the S3 destination bucket name.",
					},
					"logFilePrefix": {
						Type:        provider.ResourceDefinitionsSchemaTypeString,
						Description: "The key prefix applied to log objects.",
					},
				},
			},

			// Deferred configs: accepted here so blueprints validate, but not yet
			// mapped by the aws-serverless emit — a warning diagnostic is raised when
			// either is set (see the emit). Scheduled for a follow-up pass
			// (replication's role is sourced from deploy config).
			"lifecycle": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Nullable:    true,
				Description: "Object lifecycle rules. Not yet mapped on aws-serverless (follow-up pass).",
			},
			"replication": {
				Type:        provider.ResourceDefinitionsSchemaTypeObject,
				Nullable:    true,
				Description: "Cross-region replication. Not yet mapped on aws-serverless (follow-up pass).",
			},

			// Computed output.
			"id": {
				Type:     provider.ResourceDefinitionsSchemaTypeString,
				Computed: true,
				Description: "The ID of the created bucket in the target environment. On AWS " +
					"this is the S3 bucket ARN.",
			},
		},
	}
}
