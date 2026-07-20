//go:build unit

// Package pipeline contains full-pipeline tests that drive the bluelink blueprint
// container in-process: .blueprint parse -> abstract validation -> celerity
// transform (registered as a transform.SpecTransformer) -> post-transform
// validation against the real AWS provider's resource schemas -> change staging.
// No AWS calls are made and no credentials are required; every scenario stages a
// new instance and asserts on the resulting change set, it never deploys.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	awsprovider "github.com/newstack-cloud/bluelink-provider-aws/provider"
	cloudcontrolservice "github.com/newstack-cloud/bluelink-provider-aws/services/cloudcontrol/service"
	dynamodbservice "github.com/newstack-cloud/bluelink-provider-aws/services/dynamodb/service"
	ec2service "github.com/newstack-cloud/bluelink-provider-aws/services/ec2/service"
	elasticacheservice "github.com/newstack-cloud/bluelink-provider-aws/services/elasticache/service"
	eventsservice "github.com/newstack-cloud/bluelink-provider-aws/services/events/service"
	iamservice "github.com/newstack-cloud/bluelink-provider-aws/services/iam/service"
	kmsservice "github.com/newstack-cloud/bluelink-provider-aws/services/kms/service"
	lambdaservice "github.com/newstack-cloud/bluelink-provider-aws/services/lambda/service"
	resgrouptagservice "github.com/newstack-cloud/bluelink-provider-aws/services/resgrouptag/service"
	s3service "github.com/newstack-cloud/bluelink-provider-aws/services/s3/service"
	secretsmanagerservice "github.com/newstack-cloud/bluelink-provider-aws/services/secretsmanager/service"
	sqsservice "github.com/newstack-cloud/bluelink-provider-aws/services/sqs/service"
	ssmservice "github.com/newstack-cloud/bluelink-provider-aws/services/ssm/service"
	"github.com/newstack-cloud/bluelink-provider-aws/utils"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	celerity "github.com/newstack-cloud/bluelink-transformer-celerity/transformer"
	"github.com/newstack-cloud/bluelink/libs/blueprint-state/memfile"
	"github.com/newstack-cloud/bluelink/libs/blueprint/changes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/container"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	bperrors "github.com/newstack-cloud/bluelink/libs/blueprint/errors"
	"github.com/newstack-cloud/bluelink/libs/blueprint/includes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subengine"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/providerserverv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/transformerserverv1"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

// Staging runs fully in-process, so anything beyond a minute is a hang,
// not a slow operation.
const channelTimeout = 60 * time.Second

// The region never receives a request in this suite; it only has to be a
// well-formed value for provider config validation.
const testRegion = "eu-west-2"

var instanceCounter atomic.Uint64

// Harness drives the blueprint container in-process with the celerity
// transformer and the real AWS provider registered, mirroring how a
// production plugin host composes them. A single Loader.Load performs the
// whole pipeline (parse -> abstract validation -> link graph -> transform ->
// concrete validation) as of blueprint v0.51.1, which fixed the loader
// discarding transformed blueprints and added its own transformer-provider
// adapter for abstract namespace resolution.
type Harness struct {
	T             *testing.T
	Ctx           context.Context
	Loader        container.Loader
	TransformName string
}

// StageResult carries both the transformer's output schema (for
// must-emit/must-not-emit assertions) and the container's planned change set
// for a new instance.
type StageResult struct {
	// Transformed is the blueprint produced by the celerity transformer,
	// before concrete validation and staging.
	Transformed *schema.Blueprint
	// Changes is the change set staged from the transformed blueprint.
	Changes *changes.BlueprintChanges
}

// Setup builds a fresh harness with isolated in-memory state.
func Setup(t *testing.T) *Harness {
	t.Helper()
	ctx := context.Background()

	configStore := utils.NewAWSConfigStore(
		os.Environ(),
		utils.AWSConfigFromProviderContext,
		&utils.DefaultAWSConfigLoader{},
		utils.AWSConfigCacheKey,
	)
	prov := awsprovider.NewProvider(
		iamservice.NewService,
		lambdaservice.NewService,
		ec2service.NewService,
		resgrouptagservice.NewService,
		sqsservice.NewService,
		dynamodbservice.NewService,
		eventsservice.NewService,
		cloudcontrolservice.NewService,
		s3service.NewService,
		ssmservice.NewService,
		kmsservice.NewService,
		elasticacheservice.NewService,
		secretsmanagerservice.NewService,
		configStore,
	)
	// Mirrors the production plugin host: derive each resource's CanLinkTo from
	// the registered link types so link selectors activate. Without this wrap,
	// links silently never form.
	allLinkTypes, err := prov.ListLinkTypes(ctx)
	require.NoError(t, err, "list provider link types")
	wrappedProv := providerserverv1.WrapProviderWithDerivedCanLinkTo(prov, allLinkTypes)

	transformer := transformerserverv1.WrapTransformerWithDerivedCanLinkTo(
		celerity.NewTransformer(&shared.Dependencies{
			BuildManifestLoader: build.NewManifestLoader(
				build.WithDefaultResourceLoader(build.NewFSResourceLoader(afero.NewOsFs())),
			),
		}),
	)
	transformName, err := transformer.GetTransformName(ctx)
	require.NoError(t, err, "get transform name")

	stateContainer, err := memfile.LoadStateContainer(
		t.TempDir(), afero.NewMemMapFs(), core.NewNopLogger(),
	)
	require.NoError(t, err, "load in-memory state container")

	loader := container.NewDefaultLoader(
		map[string]provider.Provider{"aws": wrappedProv},
		map[string]transform.SpecTransformer{transformName: transformer},
		stateContainer,
		&noChildResolver{},
		container.WithLoaderTransformSpec(true),
		container.WithLoaderValidateAfterTransform(true),
		container.WithLoaderLogger(core.NewNopLogger()),
	)

	return &Harness{
		T:             t,
		Ctx:           ctx,
		Loader:        loader,
		TransformName: transformName,
	}
}

// Params builds blueprint params the way the deploy engine would: AWS provider
// config, plus celerity context variables selecting the aws-serverless target
// and pointing the transformer at a build manifest. An empty manifestPath means
// "before celerity build" (the manifest-less fallback path).
func (h *Harness) Params(manifestPath string, vars map[string]*core.ScalarValue) core.BlueprintParams {
	contextVars := map[string]*core.ScalarValue{
		"deployTarget":              core.ScalarFromString(shared.AWSServerless),
		shared.AppNameContextVarKey: core.ScalarFromString("pipeline-test-app"),
	}
	if manifestPath != "" {
		contextVars[shared.BuildManifestContextVarKey] = core.ScalarFromString(manifestPath)
	}

	blueprintVars := map[string]*core.ScalarValue{}
	maps.Copy(blueprintVars, vars)

	return core.NewDefaultParams(
		map[string]map[string]*core.ScalarValue{
			"aws": {"region": core.ScalarFromString(testRegion)},
		},
		map[string]map[string]*core.ScalarValue{
			h.TransformName: {},
		},
		contextVars,
		blueprintVars,
	)
}

// Stage runs the given testdata blueprint through the full pipeline with a
// real build manifest and stages a new instance.
func (h *Harness) Stage(t *testing.T, blueprintFile string) *StageResult {
	t.Helper()
	return h.StageWithParams(t, blueprintFile, h.Params(ManifestPath(), nil))
}

// StageWithParams is Stage with caller-controlled params.
func (h *Harness) StageWithParams(
	t *testing.T,
	blueprintFile string,
	params core.BlueprintParams,
) *StageResult {
	t.Helper()
	bp, err := h.Loader.Load(h.Ctx, filepath.Join("testdata", blueprintFile), params)
	require.NoError(t, err, "load blueprint %q through the container:\n%s",
		blueprintFile, FormatLoadError(err))
	transformed := bp.BlueprintSpec().Schema()
	require.NotNil(t, transformed, "transformed schema for %q", blueprintFile)

	instanceName := fmt.Sprintf("pipeline-instance-%d", instanceCounter.Add(1))
	channels := newChangeStagingChannels()
	err = bp.StageChanges(
		h.Ctx,
		&container.StageChangesInput{InstanceName: instanceName},
		channels,
		params,
	)
	require.NoError(t, err, "start change staging for %q", blueprintFile)
	return &StageResult{
		Transformed: transformed,
		Changes:     consumeStage(t, channels),
	}
}

// Transform runs the pipeline through validation + transform without staging,
// returning the transformed schema. The loader applies registered transforms
// during Validate, so the returned schema holds the concrete resources.
// It fails the test on any validation or transform error.
func (h *Harness) Transform(
	t *testing.T,
	blueprintFile string,
	params core.BlueprintParams,
) *schema.Blueprint {
	t.Helper()
	validationResult, err := h.Loader.Validate(
		h.Ctx, filepath.Join("testdata", blueprintFile), params,
	)
	require.NoError(t, err, "validation + transform of %q:\n%s",
		blueprintFile, FormatLoadError(err))
	require.NotNil(t, validationResult.Schema, "transformed schema for %q", blueprintFile)
	return validationResult.Schema
}

// Validate runs container-level validation only (no blueprint container is
// built), returning the validation result and error for negative assertions.
func (h *Harness) Validate(
	t *testing.T,
	blueprintFile string,
	params core.BlueprintParams,
) (*container.ValidationResult, error) {
	t.Helper()
	return h.Loader.Validate(h.Ctx, filepath.Join("testdata", blueprintFile), params)
}

// ManifestPath points at the static Tier 1 build manifest fixture. Artifact
// S3 coordinates in it are never dereferenced during staging.
func ManifestPath() string {
	return filepath.Join("testdata", "build-manifest.json")
}

func newChangeStagingChannels() *container.ChangeStagingChannels {
	return &container.ChangeStagingChannels{
		ResourceChangesChan: make(chan container.ResourceChangesMessage),
		ChildChangesChan:    make(chan container.ChildChangesMessage),
		LinkChangesChan:     make(chan container.LinkChangesMessage),
		CompleteChan:        make(chan changes.BlueprintChanges),
		ErrChan:             make(chan error),
	}
}

func consumeStage(t *testing.T, channels *container.ChangeStagingChannels) *changes.BlueprintChanges {
	t.Helper()
	for {
		select {
		case <-channels.ResourceChangesChan:
		case <-channels.ChildChangesChan:
		case <-channels.LinkChangesChan:
		case complete := <-channels.CompleteChan:
			return &complete
		case err := <-channels.ErrChan:
			require.NoError(t, err, "change staging failed")
		case <-time.After(channelTimeout):
			t.Fatal("timed out waiting for change staging to complete")
		}
	}
}

// FormatLoadError flattens a blueprint LoadError tree into an indented
// multi-line string so failures show the real leaf validation errors instead
// of "N child errors".
func FormatLoadError(err error) string {
	if err == nil {
		return ""
	}
	var b strings.Builder
	formatLoadErrorInto(&b, err, 0)
	return b.String()
}

func formatLoadErrorInto(b *strings.Builder, err error, depth int) {
	indent := strings.Repeat("  ", depth)
	loadErr := &bperrors.LoadError{}
	if errors.As(err, &loadErr) {
		fmt.Fprintf(b, "%s- [%s] %s\n", indent, loadErr.ReasonCode, loadErr.Err)
		for _, child := range loadErr.ChildErrors {
			formatLoadErrorInto(b, child, depth+1)
		}
		return
	}
	fmt.Fprintf(b, "%s- %s\n", indent, err)
}

// noChildResolver rejects child includes; pipeline fixtures never use them.
type noChildResolver struct{}

func (r *noChildResolver) Resolve(
	_ context.Context,
	includeName string,
	_ *subengine.ResolvedInclude,
	_ core.BlueprintParams,
) (*includes.ChildBlueprintInfo, error) {
	return nil, fmt.Errorf("child includes are not supported in pipeline tests: %s", includeName)
}
