//go:build unit

package build

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/require"
)

type countingReadCloser struct {
	io.Reader
	closes int
}

func (c *countingReadCloser) Close() error {
	c.closes++
	return nil
}

type fakeResourceLoader struct {
	body     string
	err      error
	pathSeen string
	last     *countingReadCloser
}

func (f *fakeResourceLoader) Load(
	_ context.Context,
	path string,
	_ transform.Context,
) (io.ReadCloser, error) {
	f.pathSeen = path
	if f.err != nil {
		return nil, f.err
	}
	f.last = &countingReadCloser{Reader: strings.NewReader(f.body)}
	return f.last, nil
}

const validManifestJSON = `{
  "version": "1",
  "runtime": "nodejs20.x",
  "target": "aws-serverless",
  "handlers": {"api.handler": {"lambda": {}}},
  "lambda": {"entryPoint": "__celerity_lambda_entry__.handler"}
}`

func Test_ManifestLoader_dispatches_to_scheme_loader_and_strips_scheme(t *testing.T) {
	schemeLoader := &fakeResourceLoader{body: validManifestJSON}
	hub := NewManifestLoader(
		WithSchemeResourceLoader("test", schemeLoader),
	)

	manifest, err := hub.Load(
		context.Background(),
		"test://some-bucket/manifest.json",
		newFakeTransformContext(nil, nil),
	)
	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.Equal(t, "1", manifest.Version)
	require.Equal(t, "nodejs20.x", manifest.Runtime)
	require.Equal(t, "aws-serverless", manifest.Target)
	require.Equal(t, "some-bucket/manifest.json", schemeLoader.pathSeen,
		"hub should strip scheme prefix before delegating")
}

func Test_ManifestLoader_dispatches_to_default_loader_for_pathless_path(t *testing.T) {
	defaultLoader := &fakeResourceLoader{body: validManifestJSON}
	hub := NewManifestLoader(
		WithDefaultResourceLoader(defaultLoader),
	)

	manifest, err := hub.Load(
		context.Background(),
		"manifest.json",
		newFakeTransformContext(nil, nil),
	)
	require.NoError(t, err)
	require.Equal(t, "1", manifest.Version)
	require.Equal(t, "manifest.json", defaultLoader.pathSeen,
		"default loader should receive the raw path (no scheme to strip)")
}

func Test_ManifestLoader_returns_error_for_unknown_scheme(t *testing.T) {
	hub := NewManifestLoader(
		WithSchemeResourceLoader("s3", &fakeResourceLoader{}),
	)

	_, err := hub.Load(
		context.Background(),
		"gs://foo/bar",
		newFakeTransformContext(nil, nil),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no manifest loader found for scheme: gs")
}

func Test_ManifestLoader_returns_error_for_pathless_path_when_no_default(t *testing.T) {
	hub := NewManifestLoader(
		WithSchemeResourceLoader("s3", &fakeResourceLoader{}),
	)

	_, err := hub.Load(
		context.Background(),
		"manifest.json",
		newFakeTransformContext(nil, nil),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no manifest loader found for path: manifest.json")
}

func Test_ManifestLoader_closes_reader_on_success(t *testing.T) {
	loader := &fakeResourceLoader{body: validManifestJSON}
	hub := NewManifestLoader(WithDefaultResourceLoader(loader))

	_, err := hub.Load(context.Background(), "manifest.json", newFakeTransformContext(nil, nil))
	require.NoError(t, err)
	require.NotNil(t, loader.last)
	require.Equal(t, 1, loader.last.closes,
		"hub must Close the resource reader exactly once on the success path")
}

func Test_ManifestLoader_closes_reader_on_decode_error(t *testing.T) {
	loader := &fakeResourceLoader{body: `{"version": "1",`} // malformed
	hub := NewManifestLoader(WithDefaultResourceLoader(loader))

	_, err := hub.Load(context.Background(), "manifest.json", newFakeTransformContext(nil, nil))
	require.Error(t, err)
	require.NotNil(t, loader.last)
	require.Equal(t, 1, loader.last.closes,
		"hub must Close the resource reader even when JSON decoding fails")
}

func Test_ManifestLoader_propagates_resource_loader_error(t *testing.T) {
	sentinel := errors.New("backend unavailable")
	loader := &fakeResourceLoader{err: sentinel}
	hub := NewManifestLoader(WithDefaultResourceLoader(loader))

	_, err := hub.Load(context.Background(), "manifest.json", newFakeTransformContext(nil, nil))
	require.ErrorIs(t, err, sentinel)
}
