package configaws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/config"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// placeholderAppName is used for the store path segment during validation
// contexts, where the "celerity.appName" context variable is not yet available.
const placeholderAppName = shared.PlaceholderAppName

// Emit builds the Parameter Store tree, or the Secrets Manager secret, a
// celerity/config maps to, for the named AWS deploy target.
func Emit(
	ctx context.Context,
	run *transformutils.Run,
	target string,
	r *config.ResolvedConfig,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	if diag := replicationDeferred(target, r); diag != nil {
		return &transformutils.EmitResult{Diagnostics: []*core.Diagnostic{diag}}, nil
	}

	appName, err := resolveAppName(run)
	if err != nil {
		return nil, err
	}

	storePath := fmt.Sprintf("/celerity/%s/%s", appName, r.StoreName)

	runCtx, _ := transformutils.Use[*shared.RunContext](run)
	identifier, diagnostics, stop := awsexternal.EmitOutcome(
		ctx, runCtx, "celerity/config", r.Name, r.Resource,
	)
	if identifier != nil {
		diagnostics = append(diagnostics, externalValuesConflict(r)...)
	}
	if stop {
		// A nil identifier here means external mode that could not be read: the
		// resource names no identifier, or the one it names is malformed. Either
		// way diagnostics say so and there is nothing to resolve spec.id to.
		if identifier == nil {
			return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
		}

		// No concrete store, but spec.id still has to resolve: the handler's
		// CELERITY_CONFIG_<NS>_STORE_ID carries that reference, and the property
		// map rewrites it to this derived value either way.
		return &transformutils.EmitResult{
			Diagnostics: diagnostics,
			DerivedValues: map[string]*schema.Value{
				configIDKey(r.Name): externalStoreID(identifier),
			},
		}, nil
	}

	values, _ := pluginutils.GetValueByPath("$.values", r.Resource.Spec)
	plaintext := shared.StringSet("$.plaintext", r.Resource.Spec)
	kms, _ := pluginutils.GetValueByPath("$.encryptionKeyId", r.Resource.Spec)

	resources := map[string]*schema.Resource{}

	idRef, err := emitStore(resources, appName, r, values, plaintext, storePath, kms)
	if err != nil {
		return nil, err
	}

	// Rewrite any ${resource.spec.x} references a user embedded in config values
	// (spliced into the secret blob or a parameter value) into concrete form.
	for _, res := range resources {
		res.Spec = subwalk.WalkMappingNode(
			res.Spec,
			transformutils.RewriteResourcePropertyRefs(rw),
		)
	}

	return &transformutils.EmitResult{
		Resources: resources,
		DerivedValues: map[string]*schema.Value{
			configIDKey(r.Name): idRef,
		},
	}, nil
}

// Replication (replicate: true) is deferred because Parameter Store per-region
// copies are blocked on provider support for a per-resource region. Reported
// rather than silently ignored, since a partial store would deploy.
func replicationDeferred(target string, r *config.ResolvedConfig) *core.Diagnostic {
	replicateNode, _ := pluginutils.GetValueByPath("$.replicate", r.Resource.Spec)
	if !core.BoolValue(replicateNode) {
		return nil
	}

	return &core.Diagnostic{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"replication (replicate: true) is not yet supported for celerity/config "+
				"on %s; only single-region stores are emitted", target,
		),
		Range: core.DiagnosticRangeFromSourceMeta(replicateNode.SourceMeta, nil),
	}
}

// The app name is part of every store path, so a missing one cannot be defaulted
// outside validation without putting two apps' config in the same place.
func resolveAppName(run *transformutils.Run) (string, error) {
	if appName := shared.ResolveAppName(run); appName != "" {
		return appName, nil
	}

	if transformutils.IsValidationContext(run.TransformContext) {
		return placeholderAppName, nil
	}

	return "", fmt.Errorf(
		"appName not found for project; it must be set as the top-level " +
			"\"appName\" field in app.deploy.jsonc",
	)
}

func emitStore(
	resources map[string]*schema.Resource,
	appName string,
	r *config.ResolvedConfig,
	values *core.MappingNode,
	plaintext map[string]struct{},
	storePath string,
	kms *core.MappingNode,
) (*schema.Value, error) {
	if len(plaintext) == 0 {
		// All values are secret, so a single aws/secretsmanager/secret holding a
		// JSON-encoded blob.
		return emitSecretsManagerResource(resources, appName, r, values, storePath, kms)
	}

	// Mixed, so a single aws/ssm/parameterTree owning one parameter per key
	// beneath the path prefix, with blob-like drift semantics.
	return emitParameterTreeResource(
		resources, appName, r, values, plaintext, storePath, kms,
	)
}

func emitSecretsManagerResource(
	resources map[string]*schema.Resource,
	appName string,
	r *config.ResolvedConfig,
	values *core.MappingNode,
	storePath string,
	kms *core.MappingNode,
) (*schema.Value, error) {
	secretString, err := buildSecretStringNode(values)
	if err != nil {
		return nil, err
	}

	spec := core.MappingNodeFields(
		"name", core.MappingNodeFromString(storePath),
		"secretString", secretString,
	)
	if kms != nil {
		spec.Fields["kmsKeyId"] = kms
	}
	// aws/secretsmanager/secret.tags is a list of {key, value} objects.
	if tags := sharedaws.SpecTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      appName,
		ResourceName: r.Name,
		ResourceType: "celerity/config",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	resName := secretResourceName(r.Name)
	resources[resName] = &schema.Resource{
		Type: &schema.ResourceTypeWrapper{
			Value: "aws/secretsmanager/secret",
		},
		Spec:     spec,
		Metadata: infraMeta(r),
	}

	// The secret's id output IS the ARN (this resource has no arn attribute).
	return shared.SubstitutionBlueprintValue(
		fmt.Sprintf("${resources.%s.spec.id}", resName),
	)
}

// Emits a single aws/ssm/parameterTree that owns one SSM
// parameter per key beneath the store's path prefix. Plaintext keys land in the
// "values" map (String parameters); the rest land in "secureValues" (SecureString,
// encrypted, redacted). The tree treats its stored values as an opaque blob for
// drift, so config values written out-of-band by the Celerity CLI are
// never reported or reverted on redeploy.
func emitParameterTreeResource(
	resources map[string]*schema.Resource,
	appName string,
	r *config.ResolvedConfig,
	values *core.MappingNode,
	plaintextKeys map[string]struct{},
	storePath string,
	kms *core.MappingNode,
) (*schema.Value, error) {
	valueEntries := &core.MappingNode{Fields: map[string]*core.MappingNode{}}
	secureEntries := &core.MappingNode{Fields: map[string]*core.MappingNode{}}

	if values != nil {
		for _, key := range shared.SortedKeys(values.Fields) {
			node := paramValueNode(values.Fields[key])
			if _, isPlain := plaintextKeys[key]; isPlain {
				valueEntries.Fields[key] = node
			} else {
				secureEntries.Fields[key] = node
			}
		}
	}

	spec := core.MappingNodeFields(
		"path", core.MappingNodeFromString(storePath),
	)
	if len(valueEntries.Fields) > 0 {
		spec.Fields["values"] = valueEntries
	}
	if len(secureEntries.Fields) > 0 {
		spec.Fields["secureValues"] = secureEntries
	}
	// keyId encrypts the SecureString ("secureValues") parameters only.
	if kms != nil && len(secureEntries.Fields) > 0 {
		spec.Fields["keyId"] = kms
	}
	// aws/ssm/parameterTree.tags is a map of string -> string, applied to every
	// parameter in the tree.
	if tags := sharedaws.MapTagsFor(r.Resource.Metadata, sharedaws.Identity{
		AppName:      appName,
		ResourceName: r.Name,
		ResourceType: "celerity/config",
	}); tags != nil {
		spec.Fields["tags"] = tags
	}

	resources[paramTreeResourceName(r.Name)] = &schema.Resource{
		Type: &schema.ResourceTypeWrapper{
			Value: "aws/ssm/parameterTree",
		},
		Spec:     spec,
		Metadata: infraMeta(r),
	}

	// The SSM store id is the path prefix (a literal, not a resource attribute).
	return shared.LiteralStringBlueprintValue(storePath), nil
}

// Assembles the Secrets Manager blob as a
// StringWithSubstitutions so that any substitution-valued config entries survive
// to the concrete deployment stage rather than being serialised as their AST.
func buildSecretStringNode(values *core.MappingNode) (*core.MappingNode, error) {
	parts := []*substitutions.StringOrSubstitution{}
	pushLiteral := func(s string) {
		parts = append(
			parts,
			&substitutions.StringOrSubstitution{
				StringValue: &s,
			},
		)
	}

	pushLiteral("{")
	if values != nil {
		// sorted -> deterministic (stable contentHash)
		for i, k := range shared.SortedKeys(values.Fields) {
			if i > 0 {
				pushLiteral(",")
			}
			// JSON-encode+quote the key
			kb, _ := json.Marshal(k)
			pushLiteral(fmt.Sprintf("%s:", string(kb)))

			node := values.Fields[k]
			if node.StringWithSubstitutions != nil {
				// substitution-bearing value -> quote it and splice in its parsed parts
				pushLiteral(`"`)
				parts = append(parts, node.StringWithSubstitutions.Values...)
				pushLiteral(`"`)
			} else {
				// pure scalar -> native JSON (string quoted+escaped, number/bool bare)
				vb, err := json.Marshal(node)
				if err != nil {
					return nil, err
				}
				pushLiteral(string(vb))
			}
		}
	}
	pushLiteral("}")

	return &core.MappingNode{
		StringWithSubstitutions: &substitutions.StringOrSubstitutions{
			Values: parts,
		},
	}, nil
}

func paramValueNode(node *core.MappingNode) *core.MappingNode {
	if node == nil {
		return core.MappingNodeFromString("")
	}
	if node.StringWithSubstitutions != nil {
		return node
	}
	if node.Scalar != nil && node.Scalar.StringValue != nil {
		return node
	}
	return core.MappingNodeFromString(scalarToString(node))
}

func scalarToString(node *core.MappingNode) string {
	if node == nil || node.Scalar == nil {
		return ""
	}
	switch {
	case node.Scalar.StringValue != nil:
		return *node.Scalar.StringValue
	case node.Scalar.IntValue != nil:
		return strconv.Itoa(*node.Scalar.IntValue)
	case node.Scalar.BoolValue != nil:
		return strconv.FormatBool(*node.Scalar.BoolValue)
	case node.Scalar.FloatValue != nil:
		return strconv.FormatFloat(*node.Scalar.FloatValue, 'f', -1, 64)
	}
	return ""
}

func infraMeta(r *config.ResolvedConfig) *schema.Metadata {
	meta := &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: r.Name,
				AbstractResourceType: "celerity/config",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}

	// Carry the labels matched by handler -> config edges so each linked
	// handler's preserved linkSelector matches the concrete store and the
	// provider lambda -> store link forms.
	// This is deliberately not the config's full label set: only handler
	// links have a concrete lambda -> store implementation, so labels matched
	// by non-handler selectors (e.g. the api::config association, a no-op on
	// aws-serverless) would at best be inert and at worst form unintended
	// concrete associations as the provider's link surface grows.
	// Handler-matched labels are exactly the set the contract wiring needs.
	if len(r.HandlerSelectorLabels) > 0 {
		meta.Labels = &schema.StringMap{
			Values: r.HandlerSelectorLabels,
		}
	}

	return meta
}

func secretResourceName(configName string) string {
	return fmt.Sprintf("%s_config_secret", configName)
}

func paramTreeResourceName(configName string) string {
	return fmt.Sprintf("%s_config_param_tree", configName)
}

// This is derived from concreteName so the emitted derived-value key
// always matches the ${values.<concreteName>_id} reference the property map's
// "spec.id" ValueRef rewrites to.
func configIDKey(configName string) string {
	return concreteName(configName) + "_id"
}

// An external store's contents are not Celerity's to write, so declaring values
// alongside it is a contradiction rather than a no-op.
//
// Deliberately not target-named: this holds for any target, and only lives in an
// aws-serverless file because that is the only emit so far.
func externalValuesConflict(r *config.ResolvedConfig) []*core.Diagnostic {
	values, _ := pluginutils.GetValueByPath("$.values", r.Resource.Spec)
	if values == nil || len(values.Fields) == 0 {
		return nil
	}

	return []*core.Diagnostic{{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/config %q is in external mode and declares \"values\". An external "+
				"store names one Celerity does not own, so its contents are not written "+
				"from the blueprint.",
			r.Name,
		),
		Range: core.DiagnosticRangeFromSourceMeta(r.Resource.SourceMeta, nil),
	}}
}

// The store identifier an external config resolves to. Parameter Store is
// addressed by path and Secrets Manager by ARN.
func externalStoreID(identifier *awsexternal.Identifier) *schema.Value {
	storeID := identifier.Literal
	if identifier.ARN.Service == "ssm" {
		storeID = identifier.ParameterPath()
	}

	return &schema.Value{
		Type:  &schema.ValueTypeWrapper{Value: schema.ValueTypeString},
		Value: core.MappingNodeFromString(storeID),
	}
}

// The emitted concrete store's resource name.
func concreteName(name string) string {
	return fmt.Sprintf("%s_config_store", name)
}
