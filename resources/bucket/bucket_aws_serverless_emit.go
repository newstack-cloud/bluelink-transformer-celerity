package bucket

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

const (
	sseAlgorithmKMS = "aws:kms"
	sseAlgorithmAES = "AES256"
)

// deferredConfigs are abstract bucket configs the aws-serverless emit does not
// yet map. They are accepted by the schema but raise a warning when set so
// nothing is silently dropped; they are scheduled for a follow-up pass.
var deferredConfigs = []string{"lifecycle", "replication"}

func emitBucket(
	_ context.Context,
	_ *transformutils.Run,
	r *ResolvedBucket,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	if name := core.StringValue(specGet(r, "$.name")); name != "" {
		spec.Fields["bucketName"] = core.MappingNodeFromString(name)
	}
	if enc := buildEncryption(r); enc != nil {
		spec.Fields["bucketEncryption"] = enc
	}
	// The abstract cors.corsRules shape maps 1:1 onto the provider's
	// corsConfiguration.corsRules, so it passes through unchanged.
	if cors, ok := pluginutils.GetValueByPath("$.cors.corsRules", r.Resource.Spec); ok && cors != nil {
		spec.Fields["corsConfiguration"] = core.MappingNodeFields("corsRules", cors)
	}
	if status, ok := pluginutils.GetValueByPath("$.versioning.status", r.Resource.Spec); ok && status != nil {
		spec.Fields["versioningConfiguration"] = core.MappingNodeFields("status", status)
	}
	if website := buildWebsite(r); website != nil {
		spec.Fields["websiteConfiguration"] = website
	}
	if logging := buildLogging(r); logging != nil {
		spec.Fields["loggingConfiguration"] = logging
	}

	// aws/s3/bucket.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFromResourceMetadata(r.Resource.Metadata); tags != nil {
		spec.Fields["tags"] = tags
	}

	// Rewrite any ${resources.x.spec.y} references a user embedded into concrete form.
	spec = subwalk.WalkMappingNode(spec, transformutils.RewriteResourcePropertyRefs(rw))

	res := &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/s3/bucket"},
		Spec:         spec,
		Metadata:     bucketMetadata(r),
		LinkSelector: r.Resource.LinkSelector,
	}

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			bucketConcreteName(r.Name): res,
		},
		Diagnostics: deferredConfigDiagnostics(r),
	}, nil
}

// The SSE algorithm is taken from encryptionAlgorithm when set, otherwise
// defaults to KMS when a key is supplied and SSE-S3 (AES256) otherwise.
func buildEncryption(r *ResolvedBucket) *core.MappingNode {
	enc, ok := pluginutils.GetValueByPath("$.encryption", r.Resource.Spec)
	if !ok || enc == nil {
		return nil
	}

	keyID := enc.Fields["encryptionKeyId"]
	hasKey := keyID != nil && core.StringValue(keyID) != ""

	algorithm := core.StringValue(enc.Fields["encryptionAlgorithm"])
	if hasKey {
		// A customer-managed key only applies under KMS; an explicit non-KMS
		// algorithm paired with a key is invalid on S3, so KMS is authoritative
		// whenever a key is present.
		algorithm = sseAlgorithmKMS
	} else if algorithm == "" {
		algorithm = sseAlgorithmAES
	}

	sseByDefault := core.MappingNodeFields(
		"sseAlgorithm", core.MappingNodeFromString(algorithm),
	)
	// kmsMasterKeyID is only valid under KMS; never emit it for non-KMS encryption.
	if hasKey && algorithm == sseAlgorithmKMS {
		sseByDefault.Fields["kmsMasterKeyID"] = keyID
	}

	return core.MappingNodeFields(
		"serverSideEncryptionConfiguration", &core.MappingNode{
			Items: []*core.MappingNode{
				core.MappingNodeFields("serverSideEncryptionByDefault", sseByDefault),
			},
		},
	)
}

func buildWebsite(r *ResolvedBucket) *core.MappingNode {
	web, ok := pluginutils.GetValueByPath("$.website", r.Resource.Spec)
	if !ok || web == nil {
		return nil
	}
	out := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	if idx := web.Fields["mainPageDocument"]; idx != nil {
		out.Fields["indexDocument"] = idx
	}
	if errDoc := web.Fields["notFoundDocument"]; errDoc != nil {
		out.Fields["errorDocument"] = errDoc
	}
	if len(out.Fields) == 0 {
		return nil
	}
	return out
}

func buildLogging(r *ResolvedBucket) *core.MappingNode {
	log, ok := pluginutils.GetValueByPath("$.logging", r.Resource.Spec)
	if !ok || log == nil {
		return nil
	}
	out := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	if dest := log.Fields["destinationBucket"]; dest != nil {
		out.Fields["destinationBucketName"] = dest
	}
	if prefix := log.Fields["logFilePrefix"]; prefix != nil {
		out.Fields["logFilePrefix"] = prefix
	}
	if len(out.Fields) == 0 {
		return nil
	}
	return out
}

func deferredConfigDiagnostics(r *ResolvedBucket) []*core.Diagnostic {
	var diags []*core.Diagnostic
	for _, cfg := range deferredConfigs {
		node, ok := pluginutils.GetValueByPath("$."+cfg, r.Resource.Spec)
		if !ok || node == nil {
			continue
		}
		diags = append(diags, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"bucket %q configuration is not yet mapped on aws-serverless and will not be "+
					"applied to the emitted resource; support is scheduled for a follow-up pass",
				cfg,
			),
			Range: core.DiagnosticRangeFromSourceMeta(node.SourceMeta, nil),
		})
	}
	return diags
}

func bucketMetadata(r *ResolvedBucket) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/bucket",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
	if r.Resource.Metadata != nil {
		meta.Labels = r.Resource.Metadata.Labels
	}
	return meta
}

func specGet(r *ResolvedBucket, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, r.Resource.Spec)
	return node
}

func bucketConcreteName(name string) string {
	return fmt.Sprintf("%s_s3_bucket", name)
}
