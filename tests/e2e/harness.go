//go:build e2e

// Package e2e contains Tier 2 end-to-end tests that deploy real blueprints
// (authored as .blueprint files under testdata/) against a real AWS account by
// driving the bluelink blueprint container in-process with the celerity
// transformer and the AWS provider registered together. Each test pre-stages
// build artifacts in S3, runs the two-phase load (abstract validation ->
// celerity transform -> concrete load), stages changes, deploys, asserts on
// real AWS side effects, then destroys the instance via t.Cleanup. Build-tagged
// `e2e`; skips when AWS_REGION is not set.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
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
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/blueprint/state"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/providerserverv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/transformerserverv1"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var idCounter atomic.Uint64

// Controls how many blueprint instances are deployed or destroyed against AWS
// concurrently. Subtests run in parallel (t.Parallel), so without a cap they
// would fire enough simultaneous Cloud Control operations to hit account or
// region concurrency limits. The gate enforces the bound regardless of how
// `go test` is invoked (i.e. independent of -parallel); override the size with
// E2E_CONCURRENCY (default 6).
var e2eOpGate = make(chan struct{}, e2eConcurrency())

func e2eConcurrency() int {
	if v := os.Getenv("E2E_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 6
}

// Blocks until a concurrency slot is free and returns a release func
// (call via defer) to free it.
func acquireE2ESlot() func() {
	e2eOpGate <- struct{}{}
	return func() { <-e2eOpGate }
}

// Harness drives in-process blueprint deployments against a real AWS account
// with the celerity transformer in the loop. A single Loader.Load performs the
// whole pipeline (parse -> abstract validation -> link graph -> transform ->
// concrete validation).
type Harness struct {
	T             *testing.T
	Ctx           context.Context
	Loader        container.Loader
	State         state.Container
	AWSConfig     aws.Config
	Region        string
	TransformName string
	// NamePrefix is a run-unique prefix injected into fixtures as
	// ${variables.namePrefix} so parallel runs never collide on
	// user-visible AWS resource names.
	NamePrefix string
	// AppName is the run-unique celerity application name; the internal
	// resources store writes SSM parameters under /celerity/<AppName>/.
	AppName string

	sessionID string

	teardownMu sync.Mutex
	teardowns  map[string]*instanceTeardown
}

// Setup builds the AWS provider, the celerity transformer, an in-memory state
// container and a blueprint loader wired together in-process. It skips the
// test when AWS_REGION is not set.
func Setup(t *testing.T) *Harness {
	t.Helper()

	region := os.Getenv("AWS_REGION")
	if region == "" {
		t.Skip("AWS_REGION not set; skipping e2e test")
	}

	ctx := context.Background()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	require.NoError(t, err, "load AWS SDK config")

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
		// Warn level catches the engine's "unknown error" logs — the only
		// place a link-update error that is not wrapped in a typed provider
		// error is ever surfaced (finish messages just name the element).
		container.WithLoaderLogger(warnLogger()),
	)

	suffix := uniqueSuffix()
	registerSweepScope("celerity-e2e-" + suffix)
	return &Harness{
		T:             t,
		Ctx:           ctx,
		Loader:        loader,
		State:         stateContainer,
		AWSConfig:     awsCfg,
		Region:        region,
		TransformName: transformName,
		NamePrefix:    "celerity-e2e-" + suffix,
		AppName:       "celerity-e2e-" + suffix,
		sessionID:     "e2e-session-" + suffix,
	}
}

// Name returns a run-unique resource name from the harness prefix and a label,
// e.g. "celerity-e2e-<suffix>-<label>".
func (h *Harness) Name(label string) string {
	return fmt.Sprintf("%s-%s", h.NamePrefix, label)
}

func warnLogger() core.Logger {
	zapConfig := zap.NewDevelopmentConfig()
	zapConfig.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	zapLogger, err := zapConfig.Build()
	if err != nil {
		return core.NewNopLogger()
	}
	return core.NewLoggerFromZap(zapLogger)
}

// DeployedInstance is the result of deploying a blueprint: the instance
// identifiers, its final state (resources + exports) and the change set that
// was staged for the deploy.
type DeployedInstance struct {
	InstanceID   string
	InstanceName string
	State        state.InstanceState
	// Changes is the change set staged for the deploy that produced this
	// state, so tests can assert plan contents alongside deployed state.
	Changes *changes.BlueprintChanges
}

// ResourceSpec returns the deployed spec/external state of a resource by its
// logical name in the transformed blueprint (concrete names such as
// "myQueue_sqs_queue", not the abstract names from the fixture).
func (d *DeployedInstance) ResourceSpec(t *testing.T, name string) *core.MappingNode {
	t.Helper()
	resourceID, ok := d.State.ResourceIDs[name]
	require.Truef(t, ok, "resource %q not found in deployed instance; have: %v",
		name, resourceNames(d.State))
	resourceState, ok := d.State.Resources[resourceID]
	require.Truef(t, ok, "resource state for %q not found", name)
	return resourceState.SpecData
}

// ResourceSpecByPrefix returns the deployed spec of the single resource whose
// logical name starts with the given prefix, for concrete names with
// generated suffixes (e.g. the shared lambda execution role).
func (d *DeployedInstance) ResourceSpecByPrefix(t *testing.T, prefix string) *core.MappingNode {
	t.Helper()
	var matches []string
	for name := range d.State.ResourceIDs {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	require.Lenf(t, matches, 1,
		"expected exactly one resource with prefix %q; have: %v", prefix, resourceNames(d.State))
	return d.ResourceSpec(t, matches[0])
}

// Export returns a blueprint export value by name.
func (d *DeployedInstance) Export(t *testing.T, name string) *core.MappingNode {
	t.Helper()
	exportState, ok := d.State.Exports[name]
	require.Truef(t, ok, "export %q not found in deployed instance", name)
	return exportState.Value
}

func resourceNames(instanceState state.InstanceState) []string {
	names := make([]string, 0, len(instanceState.ResourceIDs))
	for name := range instanceState.ResourceIDs {
		names = append(names, name)
	}
	return names
}

// Deploy loads the named .blueprint file from testdata/ through the container
// pipeline (abstract validation -> celerity transform -> concrete validation),
// stages and deploys it as a new instance (injecting the run's unique
// namePrefix plus any extra blueprint variables), registers a t.Cleanup that
// destroys it, and returns the deployed instance state. Fixtures should
// reference `${variables.namePrefix}` in user-visible resource names to stay
// unique per run.
func (h *Harness) Deploy(
	t *testing.T,
	blueprintFile string,
	manifestPath string,
	vars map[string]*core.ScalarValue,
) *DeployedInstance {
	t.Helper()

	params := h.Params(manifestPath, vars)
	bp := h.load(t, blueprintFile, params)

	instanceName := "e2e-instance-" + uniqueSuffix()
	changeSet := h.stage(t, bp, instanceName, params)

	// Register teardown before deploying so partial deployments are still
	// cleaned up. Only the first deploy of an instance registers teardown;
	// StageUpdate/Redeploy never register another one — instead they update
	// the teardown holder so the destroy always runs with the LATEST
	// blueprint for the instance (destroying with a stale earlier blueprint
	// leaks resources that only later revisions introduced).
	teardown := &instanceTeardown{bp: bp, params: params}
	h.registerTeardown(instanceName, teardown)
	t.Cleanup(func() {
		teardown.mu.Lock()
		latestBP, latestParams := teardown.bp, teardown.params
		teardown.mu.Unlock()
		h.destroy(instanceName, latestBP, latestParams)
	})

	return h.deployStaged(t, blueprintFile, bp, instanceName, changeSet, params)
}

// instanceTeardown holds the most recent blueprint container + params for a
// deployed instance so the registered cleanup destroys the latest revision.
type instanceTeardown struct {
	mu     sync.Mutex
	bp     container.BlueprintContainer
	params core.BlueprintParams
}

func (h *Harness) registerTeardown(instanceName string, td *instanceTeardown) {
	h.teardownMu.Lock()
	defer h.teardownMu.Unlock()
	if h.teardowns == nil {
		h.teardowns = map[string]*instanceTeardown{}
	}
	h.teardowns[instanceName] = td
}

func (h *Harness) updateTeardown(
	instanceName string,
	bp container.BlueprintContainer,
	params core.BlueprintParams,
) {
	h.teardownMu.Lock()
	td := h.teardowns[instanceName]
	h.teardownMu.Unlock()
	if td == nil {
		return
	}
	td.mu.Lock()
	td.bp, td.params = bp, params
	td.mu.Unlock()
}

// StagedUpdate is a change set staged against an existing deployed instance.
// Tests inspect Changes to assert plan contents before deciding whether to
// apply it with Deploy.
type StagedUpdate struct {
	Changes *changes.BlueprintChanges

	harness       *Harness
	blueprintFile string
	bp            container.BlueprintContainer
	instanceName  string
	params        core.BlueprintParams
}

// StageUpdate loads the named fixture through the full transform pipeline and
// stages its changes against an existing instance's current state WITHOUT
// deploying. No destroy cleanup is registered: the first Deploy of the
// instance owns teardown.
func (h *Harness) StageUpdate(
	t *testing.T,
	blueprintFile string,
	manifestPath string,
	instanceName string,
	vars map[string]*core.ScalarValue,
) *StagedUpdate {
	t.Helper()
	params := h.Params(manifestPath, vars)
	bp := h.load(t, blueprintFile, params)
	return &StagedUpdate{
		Changes:       h.stage(t, bp, instanceName, params),
		harness:       h,
		blueprintFile: blueprintFile,
		bp:            bp,
		instanceName:  instanceName,
		params:        params,
	}
}

// Deploy applies the staged update to the existing instance and returns its
// finished state (with the staged change set attached). The instance's
// teardown is pointed at this revision first, so cleanup destroys the latest
// blueprint even if this deploy fails partway.
func (s *StagedUpdate) Deploy(t *testing.T) *DeployedInstance {
	t.Helper()
	s.harness.updateTeardown(s.instanceName, s.bp, s.params)
	return s.harness.deployStaged(
		t, s.blueprintFile, s.bp, s.instanceName, s.Changes, s.params,
	)
}

// Redeploy is StageUpdate + Deploy in one step: it stages the named fixture
// against an existing instance and deploys immediately, returning the change
// set and the finished state. Use StageUpdate directly when plan contents
// should be asserted before applying.
func (h *Harness) Redeploy(
	t *testing.T,
	blueprintFile string,
	manifestPath string,
	instanceName string,
	vars map[string]*core.ScalarValue,
) *DeployedInstance {
	t.Helper()
	return h.StageUpdate(t, blueprintFile, manifestPath, instanceName, vars).Deploy(t)
}

func (h *Harness) load(
	t *testing.T,
	blueprintFile string,
	params core.BlueprintParams,
) container.BlueprintContainer {
	t.Helper()
	bp, err := h.Loader.Load(h.Ctx, filepath.Join("testdata", blueprintFile), params)
	require.NoErrorf(t, err,
		"load blueprint %q through the container:\n%s",
		blueprintFile, FormatLoadError(err))
	return bp
}

func (h *Harness) deployStaged(
	t *testing.T,
	blueprintFile string,
	bp container.BlueprintContainer,
	instanceName string,
	changeSet *changes.BlueprintChanges,
	params core.BlueprintParams,
) *DeployedInstance {
	t.Helper()

	// Bound concurrent deploys so parallel subtests stay within AWS
	// operation limits.
	defer acquireE2ESlot()()

	deployChannels := container.CreateDeployChannels()
	err := bp.Deploy(h.Ctx, &container.DeployInput{
		InstanceName: instanceName,
		Changes:      changeSet,
	}, deployChannels, params)
	require.NoErrorf(t, err, "start deploy of %s", blueprintFile)

	finished, elementFailures := consumeDeploy(t, deployChannels)
	// A first deploy of an instance finishes DEPLOYED; a successful deploy of
	// staged changes against an existing instance finishes UPDATED.
	require.Containsf(t,
		[]core.InstanceStatus{core.InstanceStatusDeployed, core.InstanceStatusUpdated},
		finished.Status,
		"deploy of %s did not succeed (status %v): %v\nelement failures:\n%s",
		blueprintFile, finished.Status, finished.FailureReasons,
		strings.Join(elementFailures, "\n"))

	instanceState, err := h.State.Instances().Get(h.Ctx, finished.InstanceID)
	require.NoError(t, err, "read deployed instance state")
	return &DeployedInstance{
		InstanceID:   finished.InstanceID,
		InstanceName: instanceName,
		State:        instanceState,
		Changes:      changeSet,
	}
}

// Params builds blueprint params the way the deploy engine would: AWS provider
// config, plus celerity context variables selecting the aws-serverless target
// and pointing the transformer at the pre-staged build manifest.
func (h *Harness) Params(manifestPath string, vars map[string]*core.ScalarValue) core.BlueprintParams {
	contextVars := map[string]*core.ScalarValue{
		"deployTarget":              core.ScalarFromString(shared.AWSServerless),
		"session_id":                core.ScalarFromString(h.sessionID),
		shared.AppNameContextVarKey: core.ScalarFromString(h.AppName),
	}
	if manifestPath != "" {
		contextVars[shared.BuildManifestContextVarKey] = core.ScalarFromString(manifestPath)
	}

	blueprintVars := map[string]*core.ScalarValue{
		"namePrefix": core.ScalarFromString(h.NamePrefix),
	}
	maps.Copy(blueprintVars, vars)

	return core.NewDefaultParams(
		map[string]map[string]*core.ScalarValue{
			"aws": {"region": core.ScalarFromString(h.Region)},
		},
		map[string]map[string]*core.ScalarValue{
			h.TransformName: {},
		},
		contextVars,
		blueprintVars,
	)
}

func (h *Harness) stage(
	t *testing.T,
	bp container.BlueprintContainer,
	instanceName string,
	params core.BlueprintParams,
) *changes.BlueprintChanges {
	t.Helper()
	channels := newChangeStagingChannels()
	err := bp.StageChanges(h.Ctx, &container.StageChangesInput{
		InstanceName: instanceName,
	}, channels, params)
	require.NoError(t, err, "start change staging")
	return consumeStage(t, channels)
}

func (h *Harness) destroy(
	instanceName string,
	bp container.BlueprintContainer,
	params core.BlueprintParams,
) {
	// Bound concurrent destroys alongside deploys (same AWS operation limits).
	defer acquireE2ESlot()()

	channels := newChangeStagingChannels()
	if err := bp.StageChanges(h.Ctx, &container.StageChangesInput{
		InstanceName: instanceName,
		Destroy:      true,
	}, channels, params); err != nil {
		h.T.Errorf("cleanup: stage destroy for %s failed: %v", instanceName, err)
		return
	}
	changeSet, err := consumeStageErr(channels)
	if err != nil {
		h.T.Errorf("cleanup: stage destroy for %s failed: %v", instanceName, err)
		return
	}

	deployChannels := container.CreateDeployChannels()
	bp.Destroy(h.Ctx, &container.DestroyInput{
		InstanceName: instanceName,
		Changes:      changeSet,
	}, deployChannels, params)
	finished, elementFailures, err := consumeDeployErr(deployChannels)
	if err != nil {
		h.T.Errorf("cleanup: destroy of %s failed: %v", instanceName, err)
		return
	}
	if finished.Status != core.InstanceStatusDestroyed {
		h.T.Errorf("cleanup: destroy of %s ended in status %v: %v\nelement failures:\n%s",
			instanceName, finished.Status, finished.FailureReasons,
			strings.Join(elementFailures, "\n"))
	}
}

func uniqueSuffix() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), idCounter.Add(1))
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
