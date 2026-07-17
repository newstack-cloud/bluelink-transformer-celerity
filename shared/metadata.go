package shared

import (
	"fmt"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PlaceholderAppName is the app-name path segment used in validation contexts,
// where the app name context variable is not yet available.
const PlaceholderAppName = "placeholder-app"

// resourceLinksStoreName is the store-name segment of the internal resources
// namespace config store path.
const resourceLinksStoreName = "resources"

// ResolveAppName retrieves the app name from the transform context, if available.
func ResolveAppName(run *transformutils.Run) string {
	if run == nil || run.TransformContext == nil {
		return ""
	}

	ctxVar, _ := run.TransformContext.ContextVariable(AppNameContextVarKey)
	appName := core.StringValueFromScalar(ctxVar)
	return appName
}

// ResourceLinksStorePath returns the SSM Parameter Store path prefix of the
// internal resources namespace config store for this run. The store emission and
// the handler env var / IAM all derive the path from here so they always agree; the
// placeholder app name is used when the context has no app name (validation).
func ResourceLinksStorePath(run *transformutils.Run) string {
	appName := ResolveAppName(run)
	if appName == "" {
		appName = PlaceholderAppName
	}
	return fmt.Sprintf("/celerity/%s/%s", appName, resourceLinksStoreName)
}
