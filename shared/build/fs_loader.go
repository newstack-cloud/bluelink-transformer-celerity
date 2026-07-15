package build

import (
	"context"
	"fmt"
	"io"

	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/spf13/afero"
)

type fsLoader struct {
	fs afero.Fs
}

// NewFSResourceLoader creates a new resource loader
// that can load files from the local filesystem.
func NewFSResourceLoader(fs afero.Fs) ResourceLoader {
	return &fsLoader{
		fs: fs,
	}
}

func (l *fsLoader) Load(
	ctx context.Context,
	path string,
	transformCtx transform.Context,
) (io.ReadCloser, error) {
	file, err := l.fs.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %q: %w", path, err)
	}

	return file, nil
}
