package build

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	awsshared "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

type s3ResourceLoader struct {
	clientFactory awsshared.S3ClientFactory
	envMap        map[string]string
	configLoader  awsshared.ConfigLoader
}

// NewS3ResourceLoader creates a new resource loader
// that can load files such as a build manifest from s3.
func NewS3ResourceLoader(
	clientFactory awsshared.S3ClientFactory,
	envMap map[string]string,
	configLoader awsshared.ConfigLoader,
) ResourceLoader {
	return &s3ResourceLoader{
		clientFactory: clientFactory,
		envMap:        envMap,
		configLoader:  configLoader,
	}
}

func (l *s3ResourceLoader) Load(
	ctx context.Context,
	path string,
	transformCtx transform.Context,
) (io.ReadCloser, error) {
	client, err := l.clientFactory(
		ctx,
		transformCtx,
		l.envMap,
		l.configLoader,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to build S3 client: %w",
			err,
		)
	}

	objectInfo := parseS3Path(path)
	if objectInfo == nil {
		return nil, fmt.Errorf("invalid S3 path: %s", path)
	}

	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &objectInfo.bucket,
		Key:    &objectInfo.key,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get S3 object %q: %w", path, err)
	}

	return output.Body, nil
}

type s3ObjectInfo struct {
	bucket string
	key    string
}

func parseS3Path(path string) *s3ObjectInfo {
	// Example path: my-bucket/path/to/object
	// Scheme is expected to have been stripped by the caller
	// (e.g. "s3://my-bucket/path/to/object" -> "my-bucket/path/to/object").
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return nil
	}

	return &s3ObjectInfo{
		bucket: parts[0],
		key:    parts[1],
	}
}
