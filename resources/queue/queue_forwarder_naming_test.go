//go:build unit

package queue

import (
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/require"
)

// Deployed forwarder names must be app-scoped so two apps (or two concurrent
// e2e runs) sharing an account never collide on CreateFunction/CreateRole;
// the blueprint RESOURCE names stay the app-agnostic
// <queue>_topic_forwarder(_role) that selectors and tests reference.
func TestForwarderPhysicalNamesAreAppScoped(t *testing.T) {
	resources, err := buildTopicForwarder("sourceQueue", "orders-app", []*TopicForwardEdge{
		{TopicName: "events", SelectorLabels: map[string]string{"topic": "events"}},
	})
	require.NoError(t, err)

	fn := resources["sourceQueue_topic_forwarder"]
	require.NotNil(t, fn, "the forwarder function resource name must stay app-agnostic")
	require.Equal(t, "orders-app-sourceQueue-fwd",
		core.StringValue(fn.Spec.Fields["functionName"]),
		"the deployed function name must be scoped by the app name")

	role := resources["sourceQueue_topic_forwarder_role"]
	require.NotNil(t, role, "the forwarder role resource name must stay app-agnostic")
	require.Equal(t, "orders-app-sourceQueue-fwd-role",
		core.StringValue(role.Spec.Fields["roleName"]),
		"the deployed role name must be scoped by the app name")
}

func TestForwarderPhysicalNamesFallBackToPlaceholderApp(t *testing.T) {
	resources, err := buildTopicForwarder("sourceQueue", "", []*TopicForwardEdge{
		{TopicName: "events", SelectorLabels: map[string]string{"topic": "events"}},
	})
	require.NoError(t, err)

	fn := resources["sourceQueue_topic_forwarder"]
	require.True(t,
		strings.HasPrefix(core.StringValue(fn.Spec.Fields["functionName"]), "placeholder-app-"),
		"validation contexts without an app name must use the placeholder")
}
