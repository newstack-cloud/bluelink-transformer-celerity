package handlerawsserverless

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func SharedParents(
	ctx context.Context,
	primaries []transformutils.ResolvedResource,
	manifest *build.Manifest,
	appName string,
) []transformutils.SharedParent {
	handlers := []*handler.ResolvedHandler{}
	for _, resource := range primaries {
		if handler, ok := resource.(*handler.ResolvedHandler); ok {
			handlers = append(handlers, handler)
		}
	}

	if len(handlers) == 0 {
		return nil
	}

	parents := []transformutils.SharedParent{}

	parents = append(parents, collectIAMRoles(handlers, appName)...)
	parents = append(parents, collectLambdaLayers(handlers, manifest)...)

	// An error here means a fan-out reference could not be built, which would
	// otherwise surface as a missing queue far from its cause. Shared parent
	// collection cannot report errors, so it is dropped and the per-handler emit
	// path reports it against the handler that owns the binding.
	fanouts, err := collectTopicFanouts(handlers, appName)
	if err == nil {
		parents = append(parents, fanouts...)
	}

	return parents
}

func collectIAMRoles(
	handlers []*handler.ResolvedHandler,
	appName string,
) []transformutils.SharedParent {
	// Collect one IAM role per distinct fingerprint, remembering every handler
	// that shares it so the role can be annotated with the full sharer set.
	fingerprints := []string{}
	plans := map[string]*awslambda.RolePlan{}
	firstHandler := map[string]string{}
	sharers := map[string][]string{}
	for _, resolved := range handlers {
		plan := RolePlanFor(resolved)
		planRef := &plan
		fingerprint := plan.Fingerprint()
		if _, seen := plans[fingerprint]; !seen {
			fingerprints = append(fingerprints, fingerprint)
			plans[fingerprint] = planRef
			firstHandler[fingerprint] = resolved.Name
		}
		sharers[fingerprint] = append(sharers[fingerprint], resolved.Name)
	}

	parents := []transformutils.SharedParent{}
	for _, fingerprint := range fingerprints {
		resourceName := iamRoleResourceName(fingerprint)
		physicalName := physicalRoleName(appName, fingerprint)
		annotations := handler.SharedParentAnnotations(
			firstHandler[fingerprint],
			transformutils.ResourceCategoryInfrastructure,
		)
		// celerity.handler.sharedBy lists every handler using the role, sorted
		// for a deterministic value regardless of primary iteration order
		// (docs/contract/aws-serverless.md section 8).
		sharedBy := sharers[fingerprint]
		sort.Strings(sharedBy)
		annotations.Fields[handler.AnnotationKeySharedBy] =
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
				SeedSpec: taggedRoleSeed(appName, physicalName, plans[fingerprint]),
			},
		)
	}

	return parents
}

func collectLambdaLayers(
	handlers []*handler.ResolvedHandler,
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
	for _, resolved := range handlers {
		handlerName := handlerSpecName(resolved)
		hash, artifact := awslambda.SelectLayerForHandler(handlerName, manifest)
		if hash == "" {
			continue
		}

		if _, ok := runtimes[hash]; !ok {
			runtimes[hash] = map[string]bool{}
			artifacts[hash] = artifact
			hashNames[hash] = resolved.Name
			layerHashes = append(layerHashes, hash)
		}

		if runtime, ok := getTargetRuntime(handler.ResolvedRuntime(resolved), shared.AWSServerless); ok {
			runtimes[hash][runtime] = true
		}
	}

	for _, hash := range layerHashes {
		parents = append(parents, transformutils.SharedParent{
			Key:          "layer:" + hash,
			ResourceName: lambdaLayerResourceName(hash),
			ResourceType: "aws/lambda/layerVersion",
			Annotations:  handler.SharedParentAnnotations(hashNames[hash], transformutils.ResourceCategoryCodeHosting),
			SeedSpec:     awslambda.SeedLayerSpec(artifacts[hash], shared.SortedKeys(runtimes[hash])),
		})
	}

	return parents
}

func handlerSpecName(r *handler.ResolvedHandler) string {
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

// A build manifest content hash names its algorithm ("sha256:<hex>"), and a
// resource name is lexed as part of a ${resources.<name>...} reference, where a
// colon is not a legal character. Folding anything outside the reference
// alphabet to an underscore keeps the declared name and every reference to it
// in agreement, since both are built here.
func lambdaLayerResourceName(h string) string {
	return fmt.Sprintf("celerityLambdaLayer_%s", referenceSafe(h))
}

func referenceSafe(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
			return r
		default:
			return '_'
		}
	}, value)
}

// The execution role's seed spec with Celerity's identity attached.
//
// A role is shared by every handler with the same permission set, so it doesn't name a
// single abstract resource, tagging it with one would attribute shared
// infrastructure to whichever handler happened to be first. The app and the type
// are true of it, and are what a cost or ownership query needs.
func taggedRoleSeed(
	appName string,
	physicalName string,
	plan *awslambda.RolePlan,
) *core.MappingNode {
	seed := awslambda.SeedRoleSpec(physicalName, plan)
	if seed == nil || seed.Fields == nil {
		return seed
	}

	if tags := sharedaws.SpecTagsFor(nil, sharedaws.Identity{
		AppName:      appName,
		ResourceType: "celerity/handler",
	}); tags != nil {
		seed.Fields["tags"] = tags
	}

	return seed
}
