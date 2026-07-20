//go:build unit

package transformer

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type SharedRoleTransformTestSuite struct {
	suite.Suite
}

func TestSharedRoleTransformTestSuite(t *testing.T) {
	suite.Run(t, new(SharedRoleTransformTestSuite))
}

// Contract (docs/contract/aws-serverless.md section 8): a shared execution role
// records every handler using it in the celerity.handler.sharedBy annotation as
// a sorted, comma-separated list, while AbstractResourceName stays the first
// requester's name.
func (s *SharedRoleTransformTestSuite) Test_shared_roles_carry_the_sorted_sharedBy_annotation() {
	// walkHandler and syncHandler share a link set (none), so they share one
	// role; queueHandler links a queue, so it gets its own.
	walkHandler := sharedRoleTestHandler("walkHandler", nil)
	syncHandler := sharedRoleTestHandler("syncHandler", nil)
	queueHandler := sharedRoleTestHandler("queueHandler", map[string]string{"app": "orders"})
	queueRes := &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "celerity/queue"},
		Spec:     core.MappingNodeFields("name", core.MappingNodeFromString("orders")),
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}}},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"walkHandler": walkHandler, "syncHandler": syncHandler,
		"queueHandler": queueHandler, "ordersQueue": queueRes,
	}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          edges(edge("queueHandler", "ordersQueue", "celerity/handler", "celerity/queue")),
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	resources := out.TransformedBlueprint.Resources.Values

	sharedRole := resourceRefName(resources["walkHandler_lambda_func"].Spec.Fields["role"])
	s.Equal(sharedRole, resourceRefName(resources["syncHandler_lambda_func"].Spec.Fields["role"]),
		"handlers with identical link sets must share one role")
	queueRole := resourceRefName(resources["queueHandler_lambda_func"].Spec.Fields["role"])
	s.NotEqual(sharedRole, queueRole)

	s.Equal("syncHandler,walkHandler", s.sharedParentAnnotation(resources[sharedRole], handler.AnnotationKeySharedBy),
		"a shared role must list every sharer, sorted")
	s.Equal("queueHandler", s.sharedParentAnnotation(resources[queueRole], handler.AnnotationKeySharedBy),
		"an unshared role lists only its single handler")

	// The base shared-parent annotations remain alongside sharedBy.
	s.Equal("celerity/handler",
		s.sharedParentAnnotation(resources[sharedRole], transformutils.AnnotationSourceAbstractType))
}

func (s *SharedRoleTransformTestSuite) sharedParentAnnotation(resource *schema.Resource, key string) string {
	s.Require().NotNil(resource)
	s.Require().NotNil(resource.Metadata)
	s.Require().NotNil(resource.Metadata.Custom, "shared-parent annotations live in metadata custom")
	return core.StringValue(resource.Metadata.Custom.Fields[key])
}

func sharedRoleTestHandler(name string, selector map[string]string) *schema.Resource {
	resource := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
		Spec: core.MappingNodeFields(
			"handlerName", core.MappingNodeFromString(name),
			"handler", core.MappingNodeFromString("handlers."+name),
			"runtime", core.MappingNodeFromString("nodejs24.x"),
		),
	}
	if selector != nil {
		resource.LinkSelector = &schema.LinkSelector{ByLabel: &schema.StringMap{Values: selector}}
	}
	return resource
}
