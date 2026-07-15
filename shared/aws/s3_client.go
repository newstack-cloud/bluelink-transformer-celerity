package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// S3ClientFactory builds an S3 client from an AWS config, a transformer
// context and read-only environment map.
//
// The factory needs the transform.Context as well as the AWS config because
// some S3 client options (`aws.s3.endpoint`, `aws.s3.usePathStyle`) live on
// the transformer config rather than on aws.Config itself.
type S3ClientFactory func(
	ctx context.Context,
	transformCtx transform.Context,
	envMap map[string]string,
	loader ConfigLoader,
) (*s3.Client, error)

// NewS3Client is the high-level factory the handler emitter calls. It loads
// the AWS config from the transformer context (with environment fallback) and
// then constructs an S3 client. Returns an error only if AWS config loading
// fails; an absent transformer config is fine, the SDK falls back to its
// default chain.
func NewS3Client(
	ctx context.Context,
	transformCtx transform.Context,
	env map[string]string,
	loader ConfigLoader,
) (*s3.Client, error) {
	cfg, err := ConfigFromTransformContext(ctx, transformCtx, env, loader)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(
		*cfg,
		s3OptionsFromTransformContext(transformCtx)...,
	), nil
}

func s3OptionsFromTransformContext(transformCtx transform.Context) []func(*s3.Options) {
	opts := []func(*s3.Options){}
	endpoint, ok := transformCtx.TransformerConfigVariable("aws.s3.endpoint")
	if ok && !core.IsScalarNil(endpoint) {
		value := core.StringValueFromScalar(endpoint)
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(value)
		})
	}

	pathStyle, ok := transformCtx.TransformerConfigVariable("aws.s3.usePathStyle")
	if ok && !core.IsScalarNil(pathStyle) {
		value := core.BoolValueFromScalar(pathStyle)
		opts = append(opts, func(o *s3.Options) {
			o.UsePathStyle = value
		})
	}

	return opts
}
