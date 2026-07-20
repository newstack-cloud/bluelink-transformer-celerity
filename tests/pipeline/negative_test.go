//go:build unit

package pipeline

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/newstack-cloud/bluelink/libs/blueprint/container"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/stretchr/testify/require"
)

// A handler spec missing the required handlerName field must be rejected by
// abstract validation through the container registry, and the error must name
// the missing field.
func TestPipelinePartialAbstractSpecFailsValidation(t *testing.T) {
	h := Setup(t)
	_, err := h.Validate(t, "partial_handler.blueprint", h.Params(ManifestPath(), nil))
	require.Error(t, err, "expected abstract validation to reject the partial handler spec")
	require.Contains(t, FormatLoadError(err), "handlerName",
		"the validation error must name the missing required field:\n%s", FormatLoadError(err))
}

// The pre-build dry-run path: a validation-context transform with NO build
// manifest must succeed and emit lambda functions with the documented
// placeholder code location. The aws.region transformer config is still
// supplied (the deploy engine forwards deploy config in validation runs too)
// so the fixture's celerity/vpc emits a complete spec.
func TestPipelineValidationContextTransform(t *testing.T) {
	h := Setup(t)
	params := pipelineParams(h, "", awsRegionTransformerConfig(), map[string]*core.ScalarValue{
		core.ValidationContextVariableName: core.ScalarFromBool(true),
	})
	transformed := h.Transform(t, "validation_context.blueprint", params)

	require.Equal(t, "placeholder-bucket",
		core.StringValue(specNode(t, transformed, "valHandler_lambda_func", "$.code.s3Bucket")))
	require.Equal(t, "placeholder.handler",
		core.StringValue(specNode(t, transformed, "valHandler_lambda_func", "$.handler")))

	err := tryStage(h, transformed, params)
	require.NoError(t, err,
		"placeholder lambda specs are expected to pass concrete validation and staging:\n%s",
		FormatLoadError(err))
}

// The manifest-fallback contract (docs/contract/index.md section 1.4): a
// NON-validation transform pointing celerity.buildManifest at a nonexistent
// path must not fail; the handler emit records a warning and emits the lambda
// with the same stageable placeholder code/handler values the
// validation-context path uses (see loadCodeLocationInfo in
// resources/handler/handler_aws_serverless_emit.go). The placeholders pass
// concrete validation and change staging, a "dry run before build" can plan,
// and fail at deploy time by design.
func TestPipelineMissingManifestFallback(t *testing.T) {
	h := Setup(t)
	params := pipelineParams(
		h, filepath.Join("testdata", "nonexistent-build-manifest.json"), nil, nil,
	)
	transformed := h.Transform(t, "fallback_handler.blueprint", params)

	require.Equal(t, "placeholder-bucket",
		core.StringValue(specNode(t, transformed, "fallbackHandler_lambda_func", "$.code.s3Bucket")))
	require.Equal(t, "placeholder-key",
		core.StringValue(specNode(t, transformed, "fallbackHandler_lambda_func", "$.code.s3Key")))
	require.Equal(t, "placeholder.handler",
		core.StringValue(specNode(t, transformed, "fallbackHandler_lambda_func", "$.handler")))

	err := tryStage(h, transformed, params)
	require.NoError(t, err,
		"manifest-less placeholder lambda specs must pass concrete validation and staging:\n%s",
		FormatLoadError(err))
}

// Runs concrete validation + change staging like StageWithParams but
// returns the error instead of failing the test, so negative tests can pin
// observed behaviour either way.
func tryStage(
	h *Harness,
	transformed *schema.Blueprint,
	params core.BlueprintParams,
) error {
	bp, err := h.Loader.LoadFromSchema(h.Ctx, transformed, params)
	if err != nil {
		return err
	}

	instanceName := fmt.Sprintf("pipeline-negative-instance-%d", instanceCounter.Add(1))
	channels := newChangeStagingChannels()
	err = bp.StageChanges(
		h.Ctx,
		&container.StageChangesInput{InstanceName: instanceName},
		channels,
		params,
	)
	if err != nil {
		return err
	}
	for {
		select {
		case <-channels.ResourceChangesChan:
		case <-channels.ChildChangesChan:
		case <-channels.LinkChangesChan:
		case <-channels.CompleteChan:
			return nil
		case err := <-channels.ErrChan:
			return err
		case <-time.After(channelTimeout):
			return fmt.Errorf("timed out waiting for change staging to complete")
		}
	}
}
