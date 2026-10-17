package transformer

import (
	"sort"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// collectResourcesStore builds the internal resources namespace config store,
// holding one entry per backing resource that a handler links to, keyed by
// configKey. Returns nil when no handler links to a store-registered backing
// resource, since no store is then needed. The store carries no user config; it
// is derived entirely from handler backing links.
//
// Everything here is portable: which links a handler has, the order they are
// traversed in, and the configKey each gets. What is written against a key is
// the target's, through shared.ResourcesStoreTarget.
func collectResourcesStore(
	target shared.ResourcesStoreTarget,
	run *transformutils.Run,
	primaries []transformutils.ResolvedResource,
	storePath string,
) *transformutils.SharedParent {
	entries := shared.NewResourcesStoreEntries()

	// Iterate handlers in a stable order to make output determinstic
	// between runs.
	for _, handlerResource := range sortedHandlers(primaries) {
		addHandlerStoreEntries(entries, target, handlerResource)
	}

	return entries.SharedParent(target, shared.ResolveAppName(run), storePath)
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

// Every store-backed link a handler has. The single-identifier backings come
// first and in a fixed order, so a duplicate configKey resolves the same way on
// every run.
//
// This must stay in step with what each resource contributes: a handler left out
// here but written into the store gets no namespace and fails at cold start on
// `Config namespace "resources" not registered`, which is how every cache-backed
// handler was once broken.
func addHandlerStoreEntries(
	entries *shared.ResourcesStoreEntries,
	target shared.ResourcesStoreTarget,
	handlerResource *handler.ResolvedHandler,
) {
	backings := []struct {
		celerityType string
		linked       []*types.LinkedResource
	}{
		{"celerity/queue", handlerResource.Queues},
		{"celerity/topic", handlerResource.Topics},
		{"celerity/datastore", handlerResource.Datastores},
		{"celerity/bucket", handlerResource.Buckets},
	}
	for _, backing := range backings {
		for _, linked := range backing.linked {
			identifier, registered := target.BackingIdentifier(
				backing.celerityType, linked.Name, linked.Resource,
			)
			if !registered {
				continue
			}
			entries.AddBackingIdentifier(storeConfigKey(linked), identifier)
		}
	}

	for _, linked := range handlerResource.Caches {
		if connection, registered := target.CacheConnection(
			linked.Name, linked.Resource,
		); registered {
			entries.AddCacheConnection(storeConfigKey(linked), connection)
		}
	}

	for _, linked := range handlerResource.SQLDatabases {
		if connection, registered := target.DatabaseConnection(
			linked.Name, linked.Resource,
		); registered {
			entries.AddDatabaseConnection(storeConfigKey(linked), connection)
		}
	}
}

func storeConfigKey(linked *types.LinkedResource) string {
	return shared.StoreConfigKey(linked.Name, linked.Resource)
}
