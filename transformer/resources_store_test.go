//go:build unit

package transformer

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type ResourcesStoreTestSuite struct {
	suite.Suite
}

func TestResourcesStoreTestSuite(t *testing.T) {
	suite.Run(t, new(ResourcesStoreTestSuite))
}

func handlerLinkedTo(label map[string]string) *schema.Resource {
	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString("saveOrder"),
			"handler", core.MappingNodeFromString("handlers.save"),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
		LinkSelector: &schema.LinkSelector{ByLabel: &schema.StringMap{Values: label}},
	}
}

// A handler linked to backing resources yields the internal resources config store
// (an aws/ssm/parameterTree keyed by configKey), the STORE_ID/STORE_KIND env vars on
// the handler, and a store-read policy on its execution role.
func (s *ResourcesStoreTestSuite) Test_backing_links_emit_store_env_and_iam() {
	handlerRes := handlerLinkedTo(map[string]string{"app": "orders"})
	queueRes := &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec:     core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}}},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"saveOrder": handlerRes, "ordersQueue": queueRes,
	}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          edges(edge("saveOrder", "ordersQueue", "celerity/handler", "celerity/queue")),
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	resources := out.TransformedBlueprint.Resources.Values

	// The store: aws/ssm/parameterTree at the resources path, keyed by configKey
	// (the queue's spec.name) -> the queue's physical-id reference.
	store := resources["celerityResourcesConfigStore"]
	s.Require().NotNil(store, "expected the internal resources config store")
	s.Equal("aws/ssm/parameterTree", store.Type.Value)
	s.Equal("/celerity/placeholder-app/resources", core.StringValue(store.Spec.Fields["path"]))
	values := store.Spec.Fields["values"]
	s.Require().NotNil(values)
	s.Require().NotNil(values.Fields["orders"], "expected a store entry keyed by the queue's configKey")
	s.Equal("ordersQueue_sqs_queue", resourceRefName(values.Fields["orders"]))

	// The handler carries the store discovery env vars.
	lambda := resources["saveOrder_lambda_func"]
	s.Require().NotNil(lambda)
	env := lambda.Spec.Fields["environment"].Fields["variables"].Fields
	s.Equal("/celerity/placeholder-app/resources", core.StringValue(env["CELERITY_CONFIG_RESOURCES_STORE_ID"]))
	s.Equal("parameter-store", core.StringValue(env["CELERITY_CONFIG_RESOURCES_STORE_KIND"]))

	// Its execution role carries the store-read policy.
	role := resources[resourceRefName(lambda.Spec.Fields["role"])]
	s.Require().NotNil(role)
	s.True(hasPolicyNamed(role, "celerity-resource-links-store"),
		"expected a store-read inline policy on the execution role")
}

// A handler with no backing resource links yields no store and no store env vars.
func (s *ResourcesStoreTestSuite) Test_no_backing_links_no_store() {
	handlerRes := handlerLinkedTo(map[string]string{"app": "orders"})

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"saveOrder": handlerRes,
	}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	resources := out.TransformedBlueprint.Resources.Values

	s.Nil(resources["celerityResourcesConfigStore"], "no store without backing links")
	lambda := resources["saveOrder_lambda_func"]
	s.Require().NotNil(lambda)
	env := lambda.Spec.Fields["environment"].Fields["variables"].Fields
	s.Nil(env["CELERITY_CONFIG_RESOURCES_STORE_ID"], "no store env var without backing links")
}

// Datastore store entries reference the table's computed arn output, not
// tableName, which the emit only sets when the abstract name is present — so a
// name-less handler-linked datastore still stages (the reference resolves on
// deploy) and DynamoDB calls accept the ARN wherever a table name is
// expected. The name-less entry is keyed by the abstract-name fallback; the
// value shape is identical whether or not the datastore is named.
func (s *ResourcesStoreTestSuite) Test_datastore_entries_reference_the_computed_table_arn() {
	handlerRes := handlerLinkedTo(map[string]string{"data": "orders"})
	namelessDatastore := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"keys", core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
		),
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"data": "orders"}}},
	}
	namedDatastore := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("archive"),
			"keys", core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
		),
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"data": "orders"}}},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"saveOrder": handlerRes, "ordersTable": namelessDatastore, "archiveTable": namedDatastore,
	}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint: bp,
			LinkGraph: edges(
				edge("saveOrder", "ordersTable", "celerity/handler", "celerity/datastore"),
				edge("saveOrder", "archiveTable", "celerity/handler", "celerity/datastore"),
			),
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)

	store := out.TransformedBlueprint.Resources.Values["celerityResourcesConfigStore"]
	s.Require().NotNil(store, "expected the internal resources config store")
	values := store.Spec.Fields["values"]
	s.Require().NotNil(values)

	// Name-less: keyed by the abstract resource name.
	name, path := resourceRefNameAndPath(values.Fields["ordersTable"])
	s.Equal("ordersTable_dynamodb_table", name)
	s.Equal([]string{"spec", "arn"}, path)

	// Named: keyed by spec.name, same computed-arn value shape.
	name, path = resourceRefNameAndPath(values.Fields["archive"])
	s.Equal("archiveTable_dynamodb_table", name)
	s.Equal([]string{"spec", "arn"}, path)
}

// Two backing resources of different types sharing a configKey (an invalid
// blueprint the CLI rejects) must produce a deterministic store — the same winner
// every time — rather than silently varying. The fixed backing-type order (queue
// before datastore) makes the queue the stable winner.
func (s *ResourcesStoreTestSuite) Test_configKey_collision_is_deterministic() {
	handlerRes := handlerLinkedTo(map[string]string{"app": "orders"})
	queueRes := &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec:     core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}}},
	}
	datastoreRes := &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "celerity/datastore"},
		Spec:     core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}}},
	}

	for range 5 {
		bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
			"saveOrder": handlerRes, "ordersQueue": queueRes, "ordersTable": datastoreRes,
		}}}
		out, err := NewTransformer(&shared.Dependencies{}).Transform(
			context.Background(),
			&transform.SpecTransformerTransformInput{
				InputBlueprint: bp,
				LinkGraph: edges(
					edge("saveOrder", "ordersQueue", "celerity/handler", "celerity/queue"),
					edge("saveOrder", "ordersTable", "celerity/handler", "celerity/datastore"),
				),
				TransformerContext: validationContext(),
			},
		)
		s.Require().NoError(err)
		store := out.TransformedBlueprint.Resources.Values["celerityResourcesConfigStore"]
		s.Require().NotNil(store)
		values := store.Spec.Fields["values"]
		s.Require().Len(values.Fields, 1, "colliding configKey collapses to one entry")
		// The queue wins deterministically (queue precedes datastore in the order).
		s.Equal("ordersQueue_sqs_queue", resourceRefName(values.Fields["orders"]))
	}
}

func hasPolicyNamed(role *schema.Resource, name string) bool {
	policies := role.Spec.Fields["policies"]
	if policies == nil {
		return false
	}
	for _, policy := range policies.Items {
		if core.StringValue(policy.Fields["policyName"]) == name {
			return true
		}
	}
	return false
}
