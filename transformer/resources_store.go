package transformer

import (
	"fmt"
	"maps"
	"sort"
	"strconv"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/cache"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// resourcesStoreResourceName is the fixed concrete name of the internal resources
// namespace config store (one per blueprint).
const resourcesStoreResourceName = "celerityResourcesConfigStore"

// storeBacking describes how to build the physical-id reference for a backing
// resource type the SDK resolves through the internal resources store.
type storeBacking struct {
	concreteName func(string) string
	// idAttribute is the concrete resource's physical-identifier output the SDK
	// addresses the resource by at runtime.
	idAttribute string
}

// storeBackings maps each celerity resource type registered in the store to its
// concrete resource name and physical-identifier attribute. queue, topic and
// datastore point at computed provider outputs (queueUrl, topicArn and the table
// arn — DynamoDB data-plane calls accept a table ARN wherever a table name is
// expected) so a name-less resource still stages: the reference resolves on
// deploy instead of failing with missing_resource_spec_property. bucket must stay
// on bucketName (S3 data-plane calls cannot address a bucket by its ARN), which
// the emit only sets when the abstract name is present — a name-less
// handler-linked bucket therefore cannot stage until the provider marks
// bucketName computed or the SDK accepts bucket ARNs.
//
// cache and sqlDatabase are absent from this map because one identifier does not
// describe a connection, they need a host, a port and a way to authenticate.
var storeBackings = map[string]storeBacking{
	"celerity/queue":     {queue.ConcreteResourceName, "queueUrl"},
	"celerity/topic":     {topic.ConcreteResourceName, "topicArn"},
	"celerity/datastore": {datastore.ConcreteResourceName, "arn"},
	"celerity/bucket":    {bucket.ConcreteResourceName, "bucketName"},
}

// collectResourcesStore builds the internal resources namespace config store — a
// single aws/ssm/parameterTree holding one parameter per backing resource that a
// handler links to, keyed by configKey and valued by the resource's physical-id
// reference. Returns nil when no handler links to a store-registered backing
// resource (no store is needed). The store carries no user config; it is derived
// entirely from handler backing links.
func collectResourcesStore(
	run *transformutils.Run,
	primaries []transformutils.ResolvedResource,
	storePath string,
) *transformutils.SharedParent {
	entries := map[string]*core.MappingNode{}

	// Iterate handlers in a stable order. The pipeline passes primaries in Go map
	// order, so without this the winner of a configKey collision — and hence the
	// emitted store — would be nondeterministic across runs.
	for _, handlerResource := range sortedHandlers(primaries) {
		addHandlerStoreEntries(entries, handlerResource)
	}

	if len(entries) == 0 {
		return nil
	}

	values := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	maps.Copy(values.Fields, entries)

	return &transformutils.SharedParent{
		Key:          "resources-store",
		ResourceName: resourcesStoreResourceName,
		ResourceType: "aws/ssm/parameterTree",
		Annotations: core.MappingNodeFields(
			transformutils.AnnotationSourceAbstractName, core.MappingNodeFromString(resourcesStoreResourceName),
			transformutils.AnnotationSourceAbstractType, core.MappingNodeFromString("celerity/config"),
			transformutils.AnnotationResourceCategory, core.MappingNodeFromString(transformutils.ResourceCategoryInfrastructure),
		),
		SeedSpec: resourcesStoreSeed(shared.ResolveAppName(run), storePath, values),
	}
}

// The internal resources store's seed spec with Celerity's identity attached.
//
// The store is app-level rather than emitted from any one abstract resource, so it
// names none: the app and the type are what is true of it. A parameter tree takes
// tags as a {key: value} map, applied to every parameter in the tree.
func resourcesStoreSeed(
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

// sortedHandlers returns the ResolvedHandler primaries in a stable order (by
// resource name) so store construction is deterministic regardless of the map
// iteration order the pipeline passes primaries in.
func sortedHandlers(primaries []transformutils.ResolvedResource) []*handler.ResolvedHandler {
	handlers := []*handler.ResolvedHandler{}
	for _, primary := range primaries {
		if handlerResource, ok := primary.(*handler.ResolvedHandler); ok {
			handlers = append(handlers, handlerResource)
		}
	}
	sort.Slice(handlers, func(i, j int) bool { return handlers[i].Name < handlers[j].Name })
	return handlers
}

// addHandlerStoreEntries adds a store entry (configKey -> physical-id reference)
// for each of the handler's store-backed links, skipping keys already present.
// A duplicate configKey means an invalid blueprint (the CLI enforces uniqueness
// with a fatal check before deploy); the first entry is kept, which is
// deterministic because the caller iterates handlers in sorted order.
func addHandlerStoreEntries(
	entries map[string]*core.MappingNode,
	handlerResource *handler.ResolvedHandler,
) {
	groups := []struct {
		celerityType string
		linked       []*types.LinkedResource
	}{
		{"celerity/queue", handlerResource.Queues},
		{"celerity/topic", handlerResource.Topics},
		{"celerity/datastore", handlerResource.Datastores},
		{"celerity/bucket", handlerResource.Buckets},
	}
	for _, group := range groups {
		backing := storeBackings[group.celerityType]
		for _, linked := range group.linked {
			configKey := storeConfigKey(linked)
			if _, seen := entries[configKey]; seen {
				continue
			}
			// A reference over concrete names + a fixed attribute is always well-formed.
			ref, _ := shared.SubstitutionMappingNode(fmt.Sprintf(
				"${resources.%s.spec.%s}", backing.concreteName(linked.Name), backing.idAttribute))
			entries[configKey] = ref
		}
	}

	addCacheStoreEntries(entries, handlerResource)
	addSQLDatabaseStoreEntries(entries, handlerResource)
}

// Cache store keys, as read by the SDKs. Named here so the set a
// test pins is the set the store writes.
const (
	cacheHostSuffix        = "_host"
	cachePortSuffix        = "_port"
	cacheAuthModeSuffix    = "_authMode"
	cacheTLSSuffix         = "_tls"
	cacheClusterModeSuffix = "_clusterMode"
	cacheUserSuffix        = "_user"
	// Store-neutral by name. The SDK treats the value as an opaque reference and
	// hands it to whichever secret store its platform resolves to. That the
	// reference is an ARN here is an AWS detail the SDK has no reason to know.
	cacheAuthTokenSecretIDSuffix = "_authTokenSecretId"
)

// SQL database store keys, as read by the SDKs.
const (
	sqlHostSuffix                = "_host"
	sqlReadHostSuffix            = "_readHost"
	sqlPortSuffix                = "_port"
	sqlDatabaseSuffix            = "_database"
	sqlUserSuffix                = "_user"
	sqlEngineSuffix              = "_engine"
	sqlAuthModeSuffix            = "_authMode"
	sqlSSLSuffix                 = "_ssl"
	sqlCredentialsSecretIDSuffix = "_credentialsSecretId"
)

// Writes the connection details a handler needs to
// reach a linked database.
//
// The credential is referenced, never copied. It is generated into Secrets
// Manager at deploy time and can rotate; a copy in the parameter store would
// keep working until the first rotation and then fail in a way that looks
// nothing like its cause.
//
// The user is the application's own role, not the master to limit privileges.
func addSQLDatabaseStoreEntries(
	entries map[string]*core.MappingNode,
	handlerResource *handler.ResolvedHandler,
) {
	for _, linked := range handlerResource.SQLDatabases {
		configKey := storeConfigKey(linked)
		if _, seen := entries[configKey+sqlHostSuffix]; seen {
			continue
		}

		// Endpoints are the values only known at deploy time, so they stay
		// references; everything else is settled at transform time and is
		// written as a literal string, because the parameter tree holds strings
		// only and a lone substitution keeps the type of what it points at.
		host, _ := shared.SubstitutionMappingNode(
			fmt.Sprintf("${values.%s}", sqldatabase.HostValueKey(linked.Name)))
		database, _ := shared.SubstitutionMappingNode(
			fmt.Sprintf("${values.%s}", sqldatabase.DatabaseNameValueKey(linked.Name)))

		entries[configKey+sqlHostSuffix] = host
		entries[configKey+sqlDatabaseSuffix] = database
		entries[configKey+sqlPortSuffix] = core.MappingNodeFromString(
			strconv.Itoa(sqldatabase.Port(linked.Resource)))
		entries[configKey+sqlEngineSuffix] = core.MappingNodeFromString(
			sqldatabase.Engine(linked.Resource))
		entries[configKey+sqlUserSuffix] = core.MappingNodeFromString(sqldatabase.AppUserName)
		// RDS terminates TLS on every endpoint, and the application has no
		// reason to opt out of it.
		entries[configKey+sqlSSLSuffix] = core.MappingNodeFromString("true")

		if sqldatabase.ReadReplicasEnabled(linked.Resource) {
			readHost, _ := shared.SubstitutionMappingNode(
				fmt.Sprintf("${values.%s}", sqldatabase.ReadHostValueKey(linked.Name)),
			)
			entries[configKey+sqlReadHostSuffix] = readHost
		}

		authMode := sqldatabase.AuthMode(linked.Resource)
		entries[configKey+sqlAuthModeSuffix] = core.MappingNodeFromString(authMode)

		if authMode != "iam" {
			// spec.id, not spec.arn: a Secrets Manager secret has no arn field,
			// and its Cloud Control primary identifier is the ARN.
			credentials, _ := shared.SubstitutionMappingNode(fmt.Sprintf(
				"${resources.%s.spec.id}", sqldatabase.AppUserSecretResourceName(linked.Name),
			))
			entries[configKey+sqlCredentialsSecretIDSuffix] = credentials
		}
	}
}

// Writes the connection details a handler needs to reach a
// linked cache.
//
// A cache takes several keys rather than the single identifier the other backing
// types need, and the endpoint is a derived blueprint value because a clustered
// cache is reached at its configuration endpoint and a non-clustered one at its
// primary endpoint.
//
// The AUTH token is referenced, not copied. It is generated into Secrets Manager
// at deploy time and can rotate; writing the value into the parameter store would
// leave a second copy that nothing updates, failing at some later rotation in a
// way that looks exactly like a misconfiguration.
func addCacheStoreEntries(
	entries map[string]*core.MappingNode,
	handlerResource *handler.ResolvedHandler,
) {
	for _, linked := range handlerResource.Caches {
		configKey := storeConfigKey(linked)
		if _, seen := entries[configKey+cacheHostSuffix]; seen {
			continue
		}

		// The endpoint is the one value known at deploy time, so it stays a
		// reference. The rest are settled at transform time and are written as
		// literal strings, a lone substitution keeps the type of what it points
		// at, and the parameter tree holds strings only.
		host, _ := shared.SubstitutionMappingNode(
			fmt.Sprintf("${values.%s}", cache.HostValueKey(linked.Name)),
		)

		entries[configKey+cacheHostSuffix] = host
		entries[configKey+cachePortSuffix] = core.MappingNodeFromString(
			strconv.Itoa(cache.Port()))
		entries[configKey+cacheTLSSuffix] = core.MappingNodeFromString(
			strconv.FormatBool(cache.TLSEnabled()))
		entries[configKey+cacheClusterModeSuffix] = core.MappingNodeFromString(
			strconv.FormatBool(cache.ClusterModeEnabled(linked.Resource)),
		)

		authMode := cache.AuthMode(linked.Resource)
		entries[configKey+cacheAuthModeSuffix] = core.MappingNodeFromString(authMode)

		if authMode == "password" {
			// spec.id, not spec.arn: a Secrets Manager secret has no arn field,
			// and its Cloud Control primary identifier is the ARN.
			authTokenSecretID, _ := shared.SubstitutionMappingNode(fmt.Sprintf(
				"${resources.%s.spec.id}", cache.AuthSecretResourceName(linked.Name)))
			entries[configKey+cacheAuthTokenSecretIDSuffix] = authTokenSecretID
		}

		if authMode == "iam" {
			// The RBAC user the handler authenticates as, which the SDK signs
			// its connection token for. Without it a cache configured for iam
			// deploys cleanly and then fails at cold start on a missing key,
			// which is the failure this whole store exists to avoid.
			//
			// The region the token is signed in is deliberately not written
			// here: it is settled at deploy rather than at transform, and the
			// execution environment already carries it. The SDK reads it from
			// there.
			user, _ := shared.SubstitutionMappingNode(fmt.Sprintf(
				"${resources.%s.spec.userId}", cache.IAMUserResourceName(linked.Name)))
			entries[configKey+cacheUserSuffix] = user
		}
	}
}

// storeConfigKey derives the store parameter name for a linked resource: its
// spec.name, falling back to its blueprint logical name. This mirrors the CLI's
// configKey derivation so the SDK's routing-file lookups resolve to these entries.
func storeConfigKey(linked *types.LinkedResource) string {
	if linked.Resource != nil {
		if node, ok := pluginutils.GetValueByPath("$.name", linked.Resource.Spec); ok {
			if name := core.StringValue(node); name != "" {
				return name
			}
		}
	}
	return linked.Name
}
