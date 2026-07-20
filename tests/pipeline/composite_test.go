//go:build unit

package pipeline

import (
	"encoding/json"
	"maps"
	"regexp"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/stretchr/testify/require"
)

// Concrete resource types owned by provider links for in-blueprint event
// sources; the transformer must never emit them itself
// (docs/contract/aws-serverless.md section 3.5).
var linkOwnedTypes = []string{
	"aws/lambda/permission",
	"aws/apigatewayv2/integration",
	"aws/apigatewayv2/route",
	"aws/lambda/eventSourceMapping",
}

// A realistic composite app: an HTTP API fronting three handlers (two with
// identical outbound link sets, one divergent), a nameless datastore, a topic
// and a bucket wired to a queue. Assertions are cross-resource invariants only;
// per-field emission is covered by the transformer unit tests. The datastore is
// deliberately name-less: its resources-store entry references the table's
// computed arn output, so full staging here proves a name-less handler-linked
// datastore stages cleanly.
func TestPipelineCompositeApp(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "composite_app.blueprint")

	assertExecRoleDedup(t, result.Transformed)
	assertSharedByAnnotations(t, result.Transformed)
	assertSingletonSharedResources(t, result)
	assertNoLinkOwnedTypes(t, result.Transformed)
}

// saveOrder and getOrder have identical link sets (api inbound + ordersTable
// outbound) so they must share one execution role; publishOrderEvents also
// links the topic (its ordersFlow selector matches both the table and the
// topic, declared-link label matching is per-selector-key)
// so it must get a different role.
func assertExecRoleDedup(t *testing.T, transformed *schema.Blueprint) {
	t.Helper()
	roles := transformedNamesWithPrefix(transformed, "celerityLambdaExec_")
	require.Len(t, roles, 2,
		"expected exactly two shared execution roles (one per distinct link set); got %v", roles)

	saveRole := lambdaExecRoleRef(t, transformed, "saveOrder_lambda_func")
	getRole := lambdaExecRoleRef(t, transformed, "getOrder_lambda_func")
	publishRole := lambdaExecRoleRef(t, transformed, "publishOrderEvents_lambda_func")

	require.Equal(t, saveRole, getRole,
		"handlers with identical outbound link sets must share one execution role")
	require.NotEqual(t, saveRole, publishRole,
		"a handler with a divergent link set must get its own execution role")
}

// Each shared execution role records every handler using it in the
// celerity.handler.sharedBy annotation — a sorted, comma-separated list.
// The shared layer carries NO sharedBy annotation.
func assertSharedByAnnotations(t *testing.T, transformed *schema.Blueprint) {
	t.Helper()
	saveRole := lambdaExecRoleRef(t, transformed, "saveOrder_lambda_func")
	publishRole := lambdaExecRoleRef(t, transformed, "publishOrderEvents_lambda_func")

	require.Equal(t, "getOrder,saveOrder",
		sharedParentAnnotation(t, transformed, saveRole, "celerity.handler.sharedBy"),
		"the shared role must list both sharers, sorted")
	require.Equal(t, "publishOrderEvents",
		sharedParentAnnotation(t, transformed, publishRole, "celerity.handler.sharedBy"),
		"the unshared role lists only its single handler")

	layers := transformedNamesWithPrefix(transformed, "celerityLambdaLayer_")
	require.Len(t, layers, 1)
	layer := transformedResource(t, transformed, layers[0])
	require.NotNil(t, layer.Metadata)
	require.NotNil(t, layer.Metadata.Custom)
	require.Nil(t, layer.Metadata.Custom.Fields["celerity.handler.sharedBy"],
		"the shared layer must not carry sharedBy (contract section 9)")
}

// Reads a shared-parent resource's annotation; the
// framework places SharedParent annotations in metadata custom.
func sharedParentAnnotation(t *testing.T, bp *schema.Blueprint, resourceName, key string) string {
	t.Helper()
	resource := transformedResource(t, bp, resourceName)
	require.NotNil(t, resource.Metadata, "shared parent %q has no metadata", resourceName)
	require.NotNil(t, resource.Metadata.Custom, "shared parent %q has no custom metadata", resourceName)
	return core.StringValue(resource.Metadata.Custom.Fields[key])
}

func assertSingletonSharedResources(t *testing.T, result *StageResult) {
	t.Helper()
	layers := transformedNamesWithPrefix(result.Transformed, "celerityLambdaLayer_")
	require.Len(t, layers, 1,
		"the shared layer must be deduplicated to exactly one layerVersion; got %v", layers)

	stores := transformedNamesWithPrefix(result.Transformed, "celerityResourcesConfigStore")
	require.Len(t, stores, 1, "expected exactly one resources config store; got %v", stores)

	for _, name := range append(layers, stores...) {
		require.Contains(t, result.Changes.NewResources, name,
			"shared resource %q must survive concrete validation into the change set", name)
	}
}

func assertNoLinkOwnedTypes(t *testing.T, transformed *schema.Blueprint) {
	t.Helper()
	types := transformedResourceTypes(transformed)
	for _, forbidden := range linkOwnedTypes {
		require.NotContains(t, types, forbidden,
			"link-owned type %q must not be emitted for in-blueprint sources (contract section 3.5)",
			forbidden)
	}
}

func transformedResource(t *testing.T, bp *schema.Blueprint, name string) *schema.Resource {
	t.Helper()
	require.NotNil(t, bp.Resources, "transformed blueprint has no resources")
	resource, ok := bp.Resources.Values[name]
	require.True(t, ok, "expected resource %q in the transformed blueprint; got: %v",
		name, transformedNamesWithPrefix(bp, ""))
	return resource
}

func transformedResourceTypes(bp *schema.Blueprint) map[string][]string {
	types := map[string][]string{}
	if bp.Resources == nil {
		return types
	}
	for name, resource := range bp.Resources.Values {
		if resource.Type != nil {
			types[resource.Type.Value] = append(types[resource.Type.Value], name)
		}
	}
	return types
}

func transformedNamesWithPrefix(bp *schema.Blueprint, prefix string) []string {
	names := []string{}
	if bp.Resources == nil {
		return names
	}
	for name := range bp.Resources.Values {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names
}

func specNode(t *testing.T, bp *schema.Blueprint, resourceName, path string) *core.MappingNode {
	t.Helper()
	resource := transformedResource(t, bp, resourceName)
	node, ok := pluginutils.GetValueByPath(path, resource.Spec)
	require.True(t, ok, "expected %s in the spec of %q", path, resourceName)
	return node
}

// Mirrors Harness.Params but also lets a test supply transformer
// config (e.g. the "aws.region" deploy config the production engine forwards to
// the transformer, required by the celerity/vpc emit) and extra context
// variables (e.g. the reserved validation-context flag). The harness helper
// intentionally exposes neither.
func pipelineParams(
	h *Harness,
	manifestPath string,
	transformerConfig map[string]*core.ScalarValue,
	extraContextVars map[string]*core.ScalarValue,
) core.BlueprintParams {
	contextVars := map[string]*core.ScalarValue{
		"deployTarget":              core.ScalarFromString(shared.AWSServerless),
		shared.AppNameContextVarKey: core.ScalarFromString("pipeline-test-app"),
	}
	if manifestPath != "" {
		contextVars[shared.BuildManifestContextVarKey] = core.ScalarFromString(manifestPath)
	}
	maps.Copy(contextVars, extraContextVars)

	mergedTransformerConfig := map[string]*core.ScalarValue{}
	maps.Copy(mergedTransformerConfig, transformerConfig)

	return core.NewDefaultParams(
		map[string]map[string]*core.ScalarValue{
			"aws": {"region": core.ScalarFromString(testRegion)},
		},
		map[string]map[string]*core.ScalarValue{
			h.TransformName: mergedTransformerConfig,
		},
		contextVars,
		map[string]*core.ScalarValue{},
	)
}

func awsRegionTransformerConfig() map[string]*core.ScalarValue {
	return map[string]*core.ScalarValue{
		"aws.region": core.ScalarFromString(testRegion),
	}
}

var execRoleRefPattern = regexp.MustCompile(`celerityLambdaExec_[0-9a-fA-F]+`)

// The role reference is a "${resources.celerityLambdaExec_<fp>.spec.arn}"
// substitution; extracting the fingerprint from the serialised spec avoids
// coupling the test to the substitution AST.
func lambdaExecRoleRef(t *testing.T, bp *schema.Blueprint, lambdaName string) string {
	t.Helper()
	resource := transformedResource(t, bp, lambdaName)
	serialised, err := json.Marshal(resource.Spec)
	require.NoError(t, err, "marshal spec of %q", lambdaName)
	ref := execRoleRefPattern.FindString(string(serialised))
	require.NotEmpty(t, ref, "expected %q to reference a shared execution role", lambdaName)
	return ref
}
