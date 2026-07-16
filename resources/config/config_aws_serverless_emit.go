package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// placeholderAppName is used for the store path segment during validation
// contexts, where the "celerity.appName" context variable is not yet available.
const placeholderAppName = "placeholder-app"

func emitConfig(
	_ context.Context,
	run *transformutils.Run,
	r *ResolvedConfig,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	// Replication (replicate: true) is deferred: Parameter Store per-region copies
	// are blocked on provider support for a per-resource region, so we do not emit
	// a partial store. Surface it explicitly rather than silently ignoring it.
	replicateNode, _ := pluginutils.GetValueByPath("$.replicate", r.Resource.Spec)
	if core.BoolValue(replicateNode) {
		return &transformutils.EmitResult{
			Diagnostics: []*core.Diagnostic{
				{
					Level: core.DiagnosticLevelError,
					Message: "replication (replicate: true) is not yet supported for " +
						"celerity/config on aws-serverless; only single-region stores are emitted",
					Range: core.DiagnosticRangeFromSourceMeta(replicateNode.SourceMeta, nil),
				},
			},
		}, nil
	}

	appName := shared.ResolveAppName(run)
	if appName == "" {
		if transformutils.IsValidationContext(run.TransformContext) {
			appName = placeholderAppName
		} else {
			return nil, fmt.Errorf(
				"appName not found for project; it must be set as the top-level " +
					"\"appName\" field in app.deploy.jsonc",
			)
		}
	}

	storePath := fmt.Sprintf("/celerity/%s/%s", appName, r.StoreName)

	values, _ := pluginutils.GetValueByPath("$.values", r.Resource.Spec)
	plaintext := shared.StringSet("$.plaintext", r.Resource.Spec)
	kms, _ := pluginutils.GetValueByPath("$.encryptionKeyId", r.Resource.Spec)

	resources := map[string]*schema.Resource{}

	var idRef *schema.Value
	var err error
	if len(plaintext) == 0 {
		// All values are secret -> a single aws/secretsmanager/secret holding a
		// JSON-encoded blob.
		idRef, err = emitSecretsManagerResource(resources, r, values, storePath, kms)
	} else {
		// Mixed -> a single aws/ssm/parameterTree owning one parameter per key
		// beneath the path prefix, with blob-like drift semantics.
		idRef, err = emitParameterTreeResource(resources, r, values, plaintext, storePath, kms)
	}
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

func emitSecretsManagerResource(
	resources map[string]*schema.Resource,
	r *ResolvedConfig,
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
	if tags := sharedaws.SpecTagsFromResourceMetadata(r.Resource.Metadata); tags != nil {
		spec.Fields["tags"] = tags
	}

	resName := secretResourceName(r.Name)
	resources[resName] = &schema.Resource{
		Type: &schema.ResourceTypeWrapper{
			Value: "aws/secretsmanager/secret",
		},
		Spec:     spec,
		Metadata: infraMeta(r.Name),
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
	r *ResolvedConfig,
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
	if tags := sharedaws.MapTagsFromResourceMetadata(r.Resource.Metadata); tags != nil {
		spec.Fields["tags"] = tags
	}

	resources[paramTreeResourceName(r.Name)] = &schema.Resource{
		Type: &schema.ResourceTypeWrapper{
			Value: "aws/ssm/parameterTree",
		},
		Spec:     spec,
		Metadata: infraMeta(r.Name),
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

func infraMeta(abstractName string) *schema.Metadata {
	return &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: abstractName,
				AbstractResourceType: "celerity/config",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
}

func secretResourceName(configName string) string {
	return fmt.Sprintf("%s_config_secret", configName)
}

func paramTreeResourceName(configName string) string {
	return fmt.Sprintf("%s_config_param_tree", configName)
}

// This is derived from configConcreteName so the emitted derived-value key
// always matches the ${values.<concreteName>_id} reference the property map's
// "spec.id" ValueRef rewrites to.
func configIDKey(configName string) string {
	return configConcreteName(configName) + "_id"
}
