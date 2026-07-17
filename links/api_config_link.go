package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

// APIToConfigLink declares the celerity/api -> celerity/config relationship.
//
// On aws-serverless this link is a no-op: an API Gateway hosts no configuration
// of its own, and secrets/configuration are made available to the application's
// handlers by the handler -> config link (each handler links its own config). The
// abstract link fabricates no concrete config resource; it exists so the
// relationship is recognised, documented and validated at the abstract layer.
func APIToConfigLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/api", "celerity/config",
		"Associates configuration with an API's application.",
		"Associates a `celerity/config` with the application served by a `celerity/api`. On "+
			"aws-serverless this is a no-op: configuration is delivered to the application's handlers "+
			"through their own `celerity/config` links, as an API Gateway hosts no configuration itself.",
	)
}
