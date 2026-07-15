//go:build integration

package build

import (
	"context"
	"io"
	"os"
	"testing"

	awsshared "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/stretchr/testify/suite"
)

const (
	localstackEndpoint = "http://localhost:4579"
	testRegion         = "eu-west-2"
	testBucket         = "test-bucket"
	validManifestKey   = "valid.manifest.json"
	invalidManifestKey = "invalid.manifest.json"
)

type S3ResourceLoaderSuite struct {
	loader       ResourceLoader
	transformCtx *fakeTransformContext
	expectedBody []byte
	suite.Suite
}

func (s *S3ResourceLoaderSuite) SetupSuite() {
	body, err := os.ReadFile("__testdata/s3/data/test-bucket/" + validManifestKey)
	s.Require().NoError(err)
	s.expectedBody = body

	s.transformCtx = newFakeTransformContext(
		map[string]string{
			"aws.s3.endpoint": localstackEndpoint,
			"aws.region":      testRegion,
		},
		map[string]bool{
			"aws.s3.usePathStyle": true,
		},
	)

	// Empty envMap is fine for these tests: the loader only consults envMap
	// for AWS_RETRY_MODE, AWS_CA_BUNDLE, and EC2 IMDS fallbacks (see
	// shared/aws/config_loader.go), none of which apply to LocalStack.
	// Credentials come from AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY in the
	// process env via the SDK's default chain, these are set by scripts/run-tests.sh.
	s.loader = NewS3ResourceLoader(
		awsshared.NewS3Client,
		map[string]string{},
		nil,
	)
}

func (s *S3ResourceLoaderSuite) Test_loads_object_from_s3() {
	reader, err := s.loader.Load(
		context.Background(),
		testBucket+"/"+validManifestKey,
		s.transformCtx,
	)
	s.Require().NoError(err)
	defer reader.Close()

	got, err := io.ReadAll(reader)
	s.Require().NoError(err)
	s.Assert().Equal(s.expectedBody, got)
}

func (s *S3ResourceLoaderSuite) Test_returns_error_for_missing_object() {
	_, err := s.loader.Load(
		context.Background(),
		testBucket+"/does-not-exist.json",
		s.transformCtx,
	)
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "failed to get S3 object")
}

func (s *S3ResourceLoaderSuite) Test_returns_error_for_path_without_separator() {
	_, err := s.loader.Load(
		context.Background(),
		"no-slash-here",
		s.transformCtx,
	)
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "invalid S3 path")
}

func (s *S3ResourceLoaderSuite) Test_honours_endpoint_from_transform_context() {
	// Negative control: point the loader at a port nothing is listening on
	// and assert the error mentions that target. If the loader were ignoring
	// aws.s3.endpoint and falling back to the real AWS endpoint, the error
	// would mention "s3.amazonaws.com" (or fail with auth/signature errors)
	// rather than the misrouted localhost:1 dial target.
	misroutedCtx := newFakeTransformContext(
		map[string]string{
			"aws.s3.endpoint": "http://localhost:1",
			"aws.region":      testRegion,
		},
		map[string]bool{
			"aws.s3.usePathStyle": true,
		},
	)

	_, err := s.loader.Load(
		context.Background(),
		testBucket+"/"+validManifestKey,
		misroutedCtx,
	)
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "failed to get S3 object")
	// AWS SDK wraps the underlying dial failure with the target URL; this
	// substring is only possible if our configured endpoint was actually
	// consulted by the S3 client.
	s.Assert().Contains(err.Error(), "localhost:1",
		"error must reference the misrouted endpoint, proving aws.s3.endpoint was honoured")
	s.Assert().Contains(err.Error(), "connection refused",
		"error must indicate a dial failure, not an AWS-side rejection")
}

func TestS3ResourceLoaderSuite(t *testing.T) {
	suite.Run(t, new(S3ResourceLoaderSuite))
}
