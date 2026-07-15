//go:build unit

package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TransformTestSuite struct {
	suite.Suite
}

func (s *TransformTestSuite) Test_transforms_handler_into_lambda_function_with_env_and_role() {
	out := s.transformOneHandler()

	resources := out.TransformedBlueprint.Resources.Values
	lambda, ok := resources["myHandler_lambda_func"]
	s.Require().True(ok, "Expected a Lambda function resource emitted for the handler")
	s.Equal("aws/lambda/function", lambda.Type.Value)

	spec := lambda.Spec.Fields
	s.Equal("myHandler", core.StringValue(spec["functionName"]))
	s.Equal("nodejs24.x", core.StringValue(spec["runtime"]))

	env := spec["environment"].Fields["variables"].Fields
	s.Equal("aws", core.StringValue(env["CELERITY_PLATFORM"]))
	s.Equal("aws-serverless", core.StringValue(env["CELERITY_DEPLOY_TARGET"]))
	// CELERITY_HANDLER_ID is spec.handler (the code-entry reference), not handlerName.
	s.Equal("handlers.save", core.StringValue(env["CELERITY_HANDLER_ID"]))
	s.Equal("json", core.StringValue(env["CELERITY_LOG_FORMAT"]))

	roleResourceName, rolePath := s.resourcePropertyRef(spec["role"])
	s.Equal([]string{"spec", "arn"}, rolePath)
	s.True(
		strings.HasPrefix(roleResourceName, "celerityLambdaExec_"),
		"Expected the role to reference a shared execution role resource",
	)
	role, ok := resources[roleResourceName]
	s.Require().True(ok, "Expected the referenced execution role to be emitted in the output")
	s.Equal("aws/iam/role", role.Type.Value)
}

// Extracts the referenced resource name and property path
// from a MappingNode holding a single ${resources.<name>.<path>} substitution.
func (s *TransformTestSuite) resourcePropertyRef(node *core.MappingNode) (string, []string) {
	s.Require().NotNil(node)
	s.Require().NotNil(node.StringWithSubstitutions)
	s.Require().Len(node.StringWithSubstitutions.Values, 1)

	sub := node.StringWithSubstitutions.Values[0].SubstitutionValue
	s.Require().NotNil(sub)
	s.Require().NotNil(sub.ResourceProperty)

	path := make([]string, 0, len(sub.ResourceProperty.Path))
	for _, item := range sub.ResourceProperty.Path {
		path = append(path, item.FieldName)
	}
	return sub.ResourceProperty.ResourceName, path
}

func (s *TransformTestSuite) Test_emits_derived_values_for_referenceability() {
	out := s.transformOneHandler()

	s.Require().NotNil(out.TransformedBlueprint.Values)
	values := out.TransformedBlueprint.Values.Values
	s.Contains(values, "myHandler_lambda_func_celerity_runtime")
	s.Contains(values, "myHandler_lambda_func_handler_id")
	s.Equal(
		"nodejs24.x",
		core.StringValue(values["myHandler_lambda_func_celerity_runtime"].Value),
	)
	s.Equal(
		"handlers.save",
		core.StringValue(values["myHandler_lambda_func_handler_id"].Value),
	)
}

func (s *TransformTestSuite) Test_transform_context_wires_manifest_code_and_layer() {
	transformer := NewTransformer(manifestDeps())
	out, err := transformer.Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     oneHandlerBlueprint(),
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: transformContext(writeManifest(s.T())),
		},
	)
	s.Require().NoError(err)

	resources := out.TransformedBlueprint.Resources.Values
	lambda := resources["myHandler_lambda_func"]
	s.Require().NotNil(lambda)
	spec := lambda.Spec.Fields

	// Code + handler come from the manifest, not the validation placeholder.
	s.Equal("app-bucket", core.StringValue(spec["code"].Fields["s3Bucket"]))
	s.Equal("app.zip", core.StringValue(spec["code"].Fields["s3Key"]))
	s.Equal("__celerity_lambda_entry__.handler", core.StringValue(spec["handler"]))

	// layers[0] references the emitted layerVersion by its layerVersionArn.
	s.Require().NotNil(spec["layers"])
	s.Require().Len(spec["layers"].Items, 1)
	layerName, layerPath := s.resourcePropertyRef(spec["layers"].Items[0])
	s.Equal([]string{"spec", "layerVersionArn"}, layerPath)
	s.Equal(fmt.Sprintf("celerityLambdaLayer_%s", testLayerHash), layerName)

	// Make sure that the layerVersion resource seeded from the manifest
	// is actually emitted.
	layer := resources[layerName]
	s.Require().NotNil(layer, "expected the referenced layerVersion to be emitted")
	s.Equal("aws/lambda/layerVersion", layer.Type.Value)
	s.Equal("layer-bucket", core.StringValue(layer.Spec.Fields["content"].Fields["s3Bucket"]))
	s.Equal([]string{"nodejs24.x"}, core.StringSliceValue(layer.Spec.Fields["compatibleRuntimes"]))
}

func (s *TransformTestSuite) Test_transform_context_prefers_custom_dependency_layer() {
	out, err := NewTransformer(manifestDeps()).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: oneHandlerBlueprint(),
			LinkGraph:      emptyLinkGraph{},
			TransformerContext: transformContext(
				writeManifestWithHandlerScopedLayer(s.T()),
			),
		},
	)
	s.Require().NoError(err)

	resources := out.TransformedBlueprint.Resources.Values
	spec := resources["myHandler_lambda_func"].Spec.Fields

	// The handler's custom dependency layer should be selected over the shared layer.
	s.Require().Len(spec["layers"].Items, 1)
	layerName, layerPath := s.resourcePropertyRef(spec["layers"].Items[0])
	s.Equal([]string{"spec", "layerVersionArn"}, layerPath)
	s.Equal(fmt.Sprintf("celerityLambdaLayer_%s", testHandlerScopedLayerHash), layerName)

	// Layer emitted from the per-handler artifact.
	layer := resources[layerName]
	s.Require().NotNil(layer)
	s.Equal("aws/lambda/layerVersion", layer.Type.Value)
	s.Equal("custom-bucket", core.StringValue(layer.Spec.Fields["content"].Fields["s3Bucket"]))

	// The unused shared layer should not be emitted (no handler selected it).
	s.NotContains(resources, fmt.Sprintf("celerityLambdaLayer_%s", testLayerHash))
}

func (s *TransformTestSuite) transformOneHandler() *transform.SpecTransformerTransformOutput {
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     oneHandlerBlueprint(),
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}

func TestTransformTestSuite(t *testing.T) {
	suite.Run(t, new(TransformTestSuite))
}

func oneHandlerBlueprint() *schema.Blueprint {
	return &schema.Blueprint{
		Resources: &schema.ResourceMap{
			Values: map[string]*schema.Resource{
				"myHandler": {
					Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
					Spec: core.MappingNodeFields(
						"handlerName", core.MappingNodeFromString("myHandler"),
						"handler", core.MappingNodeFromString("handlers.save"),
						"runtime", core.MappingNodeFromString("nodejs24.x"),
					),
				},
			},
		},
	}
}

const testLayerHash = "layerHash123"

func writeManifest(t *testing.T) string {

	return writeManifestFile(t, &build.Manifest{
		Version: "1",
		Target:  shared.AWSServerless,
		Lambda: &build.LambdaManifest{
			AppCode: &build.LambdaArtifact{
				Type:        "zip",
				S3Bucket:    "app-bucket",
				S3Key:       "app.zip",
				ContentHash: "appHash",
			},
			EntryPoint: "__celerity_lambda_entry__.handler",
			SharedLayer: &build.LambdaArtifact{
				Type:        "zip",
				S3Bucket:    "layer-bucket",
				S3Key:       "deps.zip",
				ContentHash: testLayerHash,
			},
		},
	})
}

const testHandlerScopedLayerHash = "customHash456"

func writeManifestWithHandlerScopedLayer(t *testing.T) string {
	return writeManifestFile(t, &build.Manifest{
		Version: "1",
		Target:  shared.AWSServerless,
		Lambda: &build.LambdaManifest{
			AppCode: &build.LambdaArtifact{
				Type:        "zip",
				S3Bucket:    "app-bucket",
				S3Key:       "app.zip",
				ContentHash: "appHash",
			},
			EntryPoint: "__celerity_lambda_entry__.handler",
			SharedLayer: &build.LambdaArtifact{
				Type:        "zip",
				S3Bucket:    "layer-bucket",
				S3Key:       "deps.zip",
				ContentHash: testLayerHash,
			},
		},
		Handlers: map[string]*build.HandlerArtifacts{
			"myHandler": {
				Lambda: &build.LambdaHandlerArtifacts{
					Dependencies: &build.LambdaArtifact{
						Type:        "zip",
						S3Bucket:    "custom-bucket",
						S3Key:       "custom-deps.zip",
						ContentHash: testHandlerScopedLayerHash,
					},
				},
			},
		},
	})
}

func writeManifestFile(t *testing.T, manifest *build.Manifest) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "build-manifest.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func manifestDeps() *shared.Dependencies {
	return &shared.Dependencies{
		BuildManifestLoader: build.NewManifestLoader(
			build.WithDefaultResourceLoader(
				build.NewFSResourceLoader(afero.NewOsFs()),
			),
		),
	}
}

func validationContext() transform.Context {
	return &fakeTransformContext{
		contextVars: map[string]*core.ScalarValue{
			core.ValidationContextVariableName: core.ScalarFromBool(true),
			// The plugin resolves the deploy target from the "deployTarget"
			// context variable.
			"deployTarget": core.ScalarFromString(shared.AWSServerless),
		},
	}
}

// transform (not validation) context, pointing the OnRun hook at the manifest.
func transformContext(manifestPath string) transform.Context {
	return &fakeTransformContext{
		contextVars: map[string]*core.ScalarValue{
			"deployTarget":                    core.ScalarFromString(shared.AWSServerless),
			shared.BuildManifestContextVarKey: core.ScalarFromString(manifestPath),
			// no ValidationContextVariableName -> this is a real transform run
		},
	}
}

type fakeTransformContext struct {
	configVars  map[string]*core.ScalarValue
	contextVars map[string]*core.ScalarValue
}

func (f *fakeTransformContext) TransformerConfigVariable(name string) (*core.ScalarValue, bool) {
	v, ok := f.configVars[name]
	return v, ok
}

func (f *fakeTransformContext) TransformerConfigVariables() map[string]*core.ScalarValue {
	return f.configVars
}

func (f *fakeTransformContext) ContextVariable(name string) (*core.ScalarValue, bool) {
	v, ok := f.contextVars[name]
	return v, ok
}

func (f *fakeTransformContext) ContextVariables() map[string]*core.ScalarValue {
	return f.contextVars
}

type emptyLinkGraph struct{}

func (emptyLinkGraph) Edges() []*linktypes.ResolvedLink {
	return nil
}

func (emptyLinkGraph) EdgesFrom(string) []*linktypes.ResolvedLink {
	return nil
}

func (emptyLinkGraph) EdgesTo(string) []*linktypes.ResolvedLink {
	return nil
}

func (emptyLinkGraph) Resource(string) (*schema.Resource, linktypes.ResourceClass, bool) {
	return nil, "", false
}
