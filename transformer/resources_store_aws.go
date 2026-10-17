package transformer

import (
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket/bucketaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/cache/cacheaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore/datastoreaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue/queueaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase/sqldatabaseaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic/topicaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// awsResourcesStore is the AWS realisation of the internal resources store. The
// key vocabulary and the traversal are portable (see
// shared.ResourcesStoreTarget); everything here is what AWS writes against them.
//
// Shared by every AWS deploy target, not just aws-serverless: the store is an
// SSM parameter tree whichever compute model reads it, and every value it writes
// comes from a {resource}aws package. A containerised target registers this same
// realisation from its own aggregator.
type awsResourcesStore struct{}

func (awsResourcesStore) StoreResourceType() string {
	return "aws/ssm/parameterTree"
}

// The store is app-level rather than emitted from any one abstract resource, so
// it names none: the app and the type are what is true of it. A parameter tree
// takes tags as a {key: value} map, applied to every parameter in the tree.
func (awsResourcesStore) StoreSeedSpec(
	appName string,
	storePath string,
	values *core.MappingNode,
) *core.MappingNode {
	seed := core.MappingNodeFields(
		"path", core.MappingNodeFromString(storePath),
		"values", values,
	)

	if tags := sharedaws.MapTagsFor(nil, sharedaws.Identity{
		AppName:      appName,
		ResourceType: "celerity/config",
	}); tags != nil {
		seed.Fields["tags"] = tags
	}

	return seed
}

// storeBacking is the concrete resource and attribute a managed backing resource
// is addressed by.
type storeBacking struct {
	concreteName func(string) string
	// idAttribute is the concrete resource's physical-identifier output the SDK
	// addresses the resource by at runtime.
	idAttribute string
	// externalID derives what the SDKs address an external resource by, which is
	// not always its ARN: a queue is addressed by URL and a bucket by name.
	externalID func(*awsexternal.Identifier) string
}

// cache and sqlDatabase are absent because one identifier does not describe a
// connection, they need a host, a port and a way to authenticate.
var awsStoreBackings = map[string]storeBacking{
	"celerity/queue": {
		queueaws.ConcreteResourceName,
		"queueUrl",
		(*awsexternal.Identifier).QueueURL,
	},
	"celerity/topic": {
		topicaws.ConcreteResourceName,
		"topicArn",
		func(i *awsexternal.Identifier) string { return i.Literal },
	},
	"celerity/datastore": {
		datastoreaws.ConcreteResourceName,
		"arn",
		func(i *awsexternal.Identifier) string { return i.Literal },
	},
	"celerity/bucket": {
		bucketaws.ConcreteResourceName,
		"bucketName",
		(*awsexternal.Identifier).ResourceName,
	},
}

// An external backing resource has no concrete counterpart, so the store carries
// the identifier the author declared rather than a reference to a resource that
// was never emitted.
//
// A resource declaring external mode whose identifier could not be resolved is
// treated as managed here: the emit for that resource reports the reason, and
// writing nothing keeps one error rather than two.
func (awsResourcesStore) BackingIdentifier(
	celerityType string,
	abstractName string,
	resource *schema.Resource,
) (shared.BackingIdentifier, bool) {
	backing, registered := awsStoreBackings[celerityType]
	if !registered {
		return shared.BackingIdentifier{}, false
	}

	if external, diagnostics := shared.ResolvedExternal(
		celerityType, abstractName, resource,
	); external != nil && len(diagnostics) == 0 {
		if identifier := awsexternal.ParseOrNil(external); identifier != nil {
			return shared.BackingIdentifier{
				Value:  core.MappingNodeFromString(backing.externalID(identifier)),
				Region: identifier.Region,
			}, true
		}
	}

	// A reference over concrete names + a fixed attribute is always well-formed.
	ref, _ := shared.SubstitutionMappingNode(fmt.Sprintf(
		"${resources.%s.spec.%s}", backing.concreteName(abstractName), backing.idAttribute),
	)

	return shared.BackingIdentifier{Value: ref}, true
}

// The endpoint is the one value known only at deploy time, so it stays a
// reference. The rest are settled at transform time and are written as literals,
// because a lone substitution keeps the type of what it points at and the
// parameter tree holds strings only.
func (awsResourcesStore) CacheConnection(
	abstractName string,
	resource *schema.Resource,
) (shared.CacheConnection, bool) {
	host, _ := shared.SubstitutionMappingNode(
		fmt.Sprintf("${values.%s}", cacheaws.HostValueKey(abstractName)),
	)

	connection := shared.CacheConnection{
		Host:        host,
		Port:        cacheaws.Port(),
		TLS:         cacheaws.TLSEnabled(),
		ClusterMode: cacheaws.ClusterModeEnabled(resource),
		AuthMode:    cacheaws.AuthMode(resource),
	}

	switch connection.AuthMode {
	case "password":
		// spec.id, not spec.arn: a Secrets Manager secret has no arn field, and
		// its Cloud Control primary identifier is the ARN.
		connection.AuthTokenSecretID, _ = shared.SubstitutionMappingNode(
			fmt.Sprintf(
				"${resources.%s.spec.id}", cacheaws.AuthSecretResourceName(abstractName),
			),
		)
	case "iam":
		// The RBAC user the handler authenticates as, which the SDK signs its
		// connection token for. Without it a cache configured for iam deploys
		// cleanly and then fails at cold start on a missing key, which is the
		// failure this whole store exists to avoid.
		//
		// The region the token is signed in is deliberately not written: it is
		// settled at deploy rather than at transform, and the execution
		// environment already carries it. The SDK reads it from there.
		connection.User, _ = shared.SubstitutionMappingNode(
			fmt.Sprintf(
				"${resources.%s.spec.userId}", cacheaws.IAMUserResourceName(abstractName),
			),
		)
	}

	return connection, true
}

// Endpoints are the values known only at deploy time, so they stay references;
// everything else is settled at transform time and written as a literal.
func (awsResourcesStore) DatabaseConnection(
	abstractName string,
	resource *schema.Resource,
) (shared.DatabaseConnection, bool) {
	host, _ := shared.SubstitutionMappingNode(
		fmt.Sprintf("${values.%s}", sqldatabaseaws.HostValueKey(abstractName)))
	database, _ := shared.SubstitutionMappingNode(
		fmt.Sprintf("${values.%s}", sqldatabaseaws.DatabaseNameValueKey(abstractName)))

	connection := shared.DatabaseConnection{
		Host:     host,
		Database: database,
		Port:     sqldatabase.Port(resource),
		Engine:   sqldatabase.Engine(resource),
		User:     sqldatabase.AppUserName,
		// RDS terminates TLS on every endpoint, and the application has no
		// reason to opt out of it.
		SSL:      true,
		AuthMode: sqldatabase.AuthMode(resource),
	}

	if sqldatabase.ReadReplicasEnabled(resource) {
		connection.ReadHost, _ = shared.SubstitutionMappingNode(
			fmt.Sprintf("${values.%s}", sqldatabaseaws.ReadHostValueKey(abstractName)))
	}

	if connection.AuthMode != "iam" {
		// spec.id, not spec.arn: a Secrets Manager secret has no arn field, and
		// its Cloud Control primary identifier is the ARN.
		connection.CredentialsSecretID, _ = shared.SubstitutionMappingNode(fmt.Sprintf(
			"${resources.%s.spec.id}", sqldatabaseaws.AppUserSecretResourceName(abstractName)))
	}

	return connection, true
}
