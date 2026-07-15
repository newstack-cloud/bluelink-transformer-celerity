package aws

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// ConfigLoader is the interface used by ConfigFromTransformContext to load
// configuration for AWS SDK clients.
type ConfigLoader interface {
	LoadDefaultConfig(
		ctx context.Context,
		optFns ...func(*config.LoadOptions) error,
	) (aws.Config, error)
}

// DefaultConfigLoader is the production ConfigLoader implementation.
type DefaultConfigLoader struct{}

// LoadDefaultConfig delegates to config.LoadDefaultConfig.
func (l *DefaultConfigLoader) LoadDefaultConfig(
	ctx context.Context,
	optFns ...func(*config.LoadOptions) error,
) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, optFns...)
}

// ConfigFromTransformContext assembles an aws.Config from the transformer's
// aws.* config variables. All fields are optional; unset fields fall through
// to the AWS SDK default chain.
//
// `env` is the process environment as a map (typically built once at plugin
// startup with envMapFromStrings). It exists so the option helpers can
// override the SDK's env-fallback behaviour with the transformer config's
// precedence rule (config > env > SDK default).
//
// `loader` may be nil; the default loader is used when so.
func ConfigFromTransformContext(
	ctx context.Context,
	transformCtx transform.Context,
	env map[string]string,
	loader ConfigLoader,
) (*aws.Config, error) {
	if loader == nil {
		loader = &DefaultConfigLoader{}
	}

	opts := []func(*config.LoadOptions) error{}
	opts = append(opts, regionOptions(transformCtx)...)
	opts = append(opts, retryConfigOptions(transformCtx, env)...)
	opts = append(opts, credentialOptions(transformCtx)...)
	opts = append(opts, sharedEndpointOptions(transformCtx)...)
	opts = append(opts, ec2MetadataServiceOptions(transformCtx, env)...)

	certOpts, err := certOptions(transformCtx, env)
	if err != nil {
		return nil, err
	}
	opts = append(opts, certOpts...)

	opts = append(opts, httpClientOptions(transformCtx)...)
	opts = append(opts, assumeRoleOptions(transformCtx)...)
	opts = append(opts, assumeRoleWithWebIdentityOptions(transformCtx)...)

	cfg, err := loader.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func regionOptions(transformCtx transform.Context) []func(*config.LoadOptions) error {
	opts := []func(*config.LoadOptions) error{}

	region, ok := transformCtx.TransformerConfigVariable("aws.region")
	if ok && !core.IsScalarNil(region) {
		opts = append(opts, config.WithRegion(core.StringValueFromScalar(region)))
	}

	return opts
}

func retryConfigOptions(
	transformCtx transform.Context,
	env map[string]string,
) []func(*config.LoadOptions) error {
	opts := []func(*config.LoadOptions) error{}

	retryMode, hasRetryMode := configValueFallbackToEnv(
		transformCtx, env, "aws.retryMode", "AWS_RETRY_MODE",
	)
	if hasRetryMode && !core.IsScalarNil(retryMode) {
		opts = append(opts, config.WithRetryMode(
			aws.RetryMode(core.StringValueFromScalar(retryMode)),
		))
	}

	maxRetries, hasMaxRetries := transformCtx.TransformerConfigVariable("aws.maxRetries")
	if hasMaxRetries && !core.IsScalarNil(maxRetries) {
		opts = append(opts, config.WithRetryMaxAttempts(
			core.IntValueFromScalar(maxRetries),
		))
	}

	return opts
}

func credentialOptions(transformCtx transform.Context) []func(*config.LoadOptions) error {
	opts := []func(*config.LoadOptions) error{}

	accessKeyID, hasAccessKeyID := transformCtx.TransformerConfigVariable("aws.accessKeyId")
	secretAccessKey, hasSecretAccessKey := transformCtx.TransformerConfigVariable("aws.secretAccessKey")
	sessionToken, _ := transformCtx.TransformerConfigVariable("aws.sessionToken")

	if hasAccessKeyID && !core.IsScalarNil(accessKeyID) &&
		hasSecretAccessKey && !core.IsScalarNil(secretAccessKey) {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				core.StringValueFromScalar(accessKeyID),
				core.StringValueFromScalar(secretAccessKey),
				core.StringValueFromScalar(sessionToken),
			),
		))
	}

	if files, ok := transformCtx.TransformerConfigVariable("aws.sharedCredentialsFiles"); ok && !core.IsScalarNil(files) {
		opts = append(opts, config.WithSharedCredentialsFiles(
			strings.Split(core.StringValueFromScalar(files), ","),
		))
	}

	if files, ok := transformCtx.TransformerConfigVariable("aws.sharedConfigFiles"); ok && !core.IsScalarNil(files) {
		opts = append(opts, config.WithSharedConfigFiles(
			strings.Split(core.StringValueFromScalar(files), ","),
		))
	}

	if profile, ok := transformCtx.TransformerConfigVariable("aws.profile"); ok && !core.IsScalarNil(profile) {
		opts = append(opts, config.WithSharedConfigProfile(
			core.StringValueFromScalar(profile),
		))
	}

	return opts
}

func sharedEndpointOptions(transformCtx transform.Context) []func(*config.LoadOptions) error {
	opts := []func(*config.LoadOptions) error{}

	if useFIPS, ok := transformCtx.TransformerConfigVariable("aws.useFIPSEndpoint"); ok && !core.IsScalarNil(useFIPS) {
		state := aws.FIPSEndpointStateDisabled
		if core.BoolValueFromScalar(useFIPS) {
			state = aws.FIPSEndpointStateEnabled
		}
		opts = append(opts, config.WithUseFIPSEndpoint(state))
	}

	if useDualStack, ok := transformCtx.TransformerConfigVariable("aws.useDualStackEndpoint"); ok && !core.IsScalarNil(useDualStack) {
		state := aws.DualStackEndpointStateDisabled
		if core.BoolValueFromScalar(useDualStack) {
			state = aws.DualStackEndpointStateEnabled
		}
		opts = append(opts, config.WithUseDualStackEndpoint(state))
	}

	return opts
}

func certOptions(
	transformCtx transform.Context,
	env map[string]string,
) ([]func(*config.LoadOptions) error, error) {
	opts := []func(*config.LoadOptions) error{}

	bundle, ok := configValueFallbackToEnv(
		transformCtx, env, "aws.customCABundle", "AWS_CA_BUNDLE",
	)
	if !ok || core.IsScalarNil(bundle) {
		return opts, nil
	}

	bundleData, err := os.ReadFile(core.StringValueFromScalar(bundle))
	if err != nil {
		return nil, err
	}
	opts = append(opts, config.WithCustomCABundle(bytes.NewReader(bundleData)))
	return opts, nil
}

func httpClientOptions(transformCtx transform.Context) []func(*config.LoadOptions) error {
	insecureScalar, hasInsecure := transformCtx.TransformerConfigVariable("aws.insecure")
	insecure := core.BoolValueFromScalar(insecureScalar)

	httpProxyScalar, hasHTTPProxy := transformCtx.TransformerConfigVariable("aws.httpProxy")
	httpProxy := core.StringValueFromScalar(httpProxyScalar)

	httpsProxyScalar, hasHTTPSProxy := transformCtx.TransformerConfigVariable("aws.httpsProxy")
	httpsProxy := core.StringValueFromScalar(httpsProxyScalar)

	// The AWS SDK already picks up HTTP_PROXY and HTTPS_PROXY env vars on its
	// own, so we only attach a custom client when the transformer config
	// explicitly set one of these — matching the AWS provider's behaviour.
	if !((hasInsecure && insecure) ||
		(hasHTTPProxy && !core.IsScalarNil(httpProxyScalar)) ||
		(hasHTTPSProxy && !core.IsScalarNil(httpsProxyScalar))) {
		return nil
	}

	customClient := awshttp.NewBuildableClient().WithTransportOptions(
		func(t *http.Transport) {
			if insecure {
				t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			}

			finalProxyURL := strings.TrimSpace(httpProxy)
			if finalProxyURL == "" {
				finalProxyURL = strings.TrimSpace(httpsProxy)
			}
			if finalProxyURL == "" {
				return
			}

			parsed, err := url.Parse(finalProxyURL)
			if err != nil {
				// Validation should have caught a malformed URL before reaching
				// here; fall back to the SDK's env-driven proxy resolution
				// rather than panicking inside a transformer plugin.
				return
			}
			t.Proxy = http.ProxyURL(parsed)
		},
	)

	return []func(*config.LoadOptions) error{config.WithHTTPClient(customClient)}
}

func ec2MetadataServiceOptions(
	transformCtx transform.Context,
	env map[string]string,
) []func(*config.LoadOptions) error {
	opts := []func(*config.LoadOptions) error{}

	endpoint, hasEndpoint := configValueFallbackToEnv(
		transformCtx, env,
		"aws.ec2MetadataServiceEndpoint",
		"AWS_EC2_METADATA_SERVICE_ENDPOINT",
	)
	if hasEndpoint && !core.IsScalarNil(endpoint) {
		opts = append(opts, config.WithEC2IMDSEndpoint(
			core.StringValueFromScalar(endpoint),
		))
	}

	mode, hasMode := configValueFallbackToEnv(
		transformCtx, env,
		"aws.ec2MetadataServiceEndpointMode",
		"AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE",
	)
	if hasMode && !core.IsScalarNil(mode) {
		opts = append(opts, config.WithEC2IMDSEndpointMode(
			imdsEndpointModeStateFromString(core.StringValueFromScalar(mode)),
		))
	}

	return opts
}

func imdsEndpointModeStateFromString(s string) imds.EndpointModeState {
	switch strings.ToLower(s) {
	case "ipv4":
		return imds.EndpointModeStateIPv4
	case "ipv6":
		return imds.EndpointModeStateIPv6
	default:
		return imds.EndpointModeStateUnset
	}
}

func assumeRoleOptions(transformCtx transform.Context) []func(*config.LoadOptions) error {
	roleArn, hasRoleArn := transformCtx.TransformerConfigVariable("aws.assumeRole.roleArn")
	if !hasRoleArn || core.IsScalarNil(roleArn) {
		return nil
	}

	externalID, hasExternalID := transformCtx.TransformerConfigVariable("aws.assumeRole.externalId")
	duration, hasDuration := transformCtx.TransformerConfigVariable("aws.assumeRole.duration")
	sessionName, hasSessionName := transformCtx.TransformerConfigVariable("aws.assumeRole.sessionName")

	return []func(*config.LoadOptions) error{
		config.WithAssumeRoleCredentialOptions(func(o *stscreds.AssumeRoleOptions) {
			o.RoleARN = core.StringValueFromScalar(roleArn)

			if hasExternalID && !core.IsScalarNil(externalID) {
				o.ExternalID = aws.String(core.StringValueFromScalar(externalID))
			}

			if hasDuration && !core.IsScalarNil(duration) {
				// Validation enforced by the config schema.
				parsed, _ := time.ParseDuration(core.StringValueFromScalar(duration))
				o.Duration = parsed
			}

			if hasSessionName && !core.IsScalarNil(sessionName) {
				o.RoleSessionName = core.StringValueFromScalar(sessionName)
			}
		}),
	}
}

func assumeRoleWithWebIdentityOptions(transformCtx transform.Context) []func(*config.LoadOptions) error {
	roleArn, hasRoleArn := transformCtx.TransformerConfigVariable("aws.assumeRoleWithWebIdentity.roleArn")
	if !hasRoleArn || core.IsScalarNil(roleArn) {
		return nil
	}

	tokenFile, hasTokenFile := transformCtx.TransformerConfigVariable("aws.assumeRoleWithWebIdentity.webIdentityTokenFile")
	duration, hasDuration := transformCtx.TransformerConfigVariable("aws.assumeRoleWithWebIdentity.duration")
	sessionName, hasSessionName := transformCtx.TransformerConfigVariable("aws.assumeRoleWithWebIdentity.sessionName")

	return []func(*config.LoadOptions) error{
		config.WithWebIdentityRoleCredentialOptions(func(o *stscreds.WebIdentityRoleOptions) {
			o.RoleARN = core.StringValueFromScalar(roleArn)

			if hasTokenFile && !core.IsScalarNil(tokenFile) {
				o.TokenRetriever = stscreds.IdentityTokenFile(
					core.StringValueFromScalar(tokenFile),
				)
			}

			if hasDuration && !core.IsScalarNil(duration) {
				parsed, _ := time.ParseDuration(core.StringValueFromScalar(duration))
				o.Duration = parsed
			}

			if hasSessionName && !core.IsScalarNil(sessionName) {
				o.RoleSessionName = core.StringValueFromScalar(sessionName)
			}
		}),
	}
}

func configValueFallbackToEnv(
	transformCtx transform.Context,
	env map[string]string,
	key string,
	envKey string,
) (*core.ScalarValue, bool) {
	value, ok := transformCtx.TransformerConfigVariable(key)
	if ok && !core.IsScalarNil(value) {
		return value, true
	}

	envValue, ok := env[envKey]
	if ok {
		return core.ScalarFromString(envValue), true
	}

	return nil, false
}
