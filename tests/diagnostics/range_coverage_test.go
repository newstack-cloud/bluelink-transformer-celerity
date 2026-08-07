//go:build unit

// Package diagnostics holds invariant tests over how the transformer reports
// diagnostics, rather than over what any single transform produces.
package diagnostics

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// filesExemptFromRanges lists files whose diagnostics are not about the
// blueprint, so a blueprint source range would point somewhere unrelated.
var filesExemptFromRanges = map[string]string{
	// Deploy configuration field validators. These report on values from the
	// deploy config, which has no position in the blueprint document.
	filepath.Join("shared", "aws", "config_validation.go"): "validates deploy config, not the blueprint",
}

// Every core.Diagnostic the transformer constructs must set Range.
//
// Source positions now travel across the plugin boundary, so a diagnostic
// without a range is reported at the start of the document by consumers such as
// the language server and the CLI. That fails quietly: the diagnostic still
// shows up, just anchored to the wrong place. Pipeline tests only cover the
// paths their fixtures happen to trigger, so this asserts the invariant over
// the source instead.
func TestEveryDiagnosticSetsARange(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	var offenders []string
	inspected := 0

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if _, exempt := filesExemptFromRanges[relative]; exempt {
			return nil
		}

		found, inspectErr := diagnosticsWithoutRange(path, relative)
		if inspectErr != nil {
			return inspectErr
		}
		offenders = append(offenders, found...)
		inspected += 1

		return nil
	})
	require.NoError(t, err)
	require.Positive(t, inspected, "expected to inspect transformer source files")

	require.Empty(t, offenders, "diagnostics constructed without a Range:\n%s",
		strings.Join(offenders, "\n"))
}

// Reports core.Diagnostic composite literals in a file
// that set no Range field. Literals with no fields at all are skipped: those are
// empty slice or placeholder values rather than a diagnostic being reported.
func diagnosticsWithoutRange(path string, relative string) ([]string, error) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	var offenders []string
	report := func(literal *ast.CompositeLit) {
		if len(literal.Elts) == 0 || hasField(literal, "Range") {
			return
		}
		position := fileSet.Position(literal.Pos())
		offenders = append(offenders, fmt.Sprintf("  %s:%d", relative, position.Line))
	}

	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, isLiteral := node.(*ast.CompositeLit)
		if !isLiteral {
			return true
		}

		// A literal naming the type directly, as in &core.Diagnostic{...}.
		if isDiagnosticType(literal.Type) {
			report(literal)
			return true
		}

		// A collection of diagnostics, as in []*core.Diagnostic{{...}, {...}}.
		// Elements elide the type, so they carry no type of their own and have to
		// be recognised through the collection that holds them.
		if !isDiagnosticCollection(literal.Type) {
			return true
		}
		for _, element := range literal.Elts {
			if elementLiteral, ok := element.(*ast.CompositeLit); ok && elementLiteral.Type == nil {
				report(elementLiteral)
			}
		}

		return true
	})

	return offenders, nil
}

// isDiagnosticType matches the core.Diagnostic type, with or without a pointer.
func isDiagnosticType(expr ast.Expr) bool {
	if star, isStar := expr.(*ast.StarExpr); isStar {
		expr = star.X
	}

	selector, isSelector := expr.(*ast.SelectorExpr)
	if !isSelector {
		return false
	}

	pkg, isIdent := selector.X.(*ast.Ident)

	return isIdent && pkg.Name == "core" && selector.Sel.Name == "Diagnostic"
}

// isDiagnosticCollection matches slice, array and map types whose elements are
// diagnostics.
func isDiagnosticCollection(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.ArrayType:
		return isDiagnosticType(typed.Elt)
	case *ast.MapType:
		return isDiagnosticType(typed.Value)
	default:
		return false
	}
}

func hasField(literal *ast.CompositeLit, name string) bool {
	for _, element := range literal.Elts {
		keyValue, isKeyValue := element.(*ast.KeyValueExpr)
		if !isKeyValue {
			continue
		}
		if key, isIdent := keyValue.Key.(*ast.Ident); isIdent && key.Name == name {
			return true
		}
	}

	return false
}
