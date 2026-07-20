package handler

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	appName string,
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

	parents = append(parents, collectIAMRoles(handlers, appName)...)
	parents = append(parents, collectLambdaLayers(handlers, manifest)...)

	return parents
}

func collectIAMRoles(
	handlers []*ResolvedHandler,
	appName string,
) []transformutils.SharedParent {
	// Collect one IAM role per distinct fingerprint, remembering every handler
	// that shares it so the role can be annotated with the full sharer set.
	fingerprints := []string{}
	plans := map[string]*awslambda.RolePlan{}
	firstHandler := map[string]string{}
	sharers := map[string][]string{}
	for _, handler := range handlers {
		plan := handler.awsRolePlan()
		fingerprint := plan.Fingerprint()
		if _, seen := plans[fingerprint]; !seen {
			fingerprints = append(fingerprints, fingerprint)
			plans[fingerprint] = plan
			firstHandler[fingerprint] = handler.Name
		}
		sharers[fingerprint] = append(sharers[fingerprint], handler.Name)
	}

	parents := []transformutils.SharedParent{}
	for _, fingerprint := range fingerprints {
		resourceName := iamRoleResourceName(fingerprint)
		physicalName := physicalRoleName(appName, fingerprint)
		annotations := sharedParentAnnotations(
			firstHandler[fingerprint],
			transformutils.ResourceCategoryInfrastructure,
		)
		// celerity.handler.sharedBy lists every handler using the role, sorted
		// for a deterministic value regardless of primary iteration order
		// (docs/contract/aws-serverless.md section 8).
		sharedBy := sharers[fingerprint]
		sort.Strings(sharedBy)
		annotations.Fields[AnnotationKeySharedBy] =
			core.MappingNodeFromString(strings.Join(sharedBy, ","))

		parents = append(
			parents,
			transformutils.SharedParent{
				Key:          fmt.Sprintf("iam-role:%s", fingerprint),
				ResourceName: resourceName,
				ResourceType: "aws/iam/role",
				Annotations:  annotations,
				// The seed is the complete role spec, the provider links inject their
				// own per-link IAM statements at deploy time, so there are no
				// per-handler contributions to merge into it.
				SeedSpec: awslambda.SeedRoleSpec(physicalName, plans[fingerprint]),
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

// IAM's hard cap on role names.
const iamRoleNameMaxLength = 64

func physicalRoleName(appName string, fp string) string {
	return shared.AppScopedPhysicalName(
		appName, fmt.Sprintf("lambdaExec-%s", fp), iamRoleNameMaxLength,
	)
}

func lambdaLayerResourceName(h string) string {
	return fmt.Sprintf("celerityLambdaLayer_%s", h)
}
