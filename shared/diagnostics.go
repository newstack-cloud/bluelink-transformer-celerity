package shared

import (
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
