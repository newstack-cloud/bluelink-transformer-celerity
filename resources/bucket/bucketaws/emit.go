package bucketaws

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
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

// deferredConfigs are abstract bucket configs no AWS emit maps yet. They are accepted by the schema but raise a warning when set so
// nothing is silently dropped; they are scheduled for a follow-up pass.
var deferredConfigs = []string{"lifecycle", "replication"}

// Emit builds the S3 bucket a celerity/bucket maps to, for the named AWS
// deploy target.
func Emit(
	ctx context.Context,
	run *transformutils.Run,
	target string,
	r *bucket.ResolvedBucket,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	if diagnostics := shared.DeferredValueErrors(
		target,
		"bucket", r.Name, r.Resource,
		shared.ExternalModeStructuralFields(r.Resource)...,
	); len(diagnostics) > 0 {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	// An external bucket emits nothing as it names a bucket Celerity does not own.
	// Handlers still reach it, through the resources store and a grant on their
	// execution role.
	runCtx, _ := transformutils.Use[*shared.RunContext](run)
	identifier, diagnostics, stop := awsexternal.EmitOutcome(
		ctx, runCtx, "celerity/bucket", r.Name, r.Resource,
	)
	if identifier != nil {
		diagnostics = append(diagnostics, shared.ExternalRegionRequired(
			"celerity/bucket", r.Name, r.Resource, identifier.Region,
		)...)
	}

	if stop {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	spec, specDiagnostics := buildBucketSpec(run, r)
	if len(specDiagnostics) > 0 {
		// A field that decides what is emitted could not be honoured, so no
		// bucket is emitted rather than one configured some other way.
		return &transformutils.EmitResult{Diagnostics: specDiagnostics}, nil
	}

	res := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/s3/bucket"},
		// Rewritten so a ${resources.x.spec.y} reference a user embedded in a
		// value reaches the concrete form.
		Spec: subwalk.WalkMappingNode(
			spec, transformutils.RewriteResourcePropertyRefs(rw),
		),
		Metadata:     bucketMetadata(r),
		LinkSelector: r.Resource.LinkSelector,
	}

	return &transformutils.EmitResult{
		Resources: map[string]*schema.Resource{
			bucketConcreteName(r.Name): res,
		},
		Diagnostics: deferredConfigDiagnostics(target, r),
	}, nil
}

// The S3 bucket's own shape. Non-empty diagnostics mean a field that decides
// what is emitted carried a value AWS does not offer, so the spec is incomplete
// and the caller emits nothing.
func buildBucketSpec(
	run *transformutils.Run,
	r *bucket.ResolvedBucket,
) (*core.MappingNode, []*core.Diagnostic) {
	spec := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	if nameNode := passthroughNameNode(specGet(r, "$.name")); nameNode != nil {
		spec.Fields["bucketName"] = nameNode
	}
	encryption, encryptionDiagnostics := buildEncryption(r)
	if len(encryptionDiagnostics) > 0 {
		return nil, encryptionDiagnostics
	}
	if encryption != nil {
		spec.Fields["bucketEncryption"] = encryption
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
	if tags := sharedaws.SpecTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      shared.ResolveAppName(run),
		ResourceName: r.Name,
		ResourceType: "celerity/bucket",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	return spec, nil
}

// The SSE algorithm is taken from encryptionAlgorithm when set, otherwise
// defaults to KMS when a key is supplied and SSE-S3 (AES256) otherwise.
//
// The abstract field is an open string, because which algorithms exist is a
// property of the target environment, so a value S3 does not offer is rejected
// here rather than by the schema.
func buildEncryption(r *bucket.ResolvedBucket) (*core.MappingNode, []*core.Diagnostic) {
	enc, ok := pluginutils.GetValueByPath("$.encryption", r.Resource.Spec)
	if !ok || enc == nil {
		return nil, nil
	}

	keyID := enc.Fields["encryptionKeyId"]
	hasKey := keyID != nil && core.StringValue(keyID) != ""

	algorithm := core.StringValue(enc.Fields["encryptionAlgorithm"])
	if diagnostics := unsupportedAlgorithmDiagnostics(r, enc, algorithm); len(diagnostics) > 0 {
		return nil, diagnostics
	}

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
	), nil
}

// Rejects an algorithm S3 does not offer, naming the ones it does.
//
// An unrecognised value would otherwise reach S3 verbatim and be refused at
// deploy, long after the author could have been told, and with the blueprint
// field that carried it nowhere in the message.
func unsupportedAlgorithmDiagnostics(
	r *bucket.ResolvedBucket,
	encryption *core.MappingNode,
	algorithm string,
) []*core.Diagnostic {
	if algorithm == "" || algorithm == sseAlgorithmKMS || algorithm == sseAlgorithmAES {
		return nil
	}

	node := encryption.Fields["encryptionAlgorithm"]
	if node == nil {
		node = r.Resource.Spec
	}

	return []*core.Diagnostic{{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/bucket %q declares encryptionAlgorithm %q, which AWS does not offer. "+
				"Use %q for a customer-managed key, or %q for the default key",
			r.Name, algorithm, sseAlgorithmKMS, sseAlgorithmAES,
		),
		Range: core.DiagnosticRangeFromSourceMeta(node.SourceMeta, nil),
	}}
}

func buildWebsite(r *bucket.ResolvedBucket) *core.MappingNode {
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

func buildLogging(r *bucket.ResolvedBucket) *core.MappingNode {
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

func deferredConfigDiagnostics(target string, r *bucket.ResolvedBucket) []*core.Diagnostic {
	var diags []*core.Diagnostic
	for _, cfg := range deferredConfigs {
		node, ok := pluginutils.GetValueByPath("$."+cfg, r.Resource.Spec)
		if !ok || node == nil {
			continue
		}
		diags = append(diags, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"bucket %q configuration is not yet mapped on %s and will not be "+
					"applied to the emitted resource; support is scheduled for a follow-up pass",
				cfg, target,
			),
			Range: core.DiagnosticRangeFromSourceMeta(node.SourceMeta, nil),
		})
	}
	return diags
}

func bucketMetadata(r *bucket.ResolvedBucket) *schema.Metadata {
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

func specGet(r *bucket.ResolvedBucket, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, r.Resource.Spec)
	return node
}

// Returns the abstract name node for the concrete spec. A
// substitution-valued name (e.g. "${variables.namePrefix}-uploads") is passed
// through as-is so the deploy engine resolves it, rather than being stringified
// to "" and silently dropped. Returns nil when no name is set so the physical
// bucket name auto-generates.
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

func bucketConcreteName(name string) string {
	return fmt.Sprintf("%s_s3_bucket", name)
}

// ConcreteResourceName is the concrete aws/s3/bucket resource name emitted for the
// abstract bucket. Exported so the internal resources config store can reference it.
func ConcreteResourceName(name string) string {
	return bucketConcreteName(name)
}
