package shared

import "github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"

// Dependencies defines the dependencies that are passed through in the construction
// of each phase of the transform pipeline that needs additional context, configuration
// and services.
type Dependencies struct {
	BuildManifestLoader build.ManifestLoader
}
