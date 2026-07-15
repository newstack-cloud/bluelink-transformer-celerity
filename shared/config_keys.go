package shared

const (
	// BuildManifestContextVarKey is the key used to store the location
	// of the build manifest file in the transform context.
	BuildManifestContextVarKey = "celerity.buildManifest"
	// AppNameContextVarKey is the project name surfaced by the CLI from
	// the app.deploy.jsonc "appName" field.
	AppNameContextVarKey = "celerity.appName"
)
