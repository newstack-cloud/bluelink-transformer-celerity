package transformer

import (
	"fmt"
	"maps"
	"sort"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
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
// bucketName computed or the SDK accepts bucket ARNs. cache and sqlDatabase are
// excluded — their connection details reach handlers via per-link env vars, not
// the store.
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
		SeedSpec: core.MappingNodeFields(
			"path", core.MappingNodeFromString(storePath),
			"values", values,
		),
	}
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
func addHandlerStoreEntries(entries map[string]*core.MappingNode, handlerResource *handler.ResolvedHandler) {
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
