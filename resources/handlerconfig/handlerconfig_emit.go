package handlerconfig

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// This is deliberately a no-op.
// celerity/handlerConfig is holds contributory metadata,
// the handler inherits its fields during resolve
// (see resources/handler resolveInheritedSpec).
// Deliberately not target-named, unlike the emits that build resources: a handler config
// contributes to other resources and emits none of its own on any target, so
// every target can register this same function.
func emitHandlerConfig(
	_ context.Context,
	_ *transformutils.Run,
	_ *ResolvedHandlerConfig,
	_ transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	return &transformutils.EmitResult{}, nil
}
