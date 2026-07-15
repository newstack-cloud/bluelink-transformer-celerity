package build

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

// fakeTransformContext is a stub implementation of transform.Context used by
// the build package's test suites. It exposes only a TransformerConfigVariable
// map; context variables return empty since the loader code under test does
// not consult them.
type fakeTransformContext struct {
	configVars map[string]*core.ScalarValue
}

func newFakeTransformContext(
	stringVars map[string]string,
	boolVars map[string]bool,
) *fakeTransformContext {
	vars := make(map[string]*core.ScalarValue, len(stringVars)+len(boolVars))
	for k, v := range stringVars {
		vars[k] = core.ScalarFromString(v)
	}
	for k, v := range boolVars {
		vars[k] = core.ScalarFromBool(v)
	}
	return &fakeTransformContext{configVars: vars}
}

func (f *fakeTransformContext) TransformerConfigVariable(name string) (*core.ScalarValue, bool) {
	v, ok := f.configVars[name]
	return v, ok
}

func (f *fakeTransformContext) TransformerConfigVariables() map[string]*core.ScalarValue {
	return f.configVars
}

func (f *fakeTransformContext) ContextVariable(name string) (*core.ScalarValue, bool) {
	return nil, false
}

func (f *fakeTransformContext) ContextVariables() map[string]*core.ScalarValue {
	return map[string]*core.ScalarValue{}
}
