//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/newstack-cloud/bluelink/libs/blueprint/changes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/blueprint/state"
	"github.com/stretchr/testify/require"
)

const (
	iamRolePrefix = "celerityLambdaExec_"

	handlerResourceName = "syncHandler_lambda_func"
	ruleResourceName    = "nightlySync_events_rule"
	queueResourceName   = "jobsQueue_sqs_queue"

	v2ScheduleExpression = "rate(2 hours)"
	v2SyncModeValue      = "full"
)

// TestUpdateLifecycle exercises the update path against real AWS across three
// stages on one instance:
//
//  1. deploy the trimmed core_v1 fixture (schedule + scheduled handler +
//     config; no API, because a handler carries a single event-source
//     annotation and the update assertions need the schedule side),
//  2. restage core_v1 unchanged and assert the change set is a resource no-op
//     (no new/removed resources, no staged field changes), skipping the deploy,
//  3. stage core_v2 (changed handler env var, changed schedule expression, and
//     a new queue linked from the handler — which changes the handler's IAM
//     role fingerprint) and assert the plan: lambda + rule are in-place
//     updates, the new queue and the new-fingerprint role are creates, and the
//     old-fingerprint role is a removal. Then deploy and assert the new env
//     var value and schedule expression on real AWS.
//
// Cleanup destroys the instance once, via the t.Cleanup registered by the
// FIRST deploy.
func TestUpdateLifecycle(t *testing.T) {
	t.Parallel()
	h := Setup(t)
	manifestPath := h.PrestageArtifacts(t)

	v1 := h.Deploy(t, "core_v1.blueprint", manifestPath, nil)
	oldRoleName := singleResourceNameByPrefix(t, v1.State, iamRolePrefix)

	restaged := h.StageUpdate(t, "core_v1.blueprint", manifestPath, v1.InstanceName, nil)
	assertResourceNoOp(t, restaged.Changes)
	t.Log("unchanged restage staged a resource no-op; skipping its deploy")

	staged := h.StageUpdate(t, "core_v2.blueprint", manifestPath, v1.InstanceName, nil)
	assertV2Plan(t, staged.Changes, oldRoleName)

	v2 := staged.Deploy(t)
	assertV2State(t, v2, oldRoleName)

	functionName := stringField(v2.ResourceSpec(t, handlerResourceName), "functionName")
	require.NotEmpty(t, functionName, "expected the updated handler's function name in state")
	assertHandlerEnvVarUpdated(t, h, functionName)
	assertRuleScheduleUpdated(t, h, v2)
	assertAllFunctionEnvVarsResolved(t, h, functionName)
}

// Asserts a restage of an unchanged blueprint plans no
// resource-level work: nothing new, nothing removed, and no existing resource
// carries staged field changes or a recreate. Entries in ResourceChanges with
// only unchanged/known-on-deploy bookkeeping fields are tolerated — the
// staging engine records every resource it examined — but any modified, new or
// removed FIELD (or MustRecreate) means the plan would touch AWS and fails the
// assertion.
func assertResourceNoOp(t *testing.T, changeSet *changes.BlueprintChanges) {
	t.Helper()
	require.Empty(t, changeSet.NewResources, "unchanged restage must plan no new resources")
	require.Empty(t, changeSet.RemovedResources, "unchanged restage must plan no removed resources")
	require.Empty(t, changeSet.RemovedLinks, "unchanged restage must plan no removed links")
	for name, resourceChanges := range changeSet.ResourceChanges {
		require.Falsef(t, resourceChanges.MustRecreate,
			"unchanged restage must not plan a recreate for %s", name)
		require.Emptyf(t, resourceChanges.ModifiedFields,
			"unchanged restage must not plan modified fields for %s", name)
		require.Emptyf(t, resourceChanges.NewFields,
			"unchanged restage must not plan new fields for %s", name)
		require.Emptyf(t, resourceChanges.RemovedFields,
			"unchanged restage must not plan removed fields for %s", name)
	}
}

func assertV2Plan(t *testing.T, changeSet *changes.BlueprintChanges, oldRoleName string) {
	t.Helper()

	assertPlannedInPlaceUpdate(t, changeSet, handlerResourceName)
	assertPlannedInPlaceUpdate(t, changeSet, ruleResourceName)

	require.Contains(t, changeSet.NewResources, queueResourceName,
		"expected the new queue in NewResources")

	newRoleName := singleKeyByPrefix(t, changeSet.NewResources, iamRolePrefix)
	require.NotEqual(t, oldRoleName, newRoleName,
		"the added handler::queue link must change the role fingerprint")
	require.Contains(t, changeSet.RemovedResources, oldRoleName,
		"expected the old-fingerprint role to be planned for removal")
}

// assertPlannedInPlaceUpdate asserts the named resource is planned as an
// UPDATE of the existing resource: present in ResourceChanges with real field
// changes, not a recreate, and not a create.
func assertPlannedInPlaceUpdate(
	t *testing.T,
	changeSet *changes.BlueprintChanges,
	resourceName string,
) {
	t.Helper()
	require.NotContainsf(t, changeSet.NewResources, resourceName,
		"%s must not be planned as a new resource", resourceName)
	resourceChanges, ok := changeSet.ResourceChanges[resourceName]
	require.Truef(t, ok, "expected %s in ResourceChanges; have: %v",
		resourceName, mapKeys(changeSet.ResourceChanges))
	require.Falsef(t, resourceChanges.MustRecreate,
		"%s must be updated in place, not recreated", resourceName)
	require.Truef(t, provider.ChangesHasFieldChanges(&resourceChanges),
		"expected staged field changes for %s", resourceName)
}

func assertV2State(t *testing.T, v2 *DeployedInstance, oldRoleName string) {
	t.Helper()
	require.Contains(t, v2.State.ResourceIDs, queueResourceName,
		"expected the new queue in the updated instance state")
	newRoleName := singleResourceNameByPrefix(t, v2.State, iamRolePrefix)
	require.NotEqual(t, oldRoleName, newRoleName,
		"expected only the new-fingerprint role in the updated instance state")
}

func assertHandlerEnvVarUpdated(t *testing.T, h *Harness, functionName string) {
	t.Helper()
	client := lambda.NewFromConfig(h.AWSConfig)
	waitFor(t, wiringAssertTimeout, 5*time.Second,
		fmt.Sprintf("SYNC_MODE=%q on function %s", v2SyncModeValue, functionName),
		func() (bool, error) {
			vars, err := functionEnvVars(h, client, functionName)
			if err != nil {
				return false, err
			}
			return vars["SYNC_MODE"] == v2SyncModeValue, nil
		})
}

func assertRuleScheduleUpdated(t *testing.T, h *Harness, v2 *DeployedInstance) {
	t.Helper()
	ruleName := stringField(v2.ResourceSpec(t, ruleResourceName), "name")
	require.NotEmpty(t, ruleName, "expected the deployed rule name in state")

	client := eventbridge.NewFromConfig(h.AWSConfig)
	waitFor(t, wiringAssertTimeout, 5*time.Second,
		fmt.Sprintf("rule %s schedule expression to be %q", ruleName, v2ScheduleExpression),
		func() (bool, error) {
			out, err := client.DescribeRule(h.Ctx, &eventbridge.DescribeRuleInput{
				Name: aws.String(ruleName),
			})
			if err != nil {
				// The rule exists before the update deploy, so an error here
				// should only ever be a transient read failure; retry.
				return false, nil
			}
			return aws.ToString(out.ScheduleExpression) == v2ScheduleExpression, nil
		})
}

// singleResourceNameByPrefix returns the single resource name in the instance
// state starting with the given prefix (e.g. the shared IAM execution role,
// whose name embeds the role-plan fingerprint).
func singleResourceNameByPrefix(
	t *testing.T,
	instanceState state.InstanceState,
	prefix string,
) string {
	t.Helper()
	var matches []string
	for name := range instanceState.ResourceIDs {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	require.Lenf(t, matches, 1,
		"expected exactly one resource with prefix %q; have: %v",
		prefix, resourceNames(instanceState))
	return matches[0]
}

func singleKeyByPrefix(
	t *testing.T,
	m map[string]provider.Changes,
	prefix string,
) string {
	t.Helper()
	var matches []string
	for name := range m {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	require.Lenf(t, matches, 1,
		"expected exactly one key with prefix %q; have: %v", prefix, mapKeys(m))
	return matches[0]
}

func mapKeys(m map[string]provider.Changes) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
