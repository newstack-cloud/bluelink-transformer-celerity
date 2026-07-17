package transformer

import (
	"fmt"

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
// concrete resource name and physical-identifier attribute (verified against the
// provider's primary identifiers). cache and sqlDatabase are excluded — their
// connection details reach handlers via per-link env vars, not the store.
var storeBackings = map[string]storeBacking{
	"celerity/queue":     {queue.ConcreteResourceName, "queueUrl"},
	"celerity/topic":     {topic.ConcreteResourceName, "topicArn"},
	"celerity/datastore": {datastore.ConcreteResourceName, "tableName"},
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
) (*transformutils.SharedParent, error) {
	entries := map[string]*core.MappingNode{}

	for _, primary := range primaries {
		handlerResource, ok := primary.(*handler.ResolvedHandler)
		if !ok {
			continue
		}
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
				ref, err := shared.SubstitutionMappingNode(fmt.Sprintf(
					"${resources.%s.spec.%s}", backing.concreteName(linked.Name), backing.idAttribute))
				if err != nil {
					return nil, err
				}
				entries[configKey] = ref
			}
		}
	}

	if len(entries) == 0 {
		return nil, nil
	}

	values := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	for _, key := range shared.SortedKeys(entries) {
		values.Fields[key] = entries[key]
	}

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
	}, nil
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
