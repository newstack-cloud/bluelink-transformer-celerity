package handler

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func AWSServerlessSharedParents(
	ctx context.Context,
	primaries []transformutils.ResolvedResource,
	manifest *build.Manifest,
) []transformutils.SharedParent {
	handlers := []*ResolvedHandler{}
	for _, resource := range primaries {
		if handler, ok := resource.(*ResolvedHandler); ok {
			handlers = append(handlers, handler)
		}
	}

	if len(handlers) == 0 {
		return nil
	}

	parents := []transformutils.SharedParent{}

	parents = append(parents, collectIAMRoles(handlers, manifest)...)
	parents = append(parents, collectLambdaLayers(handlers, manifest)...)

	return parents
}

func collectIAMRoles(
	handlers []*ResolvedHandler,
	_ *build.Manifest,
) []transformutils.SharedParent {
	parents := []transformutils.SharedParent{}

	// Collect one IAM role per distinct fingerprint.
	seen := map[string]bool{}
	for _, handler := range handlers {
		plan := handler.awsRolePlan()
		fingerprint := plan.Fingerprint()
		_, fingerprintSeen := seen[fingerprint]
		if fingerprintSeen {
			continue
		}
		seen[fingerprint] = true

		roleName := iamRoleResourceName(fingerprint)
		parents = append(
			parents,
			transformutils.SharedParent{
				Key:          fmt.Sprintf("iam-role:%s", fingerprint),
				ResourceName: roleName,
				ResourceType: "aws/iam/role",
				Annotations: sharedParentAnnotations(
					handler.Name,
					transformutils.ResourceCategoryInfrastructure,
				),
				// The seed is the COMPLETE role spec: provider links inject their
				// own per-link IAM statements at deploy time, so there are no
				// per-handler contributions to merge into it.
				SeedSpec: awslambda.SeedRoleSpec(roleName, plan),
			},
		)
	}

	return parents
}

func collectLambdaLayers(
	handlers []*ResolvedHandler,
	manifest *build.Manifest,
) []transformutils.SharedParent {
	if manifest == nil {
		// Lambda layers depend on the build manifest,
		// without it we can't determine if any layers are needed.
		return nil
	}

	parents := []transformutils.SharedParent{}

	runtimes := map[string]map[string]bool{}
	artifacts := map[string]*build.LambdaArtifact{}
	hashNames := map[string]string{}
	layerHashes := []string{}
	for _, handler := range handlers {
		handlerName := handlerSpecName(handler)
		hash, artifact := awslambda.SelectLayerForHandler(handlerName, manifest)
		if hash == "" {
			continue
		}
		if _, ok := runtimes[hash]; !ok {
			runtimes[hash] = map[string]bool{}
			artifacts[hash] = artifact
			hashNames[hash] = handler.Name
			layerHashes = append(layerHashes, hash)
		}
		if runtime, ok := getTargetRuntime(resolvedRuntime(handler), shared.AWSServerless); ok {
			runtimes[hash][runtime] = true
		}
	}

	for _, hash := range layerHashes {
		parents = append(parents, transformutils.SharedParent{
			Key:          "layer:" + hash,
			ResourceName: lambdaLayerResourceName(hash),
			ResourceType: "aws/lambda/layerVersion",
			Annotations:  sharedParentAnnotations(hashNames[hash], transformutils.ResourceCategoryCodeHosting),
			SeedSpec:     awslambda.SeedLayerSpec(artifacts[hash], shared.SortedKeys(runtimes[hash])),
		})
	}

	return parents
}

func handlerSpecName(r *ResolvedHandler) string {
	name, _ := pluginutils.GetValueByPath("$.handlerName", r.Resource.Spec)
	return core.StringValue(name)
}

func iamRoleResourceName(fp string) string {
	return fmt.Sprintf("celerityLambdaExec_%s", fp)
}

func lambdaLayerResourceName(h string) string {
	return fmt.Sprintf("celerityLambdaLayer_%s", h)
}
