package shared

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// ResolveAppName retrieves the app name from the transform context, if available.
func ResolveAppName(run *transformutils.Run) string {
	if run == nil || run.TransformContext == nil {
		return ""
	}

	ctxVar, _ := run.TransformContext.ContextVariable(AppNameContextVarKey)
	appName := core.StringValueFromScalar(ctxVar)
	return appName
}
