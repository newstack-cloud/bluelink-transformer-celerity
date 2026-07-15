//go:build unit

package build

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func Test_FSResourceLoader_loads_existing_file(t *testing.T) {
	tmpDir := t.TempDir()
	contents := []byte(`{"version":"1","runtime":"nodejs20.x"}`)
	path := filepath.Join(tmpDir, "manifest.json")
	require.NoError(t, os.WriteFile(path, contents, 0o600))

	loader := NewFSResourceLoader(afero.NewOsFs())
	reader, err := loader.Load(context.Background(), path, newFakeTransformContext(nil, nil))
	require.NoError(t, err)
	defer reader.Close()

	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, contents, got)
}

func Test_FSResourceLoader_returns_error_for_missing_file(t *testing.T) {
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "does-not-exist.json")

	loader := NewFSResourceLoader(afero.NewOsFs())
	_, err := loader.Load(context.Background(), missing, newFakeTransformContext(nil, nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to open file")
	require.Contains(t, err.Error(), missing)
}

func Test_FSResourceLoader_closes_returned_reader(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "manifest.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	loader := NewFSResourceLoader(afero.NewOsFs())
	reader, err := loader.Load(context.Background(), path, newFakeTransformContext(nil, nil))
	require.NoError(t, err)

	require.NoError(t, reader.Close())
	// A second Close on an *os.File returns an error rather than panicking;
	// asserting that documents the idempotency contract the hub relies on.
	require.Error(t, reader.Close())
}
