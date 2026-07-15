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
			// No build manifest path available in context, which is
			// possible in validation contexts.
			// Skip loading the build manifest in this case.
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

		// Make the loaded build manifest available in the run so it is available
		// at key stages of the transform pipeline.
		// The key to access the manifest with transformutils.Use will be the *build.Manifest type
		// as the type parameter.
		transformutils.Provide(run, manifest)
		return nil
	}
}
