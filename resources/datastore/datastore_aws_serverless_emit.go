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

	if nameNode := passthroughNameNode(mustGet("$.name", r)); nameNode != nil {
		spec.Fields["tableName"] = nameNode
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

	gsis, gsiDiagnostics := buildGlobalSecondaryIndexes(r, attrNames)
	if len(gsis) > 0 {
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

	// Deploy-config-sourced settings (per-datastore only, no global).
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
		Diagnostics: gsiDiagnostics,
	}, nil
}

// A DynamoDB index key schema is one HASH key and an optional RANGE key, so the
// abstract index must carry one or two fields. The schema enforces this, but an
// index whose cardinality is still wrong is reported and skipped rather than
// silently dropped or truncated to the first two fields.
func buildGlobalSecondaryIndexes(r *ResolvedDatastore, attrNames *orderedSet) ([]*core.MappingNode, []*core.Diagnostic) {
	indexes, _ := pluginutils.GetValueByPath("$.indexes", r.Resource.Spec)
	if indexes == nil {
		return nil, nil
	}

	keyTypes := []string{"HASH", "RANGE"}
	gsis := make([]*core.MappingNode, 0, len(indexes.Items))
	var diagnostics []*core.Diagnostic
	for _, index := range indexes.Items {
		name := core.StringValue(index.Fields["name"])
		fields := index.Fields["fields"]
		fieldCount := 0
		if fields != nil {
			fieldCount = len(fields.Items)
		}
		if fieldCount < 1 || fieldCount > len(keyTypes) {
			diagnostics = append(diagnostics, &core.Diagnostic{
				Level: core.DiagnosticLevelError,
				Message: fmt.Sprintf(
					"celerity/datastore %q index %q must cover one or two fields (a partition key and an "+
						"optional sort key) but has %d; the index has been skipped",
					r.Name, name, fieldCount,
				),
			})
			continue
		}

		gsiKeySchema := make([]*core.MappingNode, 0, fieldCount)
		for i, field := range fields.Items {
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
	return gsis, diagnostics
}

// Capacity units apply only under PROVISIONED, on-demand request-unit ceilings
// only under PAY_PER_REQUEST.
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
	} else {
		// DynamoDB's own API default is PROVISIONED, which requires explicit
		// capacity; a table emitted without billingMode and without
		// provisionedThroughput is rejected at deploy time ("Property
		// ProvisionedThroughput cannot be empty"). Celerity's default is
		// on-demand, so emit it explicitly.
		spec.Fields["billingMode"] = core.MappingNodeFromString("PAY_PER_REQUEST")
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
			// DynamoDB requires provisionedThroughput on every GSI under
			// PROVISIONED billing; the abstract index carries no capacity, so each
			// index inherits the table's.
			applyGSIProvisionedThroughput(spec, throughput)
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

func applyGSIProvisionedThroughput(spec *core.MappingNode, throughput *core.MappingNode) {
	gsis := spec.Fields["globalSecondaryIndexes"]
	if gsis == nil {
		return
	}
	for _, gsi := range gsis.Items {
		if gsi.Fields == nil {
			continue
		}
		gsi.Fields["provisionedThroughput"] = throughput
	}
}

func buildTimeToLive(r *ResolvedDatastore) *core.MappingNode {
	ttl, ok := pluginutils.GetValueByPath("$.timeToLive", r.Resource.Spec)
	if !ok || ttl == nil {
		return nil
	}

	// attributeName is required by DynamoDB whenever a TTL spec is present; the
	// schema requires fieldName, so a missing one means nothing to emit.
	fieldName := core.StringValue(ttl.Fields["fieldName"])
	if fieldName == "" {
		return nil
	}

	out := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	out.Fields["attributeName"] = core.MappingNodeFromString(fieldName)
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

// Returns the abstract name node for the concrete spec. A
// substitution-valued name (e.g. "${variables.namePrefix}-orders") is passed
// through as-is so the deploy engine resolves it, rather than being stringified
// to "" and silently dropped. Returns nil when no name is set so the physical
// table name auto-generates.
func passthroughNameNode(node *core.MappingNode) *core.MappingNode {
	if node == nil {
		return nil
	}
	if node.StringWithSubstitutions != nil {
		return node
	}
	if core.StringValue(node) == "" {
		return nil
	}
	return node
}

func datastoreConcreteName(name string) string {
	return fmt.Sprintf("%s_dynamodb_table", name)
}

// ConcreteResourceName is the concrete aws/dynamodb/table resource name emitted for
// the abstract datastore. Exported so the internal resources config store can
// reference it.
func ConcreteResourceName(name string) string {
	return datastoreConcreteName(name)
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
