package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// ResourceLoader defines the interface for loading resources needed during the build process,
// such as build manifests, configuration files, or other artifacts. The loader abstracts away
// the underlying storage mechanism, allowing resources to be loaded from various sources
// (e.g., local file system, S3, Azure Blob Storage) without changing the build logic.
type ResourceLoader interface {
	Load(
		ctx context.Context,
		path string,
		transformCtx transform.Context,
	) (io.ReadCloser, error)
}

// ManifestLoader defines the interface for loading a build manifest, which contains
// metadata about the build process, such as the runtime, dependencies, and other relevant information
// needed specifically for deploying application artifacts such as serverless functions
// or docker images.
type ManifestLoader interface {
	Load(
		ctx context.Context,
		path string,
		transformCtx transform.Context,
	) (*Manifest, error)
}

// ManifestLoaderOption defines a functional option for configuring the manifest loader.
type ManifestLoaderOption func(mainLoader *manifestLoaderHub)

// WithSchemeResourceLoader registers a resource loader for a specific scheme (e.g., "s3", "azureblob").
func WithSchemeResourceLoader(scheme string, loader ResourceLoader) ManifestLoaderOption {
	return func(loaderHub *manifestLoaderHub) {
		loaderHub.resourceLoaders[scheme] = loader
	}
}

// WithDefaultResourceLoader sets a default resource loader
// that will be used when no specific scheme is provided.
// This will typically be set to a local file system loader.
//
// This is not to be confused with a fallback loader for unsupported schemes,
// this will only be called when the path provided to
// Load does not contain a scheme (e.g. "s3://").
func WithDefaultResourceLoader(loader ResourceLoader) ManifestLoaderOption {
	return func(mainLoader *manifestLoaderHub) {
		mainLoader.defaultResourceLoader = loader
	}
}

// NewManifestLoader creates a new manifest loader with the provided options.
func NewManifestLoader(opts ...ManifestLoaderOption) ManifestLoader {
	loader := &manifestLoaderHub{
		resourceLoaders: make(map[string]ResourceLoader),
	}

	for _, opt := range opts {
		opt(loader)
	}

	return loader
}

type manifestLoaderHub struct {
	resourceLoaders       map[string]ResourceLoader
	defaultResourceLoader ResourceLoader
}

func (m *manifestLoaderHub) Load(
	ctx context.Context,
	path string,
	transformCtx transform.Context,
) (*Manifest, error) {
	schemeInfo := extractScheme(path)
	if schemeInfo == nil {
		if m.defaultResourceLoader != nil {
			return m.loadManifest(ctx, path, transformCtx, m.defaultResourceLoader)
		}
		return nil, fmt.Errorf("no manifest loader found for path: %s", path)
	}

	if loader, ok := m.resourceLoaders[schemeInfo.scheme]; ok {
		return m.loadManifest(ctx, schemeInfo.path, transformCtx, loader)
	}

	return nil, fmt.Errorf("no manifest loader found for scheme: %s", schemeInfo.scheme)
}

func (m *manifestLoaderHub) loadManifest(
	ctx context.Context,
	path string,
	transformCtx transform.Context,
	loader ResourceLoader,
) (*Manifest, error) {
	reader, err := loader.Load(ctx, path, transformCtx)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var manifest Manifest
	if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

type schemeInfo struct {
	scheme string
	path   string
}

func extractScheme(path string) *schemeInfo {
	schemeSepIndex := strings.Index(path, "://")
	if schemeSepIndex <= 0 {
		return nil
	}

	return &schemeInfo{
		scheme: path[:schemeSepIndex],
		path:   path[schemeSepIndex+3:],
	}
}
