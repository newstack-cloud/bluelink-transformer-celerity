//go:build unit

package pipeline

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/stretchr/testify/require"
)

// A schedule triggering a handler that reads a mixed plaintext+secret config
// store: the absorbed schedule emits an aws/events/rule targeting the handler's
// lambda, the config emits a single aws/ssm/parameterTree splitting plaintext
// and secret keys, and the handler's CELERITY_CONFIG_* store-id env var (wired
// in the blueprint against the config's spec.id) is rewritten to the derived
// store-id value the config emits.
//
// This scenario also guards the framework's schema-walk cycle guard: the
// schedule's spec.input schema is intentionally self-referential
// (resources/schedule/schedule_resource_schema.go), which hung the container's
// Validate/Load before blueprint v0.51.1 added cycle protection.
func TestPipelineScheduleConfig(t *testing.T) {
	h := Setup(t)
	result := h.Stage(t, "schedule_config.blueprint")

	rule := result.Transformed.Resources.Values["nightlySync_events_rule"]
	require.NotNil(t, rule, "expected the schedule's events rule in the transformed output")
	require.Equal(t, "aws/events/rule", rule.Type.Value)
	require.Equal(t, "rate(1 hour)", core.StringValue(rule.Spec.Fields["scheduleExpression"]))
	// An explicit app-scoped name is required: the Cloud Control Events::Rule
	// handler NPEs on a name-less create, and deployed names must not collide
	// across apps/runs.
	require.Equal(t, "pipeline-test-app-nightlySync-rule",
		core.StringValue(rule.Spec.Fields["name"]))

	targets := rule.Spec.Fields["targets"]
	require.NotNil(t, targets)
	require.Len(t, targets.Items, 1, "expected a single rule target (the handler's lambda)")
	require.Contains(t, renderSubstitutions(targets.Items[0].Fields["arn"]),
		"syncWorker_lambda_func",
		"expected the rule target arn to reference the handler's lambda")
	requirePlanned(t, result, "nightlySync_events_rule")

	tree := result.Transformed.Resources.Values["appConfig_config_param_tree"]
	require.NotNil(t, tree, "expected the mixed config store as a single SSM parameter tree")
	require.Equal(t, "aws/ssm/parameterTree", tree.Type.Value)
	require.Equal(t, "/celerity/pipeline-test-app/appConfig",
		core.StringValue(tree.Spec.Fields["path"]))
	require.Equal(t, "info", core.StringValue(tree.Spec.Fields["values"].Fields["logLevel"]),
		"plaintext key must land in the values map")
	require.Equal(t, "s3cr3t", core.StringValue(tree.Spec.Fields["secureValues"].Fields["dbPassword"]),
		"secret key must land in the secureValues map")
	requirePlanned(t, result, "appConfig_config_param_tree")

	lambda := result.Transformed.Resources.Values["syncWorker_lambda_func"]
	require.NotNil(t, lambda)
	env := lambda.Spec.Fields["environment"].Fields["variables"].Fields

	// The blueprint wires CELERITY_CONFIG_APP_STORE_ID to the config's spec.id;
	// the transform must rewrite that abstract reference to the derived
	// appConfig_config_store_id value (whose literal is the store path).
	require.Contains(t, renderSubstitutions(env["CELERITY_CONFIG_APP_STORE_ID"]),
		"appConfig_config_store_id",
		"expected the store-id env var to be rewritten to the config's derived value")
	require.Contains(t, result.Transformed.Values.Values, "appConfig_config_store_id",
		"expected the config to emit the derived store-id value")
	requirePlanned(t, result, "syncWorker_lambda_func")

	// Contract behaviour (docs/contract/aws-serverless.md sections 10.3 and 11):
	// the transformer auto-stamps per-store CELERITY_CONFIG_<NS>_STORE_ID/_KIND
	// env vars for the linked user celerity/config store (namespace derived from
	// the store name "appConfig" -> APPCONFIG), the emitted parameter tree
	// carries the abstract config's labels so the handler's preserved
	// linkSelector matches it, and the transformer stamps the
	// aws.lambda.ssm.<concreteStore>.* link annotations so the provider link
	// injects the scoped read grant under the contract env var name.
	require.Contains(t, renderSubstitutions(env["CELERITY_CONFIG_APPCONFIG_STORE_ID"]),
		"appConfig_config_store_id",
		"the auto-stamped store-id env var must reference the config's derived store-id value")
	require.Equal(t, "parameter-store",
		core.StringValue(env["CELERITY_CONFIG_APPCONFIG_STORE_KIND"]),
		"a mixed plaintext+secret store must select the parameter-store backend kind")

	require.NotNil(t, tree.Metadata, "expected metadata on the emitted parameter tree")
	require.NotNil(t, tree.Metadata.Labels, "the tree must carry the abstract config's labels")
	require.Equal(t, "appConfig", tree.Metadata.Labels.Values["store"])

	annotations := lambda.Metadata.Annotations.Values
	require.Contains(t, renderStringSubstitutions(
		annotations["aws.lambda.ssm.appConfig_config_param_tree.envVarName"]),
		"CELERITY_CONFIG_APPCONFIG_STORE_ID",
		"the link's envVarName annotation must rename the injected env var to the contract name")
	require.Contains(t, renderStringSubstitutions(
		annotations["aws.lambda.ssm.appConfig_config_param_tree.accessLevel"]),
		"read")

	// With labels on the tree and the selector on the lambda, the concrete
	// lambda -> parameterTree link forms during staging.
	lambdaChanges := result.Changes.NewResources["syncWorker_lambda_func"]
	require.Contains(t, lambdaChanges.NewOutboundLinks, "appConfig_config_param_tree",
		"expected a concrete lambda -> parameterTree link for the user config store")
}

// renderStringSubstitutions renders an annotation value back to its canonical
// string form; empty for nil or unrenderable values.
func renderStringSubstitutions(value *substitutions.StringOrSubstitutions) string {
	if value == nil {
		return ""
	}
	rendered, err := substitutions.SubstitutionsToString("", value)
	if err != nil {
		return ""
	}
	return rendered
}
