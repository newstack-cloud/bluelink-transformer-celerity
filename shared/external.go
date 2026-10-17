package shared

import (
	"fmt"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Modes a data resource can be declared in. Managed is the default: Celerity
// provisions the resource. External points at one it does not own, in another
// account or the same one, and emits nothing for it.
const (
	ModeManaged  = "managed"
	ModeExternal = "external"
)

// ExternalModeAttributes returns the mode and identifier attributes shared by
// every data resource that can point at a resource Celerity does not own.
// Declared once so the vocabulary cannot drift between resource types.
//
// The identifier is opaque here: its format belongs to the deploy target, so the
// attribute is named for its role rather than for any one target's spelling, and
// described without reference to one. What a given target expects is documented
// with that target, in docs/contract.
func ExternalModeAttributes() map[string]*provider.ResourceDefinitionsSchema {
	return map[string]*provider.ResourceDefinitionsSchema{
		"mode": {
			Type: provider.ResourceDefinitionsSchemaTypeString,
			Description: "Whether Celerity provisions the resource (\"managed\") or points at an " +
				"existing one it does not own (\"external\"). An external resource is never " +
				"created, updated or destroyed by the app that references it, and requires " +
				"\"externalId\".",
			Default: core.MappingNodeFromString(ModeManaged),
			AllowedValues: []*core.MappingNode{
				core.MappingNodeFromString(ModeManaged),
				core.MappingNodeFromString(ModeExternal),
			},
		},
		"externalId": {
			Type: provider.ResourceDefinitionsSchemaTypeString,
			Description: "The target environment's identifier for an existing resource to point " +
				"at, required when \"mode\" is \"external\" and ignored otherwise. Where the " +
				"identifier names an account other than the one being deployed into, that " +
				"account's owner must also grant access to it.",
		},
	}
}

// RegionAttributes returns the region attribute a resource type declares when
// its identifier may not carry one.
func RegionAttributes() map[string]*provider.ResourceDefinitionsSchema {
	return map[string]*provider.ResourceDefinitionsSchema{
		"region": {
			Type: provider.ResourceDefinitionsSchemaTypeString,
			Description: "The region the external resource lives in, for a target whose " +
				"identifier does not carry one. Requests are routed to the resource's own " +
				"region, so a value from another region is not interchangeable.",
		},
	}
}

// External carries a resolved external resource's authored identity through to
// the emit phase and the resources store.
//
// The identifier is a string here and nothing more. Parsing it, and deriving
// whatever a service addresses the resource by, belongs to the deploy target:
// see shared/awsexternal for the AWS reading of it.
type External struct {
	// ID is the identifier as authored.
	ID string
	// Region as authored, empty unless the resource type declares the attribute
	// and the author set it. A target whose identifier carries a region resolves
	// the effective region itself.
	Region string
}

// ResolvedExternal reads the mode and identifier fields, returning nil for a
// managed resource. A nil result with no diagnostics means the resource is
// managed.
//
// Only presence is checked. Whether the identifier is well formed is a question
// about one target's format, so that target's emit asks it.
//
// Diagnostics are returned rather than an error because the resolve phase cannot
// carry them: callers surface these from their emit.
func ResolvedExternal(
	resourceType string,
	name string,
	resource *schema.Resource,
) (*External, []*core.Diagnostic) {
	modeNode, _ := pluginutils.GetValueByPath("$.mode", resource.Spec)
	if core.StringValue(modeNode) != ModeExternal {
		return nil, nil
	}

	identifier := stringField(resource, "$.externalId")
	if identifier == "" {
		return nil, []*core.Diagnostic{ExternalDiagnostic(
			resource,
			fmt.Sprintf(
				"%s %q is in external mode but declares no \"externalId\". An external "+
					"resource is identified by the target environment's identifier for the "+
					"resource it points at.",
				resourceType, name,
			),
		)}
	}

	return &External{
		ID:     identifier,
		Region: stringField(resource, "$.region"),
	}, nil
}

// ExternalRegionRequired reports an external resource whose region is neither
// authored nor carried by its identifier. The effective region is resolved by
// the target, which is the only layer that can read an identifier for one.
func ExternalRegionRequired(
	resourceType string,
	name string,
	resource *schema.Resource,
	effectiveRegion string,
) []*core.Diagnostic {
	if effectiveRegion != "" {
		return nil
	}

	return []*core.Diagnostic{ExternalDiagnostic(
		resource,
		fmt.Sprintf(
			"%s %q is in external mode but declares no \"region\", and its identifier "+
				"carries none. Requests are routed to the resource's own region, so it has "+
				"to be declared.",
			resourceType, name,
		),
	)}
}

// ExternalModeStructuralFields returns the structural fields external mode adds.
// Both decide which concrete resources are emitted, so neither can be set from a
// reference that only resolves after the transform.
func ExternalModeStructuralFields(resource *schema.Resource) []StructuralField {
	modeNode, _ := pluginutils.GetValueByPath("$.mode", resource.Spec)
	identifierNode, _ := pluginutils.GetValueByPath("$.externalId", resource.Spec)

	return []StructuralField{
		{Name: "mode", Value: modeNode},
		{Name: "externalId", Value: identifierNode},
	}
}

// ExternalLiteralRewriter resolves references to an external resource's computed
// outputs to the literals the author declared. An external resource emits no
// concrete resource, so there is nothing for the usual property map to point a
// reference at.
//
// outputs is keyed by the abstract dotted path, so "spec.id" or "spec.name". A
// reference to any other path on the resource is left alone, which surfaces as a
// reference to a resource that does not exist rather than as a wrong value.
func ExternalLiteralRewriter(
	abstractName string,
	outputs map[string]string,
) transformutils.ResourcePropertyRewriter {
	return func(ref *substitutions.SubstitutionResourceProperty) *substitutions.Substitution {
		if ref == nil || ref.ResourceName != abstractName {
			return nil
		}

		literal, mapped := outputs[substitutionPath(ref)]
		if !mapped {
			return nil
		}

		return &substitutions.Substitution{StringValue: &literal}
	}
}

func substitutionPath(ref *substitutions.SubstitutionResourceProperty) string {
	path := ""
	for _, item := range ref.Path {
		if item.FieldName == "" {
			return ""
		}
		if path != "" {
			path += "."
		}
		path += item.FieldName
	}

	return path
}

// ExternalDiagnostic builds an error diagnostic against a resource, for a target
// reporting something about its own reading of an external identifier.
func ExternalDiagnostic(resource *schema.Resource, message string) *core.Diagnostic {
	return &core.Diagnostic{
		Level:   core.DiagnosticLevelError,
		Message: message,
		Range:   core.DiagnosticRangeFromSourceMeta(resource.SourceMeta, nil),
	}
}

func stringField(resource *schema.Resource, path string) string {
	node, _ := pluginutils.GetValueByPath(path, resource.Spec)
	return core.StringValue(node)
}
