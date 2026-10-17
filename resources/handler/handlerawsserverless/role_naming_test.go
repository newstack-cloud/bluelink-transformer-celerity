//go:build unit

package handlerawsserverless

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type RoleNamingTestSuite struct {
	suite.Suite
}

func TestRoleNamingTestSuite(t *testing.T) {
	suite.Run(t, new(RoleNamingTestSuite))
}

// The deployed IAM role name must be scoped to the application so two apps (or
// two concurrent e2e runs) with the same link-set fingerprint never collide on
// CreateRole in a shared account. The blueprint resource name stays the
// app-agnostic celerityLambdaExec_<fp>.
func (s *RoleNamingTestSuite) Test_physical_role_name_is_app_scoped() {
	parents := SharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", true),
		},
		nil,
		"orders-app",
	)

	roles := parentsOfType(parents, "aws/iam/role")
	s.Require().Len(roles, 1)
	s.Require().True(strings.HasPrefix(roles[0].ResourceName, "celerityLambdaExec_"),
		"the blueprint resource name must remain app-agnostic")

	physical := core.StringValue(roles[0].SeedSpec.Fields["roleName"])
	s.Require().True(strings.HasPrefix(physical, "orders-app-lambdaExec-"),
		"the deployed roleName must be scoped by the app name; got %q", physical)
	s.Require().LessOrEqual(len(physical), iamRoleNameMaxLength)
}

func (s *RoleNamingTestSuite) Test_physical_role_name_truncates_long_app_names() {
	longApp := strings.Repeat("a", 80)
	name := physicalRoleName(longApp, "0db64459")
	s.Require().LessOrEqual(len(name), iamRoleNameMaxLength,
		"role names must respect IAM's 64-char cap")
	s.Require().True(strings.HasSuffix(name, "-lambdaExec-0db64459"),
		"the fingerprint suffix must always survive truncation; got %q", name)
}

func (s *RoleNamingTestSuite) Test_physical_role_name_falls_back_to_placeholder_app() {
	name := physicalRoleName("", "0db64459")
	s.Require().Equal("placeholder-app-lambdaExec-0db64459", name,
		"validation contexts without an app name must use the placeholder")
}
