package handlerawsserverless

import (
	"slices"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
)

// Captures the handler's link set. Provider links inject their
// own IAM grants into whichever role a function references, so a role may only
// be shared between handlers with identical link sets, otherwise each would
// inherit the other's permissions.
// RolePlanFor fingerprints a handler's link set, so that handlers with
// identical links can share one execution role.
func RolePlanFor(r *handler.ResolvedHandler) awslambda.RolePlan {
	links := []string{}
	add := func(linkType string, linked []*types.LinkedResource) {
		for _, resource := range linked {
			links = append(links, linkType+"::"+resource.Name)
		}
	}

	// Outbound links from the handler.
	add("celerity/queue", r.Queues)
	add("celerity/topic", r.Topics)
	add("celerity/datastore", r.Datastores)
	add("celerity/bucket", r.Buckets)
	add("celerity/cache", r.Caches)
	add("celerity/config", r.Configs)
	add("celerity/sqlDatabase", r.SQLDatabases)

	// Inbound event-source links absorbed by the handler.
	add("celerity/consumer", r.Consumers)
	add("celerity/schedule", r.Schedules)
	if r.APILink != nil {
		links = append(links, "celerity/api::"+r.APILink.Name)
	}

	// Sorted so the fingerprint is order-independent.
	slices.Sort(links)

	subnetType := ""
	if r.VPC != nil {
		subnetType = r.VPCSubnetType
	}

	return awslambda.RolePlan{
		Links:                  links,
		Tracing:                r.TracingEnabled,
		VPC:                    subnetType,
		ResourceLinksStorePath: r.ResourceLinksStorePath,
		// External event sources (standalone ESMs) have no provider link to inject
		// source-read IAM, so the seed grants it. Folding them into the plan also
		// keeps the fingerprint (hence role sharing) sensitive to them.
		ExternalSources: externalRoleSources(r),
		// An external resource the handler uses, as opposed to one that invokes
		// it. Neither gets IAM from a provider link, for the same reason.
		ExternalTargets: externalRoleTargets(r),
	}
}
