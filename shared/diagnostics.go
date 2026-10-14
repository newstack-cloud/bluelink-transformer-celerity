package shared

import (
	"fmt"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/source"
)

// DiagnosticRange builds a diagnostic range from the first candidate that
// carries source metadata.
//
// Diagnostics should point at the most specific part of the blueprint they are
// about, so callers pass the offending field first and the enclosing resource
// last. Candidates are commonly absent, a diagnostic often fires precisely
// because a field is missing, hence the fallback chain. Without a range, tools
// have nothing to anchor to and report the diagnostic at the start of the
// document.
func DiagnosticRange(candidates ...*source.Meta) *core.DiagnosticRange {
	for _, meta := range candidates {
		if meta != nil {
			return core.DiagnosticRangeFromSourceMeta(meta, nil)
		}
	}

	return nil
}

// NodeMeta returns a mapping node's source metadata, tolerating a nil node so
// that lookups for absent fields can be passed straight to DiagnosticRange.
func NodeMeta(node *core.MappingNode) *source.Meta {
	if node == nil {
		return nil
	}

	return node.SourceMeta
}

// ResourceMeta returns a resource's source metadata, tolerating a nil resource.
func ResourceMeta(resource *schema.Resource) *source.Meta {
	if resource == nil {
		return nil
	}

	return resource.SourceMeta
}

// ScalarMeta returns a scalar value's source metadata, tolerating a nil value.
func ScalarMeta(value *core.ScalarValue) *source.Meta {
	if value == nil {
		return nil
	}

	return value.SourceMeta
}

// StructuralField pairs a field's spec name with its value, for reporting.
type StructuralField struct {
	Name  string
	Value *core.MappingNode
}

// DeferredValueErrors reports every structural field that was set from a
// reference. Fields are checked in the order given so a blueprint with more
// than one always reports them the same way.
func DeferredValueErrors(
	deployTarget string,
	resourceType string,
	resourceName string,
	resource *schema.Resource,
	fields ...StructuralField,
) []*core.Diagnostic {
	var diagnostics []*core.Diagnostic
	for _, field := range fields {
		if diagnostic := DeferredValueError(
			deployTarget, resourceType, resourceName, field.Name, field.Value, resource,
		); diagnostic != nil {
			diagnostics = append(diagnostics, diagnostic)
		}
	}

	return diagnostics
}

// DeferredValueError reports a structural field that was set from a ${...}
// reference, and returns nil for any field that was not.
//
// A field is structural when a deploy target has to know its value while it
// builds the resource graph, either because the value decides which concrete
// resources are emitted (clusterMode, fifo, authMode) or because it has to be
// rewritten into another form (a jwt guard's tokenSource becomes an API Gateway
// identity source). Both happen before the deploy engine resolves references.
//
// Which fields are structural is a property of the target doing the emitting,
// not of the abstract resource, so the caller names its own deployTarget and the
// message says which target cannot honour the reference. A target that passed
// the same field straight through would have no reason to call this.
func DeferredValueError(
	deployTarget string,
	resourceType string,
	resourceName string,
	fieldName string,
	node *core.MappingNode,
	resource *schema.Resource,
) *core.Diagnostic {
	if !IsSubstitution(node) {
		return nil
	}

	return &core.Diagnostic{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/%s %q sets %s from a reference, which cannot be used on %s. The transformer "+
				"reads this value while building the resource graph, which happens before references "+
				"are resolved. Set %s to a literal value.",
			resourceType, resourceName, fieldName, deployTarget, fieldName,
		),
		Range: DiagnosticRange(NodeMeta(node), ResourceMeta(resource)),
	}
}
