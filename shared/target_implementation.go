package shared

import (
	"context"

	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// TargetImplementation is one deploy target's implementation of one abstract
// resource: what it emits, how its references rewrite, and the abstract paths a
// reference may target.
//
// An abstract resource package holds the portable half of a resource (its
// schema and its resolve) and knows nothing about any target. Each target's half
// lives in its own package beneath it, and the two are composed where the
// registry is assembled. That keeps the dependency pointing one way, target
// package to abstract package, which is what lets a target be added without
// touching the portable half.
type TargetImplementation struct {
	Target      string
	Emitter     transformutils.EmitterRegistration
	PropertyMap transformutils.PropertyMap
	Rewriter    transformutils.RewriterRegistration
}

// ApplyTargetImplementations fills in an abstract resource definition's
// per-target maps. Called by an abstract resource's Resource constructor with
// whatever targets the registry passed it.
func ApplyTargetImplementations(
	definition *transformerv1.AbstractResourceDefinition,
	implementations []TargetImplementation,
) *transformerv1.AbstractResourceDefinition {
	if len(implementations) == 0 {
		return definition
	}

	definition.Emitters = map[string]transformutils.EmitterRegistration{}
	definition.PropertyMaps = map[string]transformutils.PropertyMap{}
	definition.Rewriters = map[string]transformutils.RewriterRegistration{}

	for _, implementation := range implementations {
		if implementation.Emitter != nil {
			definition.Emitters[implementation.Target] = implementation.Emitter
		}
		definition.PropertyMaps[implementation.Target] = implementation.PropertyMap
		if implementation.Rewriter != nil {
			definition.Rewriters[implementation.Target] = implementation.Rewriter
		}
	}

	return definition
}

// TargetEmit is an emit written by a package that serves more than one deploy
// target, and so is told which one it is emitting for rather than assuming.
//
// The target is what a diagnostic about a structural field names: which field a
// target has to know the value of at transform time is a property of the target,
// not of the abstract resource, so a package serving several cannot name one.
type TargetEmit[PR any] func(
	ctx context.Context,
	run *transformutils.Run,
	target string,
	r PR,
	rewriter transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error)

// BindTarget fixes a TargetEmit to one deploy target, giving back the signature
// transformutils.TypedEmitter takes.
func BindTarget[PR any](target string, emit TargetEmit[PR]) func(
	ctx context.Context,
	run *transformutils.Run,
	r PR,
	rewriter transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	return func(
		ctx context.Context,
		run *transformutils.Run,
		r PR,
		rewriter transformutils.ResourcePropertyRewriter,
	) (*transformutils.EmitResult, error) {
		return emit(ctx, run, target, r, rewriter)
	}
}
