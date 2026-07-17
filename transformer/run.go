package transformer

import (
	"context"

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
			// The manifest path is set but the manifest cannot be obtained (missing or
			// unreadable file, failed remote fetch). Per the build-manifest fallback
			// contract, this is not fatal: the transform continues without the manifest
			// so the handler emit produces syntactically valid resources without the
			// code-asset/entry-point references and surfaces a per-handler warning
			// (see loadCodeLocationInfo). This lets validation and dry-run/plan run
			// before "celerity build" has produced a manifest. OnRun has no diagnostic
			// channel, so the warning is carried by the downstream emit.
			return nil
		}

		// Provide the manifest to the run; transformutils.Use retrieves it later
		// keyed by the *build.Manifest type.
		transformutils.Provide(run, manifest)
		return nil
	}
}
