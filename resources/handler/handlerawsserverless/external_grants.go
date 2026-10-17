package handlerawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
)

// The AWS service each backing resource type is reached through, and so which
// action set an external one is granted.
//
// celerity/config is absent: which service backs a config store depends on
// whether any of its values are plaintext, so the service is derived from the
// identifier rather than from the resource type.
var externalGrantServices = map[string]string{
	"celerity/bucket":    awslambda.ExternalTargetServiceS3,
	"celerity/datastore": awslambda.ExternalTargetServiceDynamoDB,
	"celerity/queue":     awslambda.ExternalTargetServiceSQS,
	"celerity/topic":     awslambda.ExternalTargetServiceSNS,
}

// externalRoleTargets collects every out-of-blueprint resource the handler links
// to, so the seeded execution role grants access to each.
//
// Without this a handler resolves an external resource's address through the
// resources store and is then refused at the first call, because the provider
// link that would have granted it needs a concrete resource in the blueprint to
// link to and an external resource has none.
func externalRoleTargets(r *handler.ResolvedHandler) []awslambda.ExternalGrant {
	grants := []awslambda.ExternalGrant{}

	links := []struct {
		celerityType string
		linked       []*types.LinkedResource
	}{
		{"celerity/queue", r.Queues},
		{"celerity/topic", r.Topics},
		{"celerity/datastore", r.Datastores},
		{"celerity/bucket", r.Buckets},
		{"celerity/config", r.Configs},
	}
	for _, link := range links {
		for _, linked := range link.linked {
			if grant, external := externalGrantFor(link.celerityType, linked); external {
				grants = append(grants, grant)
			}
		}
	}

	awslambda.SortExternalGrants(grants)

	return grants
}

// A resource declaring external mode whose identifier cannot be read grants
// nothing, the emit for that resource reports why, and seeding a statement
// against an unparsable ARN would add a second, less useful failure.
func externalGrantFor(
	celerityType string,
	linked *types.LinkedResource,
) (awslambda.ExternalGrant, bool) {
	external, diagnostics := shared.ResolvedExternal(celerityType, linked.Name, linked.Resource)
	if external == nil || len(diagnostics) > 0 {
		return awslambda.ExternalGrant{}, false
	}

	identifier := awsexternal.ParseOrNil(external)
	if identifier == nil {
		return awslambda.ExternalGrant{}, false
	}

	service, known := externalGrantServices[celerityType]
	if !known {
		if service = configGrantService(identifier); service == "" {
			return awslambda.ExternalGrant{}, false
		}
	}

	return awslambda.ExternalGrant{Service: service, ARN: identifier.Literal}, true
}

// A config store is a parameter tree or a single secret, and the identifier is
// what says which.
func configGrantService(identifier *awsexternal.Identifier) string {
	switch identifier.ARN.Service {
	case "ssm":
		return awslambda.ExternalTargetServiceSSM
	case "secretsmanager":
		return awslambda.ExternalTargetServiceSecret
	default:
		return ""
	}
}
