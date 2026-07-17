package shared

import "github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"

// Dependencies defines the dependencies that are passed through in the construction
// of each phase of the transform pipeline that needs additional context, configuration
// and services.
type Dependencies struct {
	BuildManifestLoader build.ManifestLoader
}

// BuildManifestLoadError carries the cause of a non-fatal build manifest load
// failure through the run so downstream consumers (e.g. handler emit) can
// surface it instead of the failure being fully discarded.
type BuildManifestLoadError struct {
	Cause error
}
