//go:build unit

package handler

import (
	"context"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type SharedParentsTestSuite struct {
	suite.Suite
}

func (s *SharedParentsTestSuite) Test_handlers_with_same_tracing_share_one_iam_role() {
	parents := AWSServerlessSharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", true),
			sharedParentHandler("h2", "nodejs24.x", true),
		},
		nil,
		"test-app",
	)

	s.Len(
		parentsOfType(parents, "aws/iam/role"),
		1,
		"Expected handlers with identical tracing to share a single IAM role",
	)
}

func (s *SharedParentsTestSuite) Test_handlers_with_different_tracing_get_separate_iam_roles() {
	parents := AWSServerlessSharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", true),
			sharedParentHandler("h2", "nodejs24.x", false),
		},
		nil,
		"test-app",
	)

	s.Len(
		parentsOfType(parents, "aws/iam/role"),
		2,
		"Expected handlers with different tracing to get separate IAM roles",
	)
}

func (s *SharedParentsTestSuite) Test_nil_manifest_declares_roles_but_no_layers() {
	parents := AWSServerlessSharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", false),
		},
		/* manifest */ nil,
		"test-app",
	)

	s.Len(parentsOfType(parents, "aws/iam/role"), 1)
	s.Empty(
		parentsOfType(parents, "aws/lambda/layerVersion"),
		"Expected no layers to be declared without a build manifest",
	)
}

func (s *SharedParentsTestSuite) Test_handlers_sharing_a_layer_dedup_by_content_hash() {
	manifest := &build.Manifest{
		Lambda: &build.LambdaManifest{
			SharedLayer: &build.LambdaArtifact{
				ContentHash: "sharedhash",
				S3Bucket:    "bucket",
				S3Key:       "key",
			},
		},
	}

	parents := AWSServerlessSharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", false),
			sharedParentHandler("h2", "nodejs24.x", false),
		},
		manifest,
		"test-app",
	)

	layers := parentsOfType(parents, "aws/lambda/layerVersion")
	s.Require().Len(layers, 1, "Handlers resolving to the same shared layer collapse to one layerVersion")
	s.Equal(
		[]string{"nodejs24.x"},
		scalarItems(layers[0].SeedSpec.Fields["compatibleRuntimes"]),
	)
}

func (s *SharedParentsTestSuite) Test_custom_dependency_layer_is_preferred_over_shared() {
	manifest := &build.Manifest{
		Handlers: map[string]*build.HandlerArtifacts{
			"h1": {
				Lambda: &build.LambdaHandlerArtifacts{
					Dependencies: &build.LambdaArtifact{
						ContentHash: "customhash",
						S3Bucket:    "bucket",
						S3Key:       "custom",
					},
				},
			},
		},
		Lambda: &build.LambdaManifest{
			SharedLayer: &build.LambdaArtifact{
				ContentHash: "sharedhash",
				S3Bucket:    "bucket",
				S3Key:       "shared",
			},
		},
	}

	parents := AWSServerlessSharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", false), // custom dependency layer
			sharedParentHandler("h2", "nodejs24.x", false), // shared layer
		},
		manifest,
		"test-app",
	)

	s.Len(
		parentsOfType(parents, "aws/lambda/layerVersion"),
		2,
		"Expected a custom-dependency layer and the shared layer as distinct resources",
	)
}

func (s *SharedParentsTestSuite) Test_role_carries_correlation_annotations() {
	parents := AWSServerlessSharedParents(
		context.Background(),
		[]transformutils.ResolvedResource{
			sharedParentHandler("h1", "nodejs24.x", true),
		},
		nil,
		"test-app",
	)

	role := parentsOfType(parents, "aws/iam/role")[0]
	s.Equal(
		"celerity/handler",
		core.StringValue(role.Annotations.Fields[transformutils.AnnotationSourceAbstractType]),
	)
	s.Equal(
		"infrastructure",
		core.StringValue(role.Annotations.Fields[transformutils.AnnotationResourceCategory]),
	)
}

func (s *SharedParentsTestSuite) Test_no_handlers_returns_no_parents() {
	parents := AWSServerlessSharedParents(context.Background(), nil, nil, "test-app")
	s.Empty(parents)
}

func TestSharedParentsTestSuite(t *testing.T) {
	suite.Run(t, new(SharedParentsTestSuite))
}

func sharedParentHandler(name, runtime string, tracing bool) *ResolvedHandler {
	return &ResolvedHandler{
		Name:           name,
		TracingEnabled: tracing,
		Resource: &schema.Resource{
			Type: &schema.ResourceTypeWrapper{Value: "celerity/handler"},
			Spec: core.MappingNodeFields(
				"handlerName", core.MappingNodeFromString(name),
				"runtime", core.MappingNodeFromString(runtime),
			),
		},
	}
}

func parentsOfType(
	parents []transformutils.SharedParent,
	resourceType string,
) []transformutils.SharedParent {
	matched := []transformutils.SharedParent{}
	for _, parent := range parents {
		if parent.ResourceType == resourceType {
			matched = append(matched, parent)
		}
	}
	return matched
}

func scalarItems(node *core.MappingNode) []string {
	out := []string{}
	for _, item := range node.Items {
		out = append(out, core.StringValue(item))
	}
	return out
}
