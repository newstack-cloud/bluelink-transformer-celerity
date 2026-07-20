//go:build unit

package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/require"
)

// The deployed IAM role name must be scoped to the application so two apps (or
// two concurrent e2e runs) with the same link-set fingerprint never collide on
// CreateRole in a shared account. The blueprint resource name stays the
// app-agnostic celerityLambdaExec_<fp>.
func TestPhysicalRoleNameIsAppScoped(t *testing.T) {
	parents := AWSServerlessSharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", true),
		},
		nil,
		"orders-app",
	)

	roles := parentsOfType(parents, "aws/iam/role")
	require.Len(t, roles, 1)
	require.True(t, strings.HasPrefix(roles[0].ResourceName, "celerityLambdaExec_"),
		"the blueprint resource name must remain app-agnostic")

	physical := core.StringValue(roles[0].SeedSpec.Fields["roleName"])
	require.True(t, strings.HasPrefix(physical, "orders-app-lambdaExec-"),
		"the deployed roleName must be scoped by the app name; got %q", physical)
	require.LessOrEqual(t, len(physical), iamRoleNameMaxLength)
}

func TestPhysicalRoleNameTruncatesLongAppNames(t *testing.T) {
	longApp := strings.Repeat("a", 80)
	name := physicalRoleName(longApp, "0db64459")
	require.LessOrEqual(t, len(name), iamRoleNameMaxLength,
		"role names must respect IAM's 64-char cap")
	require.True(t, strings.HasSuffix(name, "-lambdaExec-0db64459"),
		"the fingerprint suffix must always survive truncation; got %q", name)
}

func TestPhysicalRoleNameFallsBackToPlaceholderApp(t *testing.T) {
	name := physicalRoleName("", "0db64459")
	require.Equal(t, "placeholder-app-lambdaExec-0db64459", name,
		"validation contexts without an app name must use the placeholder")
}
