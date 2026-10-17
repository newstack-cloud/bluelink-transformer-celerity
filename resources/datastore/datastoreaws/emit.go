package datastoreaws

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// defaultAttributeType is the DynamoDB attribute type used for every key
// attribute. The abstract data store keys carry only field names (types live in
// the schema-management layer, which is not deployed to the concrete table), so
// key attributes are declared as String, the only type DynamoDB needs to build
// the key schema, and the correct choice for the majority of keys.
const defaultAttributeType = "S"

// allAttributesProjection projects every item attribute into a secondary index.
// The abstract index carries no projection setting, so this is the default.
const allAttributesProjection = "ALL"

const streamViewTypeNewAndOldImages = "NEW_AND_OLD_IMAGES"

// Emit builds the DynamoDB table a celerity/datastore maps to, for the named
// AWS deploy target.
func Emit(
	ctx context.Context,
	run *transformutils.Run,
	target string,
	r *datastore.ResolvedDatastore,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	// The key schema and the attribute definitions built from it are part of the
	// table's shape, not values passed through to it.
	structural := append(
		[]shared.StructuralField{
			{Name: "keys.partitionKey", Value: mustGet("$.keys.partitionKey", r)},
			{Name: "keys.sortKey", Value: mustGet("$.keys.sortKey", r)},
		},
		shared.ExternalModeStructuralFields(r.Resource)...,
	)
	if diagnostics := shared.DeferredValueErrors(
		target,
		"datastore", r.Name, r.Resource,
		structural...,
	); len(diagnostics) > 0 {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	// An external data store does not emit anything, it names a DynamoDB table Celerity
	// does not own. Handlers still reach it, through the resources store and a
	// grant on their execution role.
	//
	// Verified through the key model rather than through the generic identifier
	// check: the declared keys have to match the live table because the SDK's
	// condition builder is written against them, and reading the key model also
	// answers whether the table exists, so one lookup replaces both.
	if identifier, external, diagnostics, stop := awsexternal.Resolve(
		"celerity/datastore", r.Name, r.Resource,
	); stop {
		if identifier != nil {
			runCtx, _ := transformutils.Use[*shared.RunContext](run)
			diagnostics = append(diagnostics, shared.VerifyExternalKeyModel(
				ctx, runCtx, "celerity/datastore", r.Name, r.Resource, external,
				core.StringValue(mustGet("$.keys.partitionKey", r)),
				core.StringValue(mustGet("$.keys.sortKey", r)),
			)...)
		}

		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	spec, gsiDiagnostics := buildTableSpec(run, r)

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

// The table's own shape, before any reference in it is rewritten. Keys come from
// the abstract spec, capacity from deploy config, and the stream is turned on
// only when something consumes it.
func buildTableSpec(
	run *transformutils.Run,
	r *datastore.ResolvedDatastore,
) (*core.MappingNode, []*core.Diagnostic) {
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

	if r.Consumed {
		spec.Fields["streamSpecification"] = buildStreamSpecification()
	}

	// Deploy-config-sourced settings (per-datastore only, no global).
	applyBillingConfig(spec, run, r)

	// aws/dynamodb/table.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      shared.ResolveAppName(run),
		ResourceName: r.Name,
		ResourceType: "celerity/datastore",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	return spec, gsiDiagnostics
}

// A DynamoDB index key schema is one HASH key and an optional RANGE key, so the
// abstract index must carry one or two fields. The schema enforces this, but an
// index whose cardinality is still wrong is reported and skipped rather than
// silently dropped or truncated to the first two fields.
func buildGlobalSecondaryIndexes(r *datastore.ResolvedDatastore, attrNames *orderedSet) ([]*core.MappingNode, []*core.Diagnostic) {
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
				Range: shared.DiagnosticRange(
					shared.NodeMeta(index),
					shared.ResourceMeta(r.Resource),
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
func applyBillingConfig(spec *core.MappingNode, run *transformutils.Run, r *datastore.ResolvedDatastore) {
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

// The stream a consumer reads, enabled on the table itself.
//
// The block's presence is what turns streams on; the table schema has no separate
// enabled flag, only the view type it requires. NEW_AND_OLD_IMAGES matches the default
// the AWS provider's table-to-function link applies, so a table created here and one
// that link configures agree rather than fighting over the view type.
func buildStreamSpecification() *core.MappingNode {
	return core.MappingNodeFields(
		"streamViewType", core.MappingNodeFromString(streamViewTypeNewAndOldImages),
	)
}

func buildTimeToLive(r *datastore.ResolvedDatastore) *core.MappingNode {
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

func datastoreMetadata(r *datastore.ResolvedDatastore) *schema.Metadata {
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

func mustGet(path string, r *datastore.ResolvedDatastore) *core.MappingNode {
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
