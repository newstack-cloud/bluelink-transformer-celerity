// Package aws holds transformer-side configuration that is shared across the
// AWS deploy targets.
//
// The fields defined here are the subset of the bluelink-provider-aws provider
// configuration that the transformer needs in order to construct AWS clients
// of its own.
//
// All fields are optional. When a field is unset, the transformer falls back
// to the AWS SDK's default credential and configuration chain (environment
// variables, shared config/credentials files, IMDS), which is the same chain
// the deploy engine and the AWS provider plugin use. This means a typical
// developer setup needs no transformer config at all; the fields exist for
// users who want to pin credentials, profiles, regions, or endpoint
// behaviour explicitly for the transform step.
package aws

import (
	"regexp"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/validation"
)

// TransformerConfigFields returns the AWS-specific transformer config fields.
//
// Returned as a map so callers can merge it with the config fields needed by
// other deploy targets (gcloud, azure).
func TransformerConfigFields() map[string]*core.ConfigFieldDefinition {
	return map[string]*core.ConfigFieldDefinition{
		// Credentials
		"aws.accessKeyId": {
			Type:  core.ScalarTypeString,
			Label: "Access Key ID",
			Description: "The access key ID the transformer uses when fetching the " +
				"build manifest (and any future AWS-side resources it needs to read). " +
				"If unset, the AWS SDK default credential chain is used " +
				"(env vars, shared credentials file, IMDS, container credentials).",
		},
		"aws.secretAccessKey": {
			Type:  core.ScalarTypeString,
			Label: "Secret Access Key",
			Description: "The secret access key paired with `aws.accessKeyId`. " +
				"Falls back to the AWS SDK default credential chain when unset.",
			Secret: true,
		},
		"aws.sessionToken": {
			Type:  core.ScalarTypeString,
			Label: "Session Token",
			Description: "Session token to use alongside temporary credentials. " +
				"Only required when `aws.accessKeyId` and `aws.secretAccessKey` come " +
				"from a temporary credential source.",
			Secret: true,
		},

		// Region and profile
		"aws.region": {
			Type:  core.ScalarTypeString,
			Label: "Region",
			Description: "The AWS region the transformer addresses when fetching the " +
				"build manifest from S3. If unset, the region is resolved from the " +
				"AWS SDK default chain (`AWS_REGION`, profile, IMDS).",
		},
		"aws.profile": {
			Type:  core.ScalarTypeString,
			Label: "Profile",
			Description: "Named profile to read from the shared AWS config and " +
				"credentials files. If unset, the default profile is used.",
		},
		"aws.sharedConfigFiles": {
			Type:  core.ScalarTypeString,
			Label: "Shared Config Files",
			Description: "A comma-separated list of paths to shared AWS config files. " +
				"Defaults to the AWS SDK default (`~/.aws/config`).",
		},
		"aws.sharedCredentialsFiles": {
			Type:  core.ScalarTypeString,
			Label: "Shared Credentials Files",
			Description: "A comma-separated list of paths to shared AWS credentials files. " +
				"Defaults to the AWS SDK default (`~/.aws/credentials`).",
		},

		// Network and TLS
		"aws.httpProxy": {
			Type:  core.ScalarTypeString,
			Label: "HTTP Proxy",
			Description: "URL of a proxy to use for HTTP requests when reaching AWS APIs. " +
				"Falls back to the `HTTP_PROXY` environment variable when unset.",
		},
		"aws.httpsProxy": {
			Type:  core.ScalarTypeString,
			Label: "HTTPS Proxy",
			Description: "URL of a proxy to use for HTTPS requests when reaching AWS APIs. " +
				"Falls back to the `HTTPS_PROXY` environment variable when unset.",
		},
		"aws.customCABundle": {
			Type:  core.ScalarTypeString,
			Label: "Custom CA Bundle",
			Description: "Path to a custom CA bundle file used for TLS connections to AWS. " +
				"Falls back to the `AWS_CA_BUNDLE` environment variable when unset.",
		},
		"aws.insecure": {
			Type:  core.ScalarTypeBool,
			Label: "Insecure",
			Description: "If true, skips TLS certificate verification for AWS API calls. " +
				"Intended for local testing against AWS-compatible mocks. Defaults to false.",
		},

		// Endpoint behaviour
		"aws.useDualStackEndpoint": {
			Type:        core.ScalarTypeBool,
			Label:       "Use Dual Stack Endpoint",
			Description: "If true, resolves AWS endpoints with DualStack capability.",
		},
		"aws.useFIPSEndpoint": {
			Type:        core.ScalarTypeBool,
			Label:       "Use FIPS Endpoint",
			Description: "If true, resolves AWS endpoints with FIPS capability.",
		},
		"aws.ec2MetadataServiceEndpoint": {
			Type:  core.ScalarTypeString,
			Label: "EC2 Metadata Service Endpoint",
			Description: "Address of the EC2 metadata service endpoint. " +
				"Falls back to the `AWS_EC2_METADATA_SERVICE_ENDPOINT` environment variable.",
		},
		"aws.ec2MetadataServiceEndpointMode": {
			Type:  core.ScalarTypeString,
			Label: "EC2 Metadata Service Endpoint Mode",
			Description: "Protocol mode for the EC2 metadata service endpoint. " +
				"Falls back to `AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE` when unset.",
			AllowedValues: []*core.ScalarValue{
				core.ScalarFromString("IPv4"),
				core.ScalarFromString("IPv6"),
			},
		},

		// Retries
		"aws.maxRetries": {
			Type:  core.ScalarTypeInteger,
			Label: "Max Retries",
			Description: "Maximum number of retries for failed AWS API requests. " +
				"Falls back to the AWS SDK default when unset.",
		},
		"aws.retryMode": {
			Type:  core.ScalarTypeString,
			Label: "Retry Mode",
			Description: "AWS SDK retry mode. " +
				"Falls back to the `AWS_RETRY_MODE` environment variable when unset.",
			AllowedValues: []*core.ScalarValue{
				core.ScalarFromString("standard"),
				core.ScalarFromString("adaptive"),
			},
		},

		// Assume role
		"aws.assumeRole.roleArn": {
			Type:         core.ScalarTypeString,
			Label:        "Assume Role ARN",
			Description:  "ARN of an IAM role to assume before calling AWS APIs.",
			ValidateFunc: validateARN,
		},
		"aws.assumeRole.duration": {
			Type:  core.ScalarTypeString,
			Label: "Assume Role Duration",
			Description: "Duration the assumed role session is valid for. " +
				"Must be between 15 minutes and 12 hours. " +
				"Valid units: ns, us (or μs), ms, s, m, h.",
			DefaultValue: core.ScalarFromString("1h"),
			Examples: []*core.ScalarValue{
				core.ScalarFromString("15m"),
				core.ScalarFromString("1h"),
				core.ScalarFromString("12h"),
			},
			ValidateFunc: validateAssumeRoleDuration,
		},
		"aws.assumeRole.externalId": {
			Type:  core.ScalarTypeString,
			Label: "Assume Role External ID",
			Description: "Optional external ID required by some cross-account role " +
				"assumption policies.",
			ValidateFunc: validation.WrapForPluginConfig(
				validation.AllOf(
					validation.StringLengthRange(2, 1224),
					validation.StringMatchesPattern(
						regexp.MustCompile(`[\w+=,.@:\/\-]*`),
					),
				),
			),
		},
		"aws.assumeRole.sessionName": {
			Type:         core.ScalarTypeString,
			Label:        "Assume Role Session Name",
			Description:  "Identifier for the assumed role session.",
			ValidateFunc: validateAssumeRoleSessionName,
		},

		// Assume role with web identity (e.g. EKS IRSA)
		"aws.assumeRoleWithWebIdentity.roleArn": {
			Type:  core.ScalarTypeString,
			Label: "Web Identity Role ARN",
			Description: "ARN of an IAM role to assume via web identity federation " +
				"(e.g. EKS IRSA).",
			ValidateFunc: validateARN,
		},
		"aws.assumeRoleWithWebIdentity.duration": {
			Type:  core.ScalarTypeString,
			Label: "Web Identity Assume Role Duration",
			Description: "Duration the web-identity-assumed session is valid for. " +
				"Must be between 15 minutes and 12 hours.",
			DefaultValue: core.ScalarFromString("1h"),
			Examples: []*core.ScalarValue{
				core.ScalarFromString("15m"),
				core.ScalarFromString("1h"),
				core.ScalarFromString("12h"),
			},
			ValidateFunc: validateAssumeRoleDuration,
		},
		"aws.assumeRoleWithWebIdentity.sessionName": {
			Type:         core.ScalarTypeString,
			Label:        "Web Identity Assume Role Session Name",
			Description:  "Identifier for the web-identity-assumed role session.",
			ValidateFunc: validateAssumeRoleSessionName,
		},
		"aws.assumeRoleWithWebIdentity.webIdentityTokenFile": {
			Type:  core.ScalarTypeString,
			Label: "Web Identity Token File",
			Description: "Path to a file containing the web identity token used " +
				"for role assumption. Falls back to the " +
				"`AWS_WEB_IDENTITY_TOKEN_FILE` environment variable when unset.",
		},

		// S3-specific
		"aws.s3.endpoint": {
			Type:  core.ScalarTypeString,
			Label: "S3 Endpoint",
			Description: "Override URL for the S3 endpoint, primarily useful for " +
				"S3-compatible mocks (LocalStack, MinIO) during local testing.",
		},
		"aws.s3.usePathStyle": {
			Type:  core.ScalarTypeBool,
			Label: "S3 Use Path Style",
			Description: "If true, the S3 client uses path-style addressing " +
				"(`https://s3.amazonaws.com/<bucket>/<key>`) rather than virtual-hosted-style " +
				"(`https://<bucket>.s3.amazonaws.com/<key>`). " +
				"Required by some S3-compatible stores.",
		},
	}
}
