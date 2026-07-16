package transformer

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createRunHook(deps *shared.Dependencies) func(ctx context.Context, run *transformutils.Run) error {
	return func(ctx context.Context, run *transformutils.Run) error {
		path, hasPath := run.TransformContext.ContextVariable(
			shared.BuildManifestContextVarKey,
		)
		if !hasPath {
			// No build manifest path in context (possible in validation
			// contexts), so skip loading it.
			return nil
		}

		manifest, err := deps.BuildManifestLoader.Load(
			ctx,
			core.StringValueFromScalar(path),
			run.TransformContext,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to load build manifest from path %q: %w",
				core.StringValueFromScalar(path),
				err,
			)
		}

		// Provide the manifest to the run; transformutils.Use retrieves it later
		// keyed by the *build.Manifest type.
		transformutils.Provide(run, manifest)
		return nil
	}
}
