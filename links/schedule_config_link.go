package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

// ScheduleToConfigLink declares the celerity/schedule -> celerity/config
// relationship.
//
// On aws-serverless this link is a no-op: a schedule is absorbed into the handler
// it triggers, and configuration is delivered to that handler through its own
// handler -> config link. The abstract link fabricates no concrete resource; it
// exists so the relationship is recognised, documented and validated at the
// abstract layer.
func ScheduleToConfigLink() *transformerv1.AbstractLinkDefinition {
	return basicLink(
		"celerity/schedule", "celerity/config",
		"Associates configuration with a schedule's application.",
		"Associates a `celerity/config` with the application a `celerity/schedule` triggers. On "+
			"aws-serverless this is a no-op: the schedule is absorbed into its handler, which receives "+
			"configuration through its own `celerity/config` link.",
	)
}
