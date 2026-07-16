package datastore

import (
	"context"
	"fmt"

	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// defaultAttributeType is the DynamoDB attribute type used for every key
// attribute. The abstract data store keys carry only field names (types live in
// the schema-management layer, which is not deployed to the concrete table), so
// key attributes are declared as String — the only type DynamoDB needs to build
// the key schema, and the correct choice for the overwhelming majority of keys.
const defaultAttributeType = "S"

// allAttributesProjection projects every item attribute into a secondary index.
// The abstract index carries no projection setting, so this is the default.
const allAttributesProjection = "ALL"

func emitDatastore(
	_ context.Context,
	run *transformutils.Run,
	r *ResolvedDatastore,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	if name := core.StringValue(mustGet("$.name", r)); name != "" {
		spec.Fields["tableName"] = core.MappingNodeFromString(name)
	}

	partitionKey := core.StringValue(mustGet("$.keys.partitionKey", r))
	sortKey := core.StringValue(mustGet("$.keys.sortKey", r))

	// attrNames accumulates every key attribute (table primary key + index keys),
	// order-preserved and deduplicated, so attributeDefinitions declares each once.
	attrNames := newOrderedSet()

	keySchema := []*core.MappingNode{keySchemaEntry(partitionKey, "HASH")}
	attrNames.add(partitionKey)
	if sortKey != "" {
		keySchema = append(keySchema, keySchemaEntry(sortKey, "RANGE"))
		attrNames.add(sortKey)
	}
	spec.Fields["keySchema"] = &core.MappingNode{Items: keySchema}

	if gsis := buildGlobalSecondaryIndexes(r, attrNames); len(gsis) > 0 {
		spec.Fields["globalSecondaryIndexes"] = &core.MappingNode{Items: gsis}
	}

	attrDefs := make([]*core.MappingNode, 0, len(attrNames.order))
	for _, name := range attrNames.order {
		attrDefs = append(attrDefs, attributeDefinition(name))
	}
	spec.Fields["attributeDefinitions"] = &core.MappingNode{Items: attrDefs}

	if ttl := buildTimeToLive(r); ttl != nil {
		spec.Fields["timeToLiveSpecification"] = ttl
	}

	// Deploy-config-sourced settings (per-datastore only, no global; §2.1).
	applyBillingConfig(spec, run, r)

	// aws/dynamodb/table.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFromResourceMetadata(r.Resource.Metadata); tags != nil {
		spec.Fields["tags"] = tags
	}

	// Rewrite any ${resources.x.spec.y} references a user embedded into concrete form.
	spec = subwalk.WalkMappingNode(spec, transformutils.RewriteResourcePropertyRefs(rw))

	res := &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/dynamodb/table"},
		Spec:         spec,
		Metadata:     datastoreMetadata(r),
		LinkSelector: r.Resource.LinkSelector,
	}

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			datastoreConcreteName(r.Name): res,
		},
	}, nil
}

func buildGlobalSecondaryIndexes(r *ResolvedDatastore, attrNames *orderedSet) []*core.MappingNode {
	indexes, _ := pluginutils.GetValueByPath("$.indexes", r.Resource.Spec)
	if indexes == nil {
		return nil
	}

	gsis := make([]*core.MappingNode, 0, len(indexes.Items))
	for _, index := range indexes.Items {
		name := core.StringValue(index.Fields["name"])
		fields := index.Fields["fields"]
		if fields == nil || len(fields.Items) == 0 {
			continue
		}

		keyTypes := []string{"HASH", "RANGE"}
		gsiKeySchema := make([]*core.MappingNode, 0, len(fields.Items))
		for i, field := range fields.Items {
			if i >= len(keyTypes) {
				break // DynamoDB indexes have at most a partition and a sort key.
			}
			fieldName := core.StringValue(field)
			gsiKeySchema = append(gsiKeySchema, keySchemaEntry(fieldName, keyTypes[i]))
			attrNames.add(fieldName)
		}

		gsis = append(gsis, core.MappingNodeFields(
			"indexName", core.MappingNodeFromString(name),
			"keySchema", &core.MappingNode{Items: gsiKeySchema},
			"projection", core.MappingNodeFields(
				"projectionType", core.MappingNodeFromString(allAttributesProjection),
			),
		))
	}
	return gsis
}

// applyBillingConfig maps the per-datastore aws.dynamodb.<datastore>.* deploy
// config onto the table's billingMode and throughput. Capacity units apply only
// under PROVISIONED, on-demand request-unit ceilings only under PAY_PER_REQUEST.
func applyBillingConfig(spec *core.MappingNode, run *transformutils.Run, r *ResolvedDatastore) {
	ctx := run.TransformContext
	name := core.StringValue(mustGet("$.name", r))
	if name == "" {
		name = r.Name
	}

	billingMode := ""
	if v, ok := sharedaws.ResolveDeployConfig(ctx, "aws.dynamodb", name, "billingMode"); ok {
		spec.Fields["billingMode"] = &core.MappingNode{Scalar: v}
		if v.StringValue != nil {
			billingMode = *v.StringValue
		}
	}

	if billingMode == "PROVISIONED" {
		throughput := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
		if v, ok := sharedaws.ResolveDeployConfigNode(ctx, "aws.dynamodb", name, "readCapacityUnits"); ok {
			throughput.Fields["readCapacityUnits"] = v
		}
		if v, ok := sharedaws.ResolveDeployConfigNode(ctx, "aws.dynamodb", name, "writeCapacityUnits"); ok {
			throughput.Fields["writeCapacityUnits"] = v
		}
		if len(throughput.Fields) > 0 {
			spec.Fields["provisionedThroughput"] = throughput
		}
		return
	}

	// PAY_PER_REQUEST (explicit or the default): optional on-demand ceilings.
	onDemand := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	if v, ok := sharedaws.ResolveDeployConfigNode(ctx, "aws.dynamodb", name, "maxReadRequestUnits"); ok {
		onDemand.Fields["maxReadRequestUnits"] = v
	}
	if v, ok := sharedaws.ResolveDeployConfigNode(ctx, "aws.dynamodb", name, "maxWriteRequestUnits"); ok {
		onDemand.Fields["maxWriteRequestUnits"] = v
	}
	if len(onDemand.Fields) > 0 {
		spec.Fields["onDemandThroughput"] = onDemand
	}
}

func buildTimeToLive(r *ResolvedDatastore) *core.MappingNode {
	ttl, ok := pluginutils.GetValueByPath("$.timeToLive", r.Resource.Spec)
	if !ok || ttl == nil {
		return nil
	}

	out := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	if fieldName := core.StringValue(ttl.Fields["fieldName"]); fieldName != "" {
		out.Fields["attributeName"] = core.MappingNodeFromString(fieldName)
	}
	// enabled is required on the concrete timeToLiveSpecification; default to
	// false when the field is absent.
	enabled := ttl.Fields["enabled"]
	if enabled == nil {
		enabled = core.MappingNodeFromBool(false)
	}
	out.Fields["enabled"] = enabled
	return out
}

func datastoreMetadata(r *ResolvedDatastore) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/datastore",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
	if r.Resource.Metadata != nil {
		meta.Labels = r.Resource.Metadata.Labels
	}
	return meta
}

func keySchemaEntry(attributeName, keyType string) *core.MappingNode {
	return core.MappingNodeFields(
		"attributeName", core.MappingNodeFromString(attributeName),
		"keyType", core.MappingNodeFromString(keyType),
	)
}

func attributeDefinition(attributeName string) *core.MappingNode {
	return core.MappingNodeFields(
		"attributeName", core.MappingNodeFromString(attributeName),
		"attributeType", core.MappingNodeFromString(defaultAttributeType),
	)
}

func mustGet(path string, r *ResolvedDatastore) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, r.Resource.Spec)
	return node
}

func datastoreConcreteName(name string) string {
	return fmt.Sprintf("%s_dynamodb_table", name)
}

// orderedSet is a small insertion-ordered string set used to collect key
// attribute names without duplicates while preserving first-seen order.
type orderedSet struct {
	seen  map[string]struct{}
	order []string
}

func newOrderedSet() *orderedSet {
	return &orderedSet{seen: map[string]struct{}{}}
}

func (s *orderedSet) add(v string) {
	if v == "" {
		return
	}
	if _, ok := s.seen[v]; ok {
		return
	}
	s.seen[v] = struct{}{}
	s.order = append(s.order, v)
}
