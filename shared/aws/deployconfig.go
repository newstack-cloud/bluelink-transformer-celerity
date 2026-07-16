package aws

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// ResolveDeployConfig resolves an aws-serverless deploy-config value keyed by a
// prefix, an optional per-resource name, and a suffix, applying the standard
// precedence from aws-serverless.md §2.1: the per-resource key
// (`<prefix>.<name>.<suffix>`) wins over the global key (`<prefix>.<suffix>`).
//
// The prefix absorbs any fixed infix (e.g. "aws.sns.fifo") and the suffix
// absorbs any post-name path (e.g. "statusLogging.0.protocol"), so this single
// helper covers the flat, infix, and indexed key shapes. It returns nil, false
// when neither form is set. Pass an empty name to look up the global key only.
func ResolveDeployConfig(
	ctx transform.Context,
	prefix, name, suffix string,
) (*core.ScalarValue, bool) {
	if ctx == nil {
		return nil, false
	}
	if name != "" {
		if v, ok := ctx.TransformerConfigVariable(perResourceKey(prefix, name, suffix)); ok && !core.IsScalarNil(v) {
			return v, true
		}
	}
	if v, ok := ctx.TransformerConfigVariable(globalKey(prefix, suffix)); ok && !core.IsScalarNil(v) {
		return v, true
	}
	return nil, false
}

// ResolveDeployConfigNode is ResolveDeployConfig with the resolved scalar wrapped
// in a MappingNode, ready to place directly in an emitted resource spec.
func ResolveDeployConfigNode(
	ctx transform.Context,
	prefix, name, suffix string,
) (*core.MappingNode, bool) {
	v, ok := ResolveDeployConfig(ctx, prefix, name, suffix)
	if !ok {
		return nil, false
	}
	return &core.MappingNode{Scalar: v}, true
}

func perResourceKey(prefix, name, suffix string) string {
	return prefix + "." + name + "." + suffix
}

func globalKey(prefix, suffix string) string {
	return prefix + "." + suffix
}
