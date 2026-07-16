package handler

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity handler.
func Resource(deps *shared.Dependencies) *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	awsServerlessEmitter := newAWSServerlessEmitter(deps)

	return &transformerv1.AbstractResourceDefinition{
		Type:                 "celerity/handler",
		Label:                "Celerity Handler",
		PlainTextSummary:     "",
		FormattedSummary:     "",
		PlainTextDescription: "",
		FormattedDescription: "",
		Schema:               handlerResourceSchema(),
		Resolve:              resolveHandler,
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(
				awsServerlessEmitter.emit,
			),
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: newHandlerRewriterRegistration(&awsPropertyMap),
		},
	}
}

// Registers the handler's rewriter for the aws-serverless target. It composes two
// contributions onto the SAME (target, *ResolvedHandler) registry slot:
//
//  1. the declarative PropertyMap rewriter (handler.spec.* -> concrete Lambda),
//     which also seeds the capability matrix entry, and
//  2. one extra rewriter per absorbed topic-source consumer that maps
//     ${<consumer>.spec.subscriberId} to the concrete SQS fan-out queue created
//     for that consumer.
//
// The consumer is a contributory resource (its own rewriter is never chained),
// so the handler — a primary whose rewriter IS chained across the pipeline — is
// the only place these consumer output references can be resolved.
func newHandlerRewriterRegistration(
	pm *transformutils.PropertyMap,
) transformutils.RewriterRegistration {
	base := transformutils.RewriterFromPropertyMap(
		pm,
		func(r *ResolvedHandler) string {
			return lambdaFuncResourceName(r.Name)
		},
	)
	return func(registry *transformutils.TransformerRegistry, target transformutils.Target) {
		// base registers the PropertyMap rewriter AND the capability matrix entry.
		base(registry, target)
		// Replace the single-rewriter factory with one that keeps the PropertyMap
		// rewriter and appends the per-consumer subscriberId rewriters. The
		// capability entry base() registered is left in place.
		transformutils.RegisterRewriter(
			registry,
			target,
			func(r *ResolvedHandler) []transformutils.ResourcePropertyRewriter {
				rewriters := []transformutils.ResourcePropertyRewriter{
					pm.Rewriter(r.Name, lambdaFuncResourceName(r.Name)),
				}
				return append(rewriters, consumerSubscriberRewriters(r)...)
			},
		)
	}
}
