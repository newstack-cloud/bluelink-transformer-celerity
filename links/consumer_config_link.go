package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

// ConsumerToConfigLink declares the celerity/consumer -> celerity/config
// relationship.
//
// On aws-serverless this link is a no-op: a consumer is absorbed into the handler
// it delivers events to, and configuration is delivered to that handler through
// its own handler -> config link. The abstract link fabricates no concrete
// resource; it exists so the relationship is recognised, documented and validated
// at the abstract layer.
func ConsumerToConfigLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/consumer", "celerity/config",
		"Associates configuration with a consumer's application.",
		"Associates a `celerity/config` with the application a `celerity/consumer` runs in. On "+
			"aws-serverless this is a no-op: the consumer is absorbed into its handler, which receives "+
			"configuration through its own `celerity/config` link.",
	)
}
