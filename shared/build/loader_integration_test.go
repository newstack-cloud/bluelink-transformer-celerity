//go:build integration

package build

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	awsshared "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/suite"
)

type ManifestLoaderEndToEndSuite struct {
	hub          ManifestLoader
	transformCtx *fakeTransformContext
	expected     *Manifest
	suite.Suite
}

func (s *ManifestLoaderEndToEndSuite) SetupSuite() {
	body, err := os.ReadFile("__testdata/s3/data/test-bucket/" + validManifestKey)
	s.Require().NoError(err)
	s.expected = &Manifest{}
	s.Require().NoError(json.Unmarshal(body, s.expected))

	s.transformCtx = newFakeTransformContext(
		map[string]string{
			"aws.s3.endpoint": localstackEndpoint,
			"aws.region":      testRegion,
		},
		map[string]bool{
			"aws.s3.usePathStyle": true,
		},
	)

	s.hub = NewManifestLoader(
		WithDefaultResourceLoader(NewFSResourceLoader(afero.NewOsFs())),
		WithSchemeResourceLoader("s3", NewS3ResourceLoader(
			awsshared.NewS3Client,
			map[string]string{},
			nil,
		)),
	)
}

func (s *ManifestLoaderEndToEndSuite) Test_loads_manifest_from_s3_through_hub() {
	manifest, err := s.hub.Load(
		context.Background(),
		"s3://"+testBucket+"/"+validManifestKey,
		s.transformCtx,
	)
	s.Require().NoError(err)
	s.Assert().Equal(s.expected.Version, manifest.Version)
	s.Assert().Equal(s.expected.Runtime, manifest.Runtime)
	s.Assert().Equal(s.expected.Target, manifest.Target)
	s.Require().NotNil(manifest.Lambda)
	s.Assert().Equal(s.expected.Lambda.EntryPoint, manifest.Lambda.EntryPoint)
	s.Require().NotNil(manifest.Lambda.AppCode)
	s.Assert().Equal(s.expected.Lambda.AppCode.ContentHash, manifest.Lambda.AppCode.ContentHash)
}

func (s *ManifestLoaderEndToEndSuite) Test_loads_manifest_from_local_filesystem_through_hub() {
	tmpDir := s.T().TempDir()
	path := filepath.Join(tmpDir, "manifest.json")
	body, err := os.ReadFile("__testdata/s3/data/test-bucket/" + validManifestKey)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, body, 0o600))

	manifest, err := s.hub.Load(context.Background(), path, s.transformCtx)
	s.Require().NoError(err)
	s.Assert().Equal(s.expected.Version, manifest.Version)
	s.Assert().Equal(s.expected.Runtime, manifest.Runtime)
	s.Assert().Equal(s.expected.Target, manifest.Target)
}

func (s *ManifestLoaderEndToEndSuite) Test_returns_decode_error_for_malformed_s3_object() {
	_, err := s.hub.Load(
		context.Background(),
		"s3://"+testBucket+"/"+invalidManifestKey,
		s.transformCtx,
	)
	s.Require().Error(err)
	// json.Decoder reports unexpected EOF for the truncated fixture.
	s.Assert().Contains(err.Error(), "EOF")
}

func TestManifestLoaderEndToEndSuite(t *testing.T) {
	suite.Run(t, new(ManifestLoaderEndToEndSuite))
}
