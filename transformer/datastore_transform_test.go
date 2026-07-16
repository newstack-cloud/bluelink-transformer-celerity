//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type DatastoreTransformTestSuite struct {
	suite.Suite
}

func TestDatastoreTransformTestSuite(t *testing.T) {
	suite.Run(t, new(DatastoreTransformTestSuite))
}

func (s *DatastoreTransformTestSuite) Test_emits_a_dynamodb_table_with_key_schema_and_attributes() {
	ds := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"keys", core.MappingNodeFields(
				"partitionKey", core.MappingNodeFromString("id"),
				"sortKey", core.MappingNodeFromString("createdAt"),
			),
			"timeToLive", core.MappingNodeFields(
				"fieldName", core.MappingNodeFromString("ttl"),
				"enabled", core.MappingNodeFromBool(true),
			),
		),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"team": "payments"}},
		},
	}

	table := s.transformDatastore(map[string]*schema.Resource{"myStore": ds})["myStore_dynamodb_table"]
	s.Require().NotNil(table)
	s.Equal("aws/dynamodb/table", table.Type.Value)
	s.Equal("orders", core.StringValue(table.Spec.Fields["tableName"]))

	// keySchema: partition -> HASH, sort -> RANGE.
	ks := table.Spec.Fields["keySchema"].Items
	s.Require().Len(ks, 2)
	s.Equal("id", core.StringValue(ks[0].Fields["attributeName"]))
	s.Equal("HASH", core.StringValue(ks[0].Fields["keyType"]))
	s.Equal("createdAt", core.StringValue(ks[1].Fields["attributeName"]))
	s.Equal("RANGE", core.StringValue(ks[1].Fields["keyType"]))

	// attributeDefinitions declares each key attribute once, defaulting to String.
	ad := table.Spec.Fields["attributeDefinitions"].Items
	s.Require().Len(ad, 2)
	s.Equal("id", core.StringValue(ad[0].Fields["attributeName"]))
	s.Equal("S", core.StringValue(ad[0].Fields["attributeType"]))

	// timeToLiveSpecification maps fieldName -> attributeName.
	ttl := table.Spec.Fields["timeToLiveSpecification"]
	s.Equal("ttl", core.StringValue(ttl.Fields["attributeName"]))
	s.True(core.BoolValue(ttl.Fields["enabled"]))

	// No stream specification is emitted (the consumer link enables streams).
	s.Nil(table.Spec.Fields["streamSpecification"])

	s.Equal("payments", table.Metadata.Labels.Values["team"])
	s.Equal("infrastructure", annotationLiteral(table.Metadata.Annotations, transformutils.AnnotationResourceCategory))
}

func (s *DatastoreTransformTestSuite) Test_index_fields_become_a_gsi_and_extra_attribute_definitions() {
	ds := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("orders"),
			"keys", core.MappingNodeFields(
				"partitionKey", core.MappingNodeFromString("id"),
			),
			"indexes", &core.MappingNode{
				Items: []*core.MappingNode{
					core.MappingNodeFields(
						"name", core.MappingNodeFromString("byCustomer"),
						"fields", &core.MappingNode{
							Items: []*core.MappingNode{
								core.MappingNodeFromString("customerId"),
								core.MappingNodeFromString("createdAt"),
							},
						},
					),
				},
			},
		),
	}

	table := s.transformDatastore(map[string]*schema.Resource{"myStore": ds})["myStore_dynamodb_table"]
	s.Require().NotNil(table)

	gsis := table.Spec.Fields["globalSecondaryIndexes"].Items
	s.Require().Len(gsis, 1)
	s.Equal("byCustomer", core.StringValue(gsis[0].Fields["indexName"]))
	gsiKeys := gsis[0].Fields["keySchema"].Items
	s.Require().Len(gsiKeys, 2)
	s.Equal("customerId", core.StringValue(gsiKeys[0].Fields["attributeName"]))
	s.Equal("HASH", core.StringValue(gsiKeys[0].Fields["keyType"]))
	s.Equal("createdAt", core.StringValue(gsiKeys[1].Fields["attributeName"]))
	s.Equal("RANGE", core.StringValue(gsiKeys[1].Fields["keyType"]))
	s.Equal("ALL", core.StringValue(gsis[0].Fields["projection"].Fields["projectionType"]))

	// attributeDefinitions covers the table key plus both index fields, once each.
	ad := table.Spec.Fields["attributeDefinitions"].Items
	names := map[string]bool{}
	for _, a := range ad {
		names[core.StringValue(a.Fields["attributeName"])] = true
	}
	s.Len(ad, 3)
	s.True(names["id"] && names["customerId"] && names["createdAt"])
}

func (s *DatastoreTransformTestSuite) Test_provisioned_billing_config_sets_capacity() {
	ds := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"keys", core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
		),
	}
	// Datastore keys are per-resource only, keyed by the logical name "orders".
	ctx := deployConfigContext(map[string]*core.ScalarValue{
		"aws.dynamodb.orders.billingMode":        core.ScalarFromString("PROVISIONED"),
		"aws.dynamodb.orders.readCapacityUnits":  core.ScalarFromInt(5),
		"aws.dynamodb.orders.writeCapacityUnits": core.ScalarFromInt(3),
	})

	table := s.transformWith(map[string]*schema.Resource{"orders": ds}, ctx)["orders_dynamodb_table"]
	s.Require().NotNil(table)
	s.Equal("PROVISIONED", core.StringValue(table.Spec.Fields["billingMode"]))
	pt := table.Spec.Fields["provisionedThroughput"]
	s.Require().NotNil(pt)
	s.Equal(5, core.IntValue(pt.Fields["readCapacityUnits"]))
	s.Equal(3, core.IntValue(pt.Fields["writeCapacityUnits"]))
	// Capacity units are provisioned-only; no on-demand ceilings.
	s.Nil(table.Spec.Fields["onDemandThroughput"])
}

// Under PROVISIONED billing DynamoDB requires provisionedThroughput on every
// GSI, so each emitted index inherits the table's capacity.
func (s *DatastoreTransformTestSuite) Test_provisioned_billing_applies_throughput_to_gsis() {
	ds := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"keys", core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
			"indexes", &core.MappingNode{
				Items: []*core.MappingNode{
					core.MappingNodeFields(
						"name", core.MappingNodeFromString("byCustomer"),
						"fields", &core.MappingNode{
							Items: []*core.MappingNode{core.MappingNodeFromString("customerId")},
						},
					),
				},
			},
		),
	}
	ctx := deployConfigContext(map[string]*core.ScalarValue{
		"aws.dynamodb.orders.billingMode":        core.ScalarFromString("PROVISIONED"),
		"aws.dynamodb.orders.readCapacityUnits":  core.ScalarFromInt(5),
		"aws.dynamodb.orders.writeCapacityUnits": core.ScalarFromInt(3),
	})

	table := s.transformWith(map[string]*schema.Resource{"orders": ds}, ctx)["orders_dynamodb_table"]
	s.Require().NotNil(table)
	gsis := table.Spec.Fields["globalSecondaryIndexes"].Items
	s.Require().Len(gsis, 1)
	gsiPT := gsis[0].Fields["provisionedThroughput"]
	s.Require().NotNil(gsiPT, "PROVISIONED GSIs must carry provisionedThroughput")
	s.Equal(5, core.IntValue(gsiPT.Fields["readCapacityUnits"]))
	s.Equal(3, core.IntValue(gsiPT.Fields["writeCapacityUnits"]))
}

func (s *DatastoreTransformTestSuite) Test_pay_per_request_applies_on_demand_ceilings() {
	ds := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"keys", core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
		),
	}
	// No billingMode set -> defaults to PAY_PER_REQUEST; on-demand ceiling applies.
	ctx := deployConfigContext(map[string]*core.ScalarValue{
		"aws.dynamodb.orders.maxReadRequestUnits": core.ScalarFromInt(100),
	})

	table := s.transformWith(map[string]*schema.Resource{"orders": ds}, ctx)["orders_dynamodb_table"]
	s.Require().NotNil(table)
	s.Nil(table.Spec.Fields["provisionedThroughput"])
	od := table.Spec.Fields["onDemandThroughput"]
	s.Require().NotNil(od)
	s.Equal(100, core.IntValue(od.Fields["maxReadRequestUnits"]))
}

// An index with an out-of-range field count (zero, or more than a partition +
// sort key) is reported as an error and skipped rather than silently dropped or
// truncated. The schema enforces the one-or-two bound, so this exercises the
// emit's defensive backstop directly.
func (s *DatastoreTransformTestSuite) Test_index_with_invalid_field_count_errors_and_is_skipped() {
	ds := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"keys", core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
			"indexes", &core.MappingNode{
				Items: []*core.MappingNode{
					core.MappingNodeFields(
						"name", core.MappingNodeFromString("tooMany"),
						"fields", &core.MappingNode{
							Items: []*core.MappingNode{
								core.MappingNodeFromString("a"),
								core.MappingNodeFromString("b"),
								core.MappingNodeFromString("c"),
							},
						},
					),
				},
			},
		),
	}

	out := s.transformOutput(map[string]*schema.Resource{"orders": ds}, validationContext())
	table := out.TransformedBlueprint.Resources.Values["orders_dynamodb_table"]
	s.Require().NotNil(table)
	s.Nil(table.Spec.Fields["globalSecondaryIndexes"], "the invalid index is skipped, not truncated")

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelError &&
			strings.Contains(d.Message, "tooMany") &&
			strings.Contains(d.Message, "one or two fields") {
			found = true
		}
	}
	s.True(found, "expected an error diagnostic about the invalid index cardinality")
}

func (s *DatastoreTransformTestSuite) transformDatastore(
	resources map[string]*schema.Resource,
) map[string]*schema.Resource {
	return s.transformWith(resources, validationContext())
}

func (s *DatastoreTransformTestSuite) transformWith(
	resources map[string]*schema.Resource,
	ctx transform.Context,
) map[string]*schema.Resource {
	return s.transformOutput(resources, ctx).TransformedBlueprint.Resources.Values
}

func (s *DatastoreTransformTestSuite) transformOutput(
	resources map[string]*schema.Resource,
	ctx transform.Context,
) *transform.SpecTransformerTransformOutput {
	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: resources}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: ctx,
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}
