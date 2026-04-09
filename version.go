package main

// Version information set via ldflags at build time.
// For release-please/goreleaser builds, these are injected automatically.
// For local/development builds, the defaults are used.
//
// Build with custom version:
//
//	go build -ldflags "-X main.version=1.0.0 -X main.commit=abc123"
var (
	// version is the semantic version of the plugin.
	// Set at build time via: -ldflags "-X main.version=x.y.z"
	// Default is a semver-compliant development version.
	version = "0.0.0-dev"
)
