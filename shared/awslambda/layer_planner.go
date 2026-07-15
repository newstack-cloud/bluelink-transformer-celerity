package awslambda

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

func SelectLayerForHandler(
	handlerName string,
	manifest *build.Manifest,
) (string, *build.LambdaArtifact) {
	if h, ok := manifest.Handlers[handlerName]; ok &&
		h.Lambda != nil && h.Lambda.Dependencies != nil {
		return h.Lambda.Dependencies.ContentHash, h.Lambda.Dependencies
	}

	if manifest.Lambda != nil && manifest.Lambda.SharedLayer != nil {
		return manifest.Lambda.SharedLayer.ContentHash, manifest.Lambda.SharedLayer
	}

	return "", nil
}

func SeedLayerSpec(a *build.LambdaArtifact, compatibleRuntimes []string) *core.MappingNode {
	return core.MappingNodeFields(
		"content", core.MappingNodeFields(
			"s3Bucket", core.MappingNodeFromString(a.S3Bucket),
			"s3Key", core.MappingNodeFromString(a.S3Key),
		),
		"compatibleRuntimes", core.MappingNodeFromStringSlice(compatibleRuntimes),
	)
}
