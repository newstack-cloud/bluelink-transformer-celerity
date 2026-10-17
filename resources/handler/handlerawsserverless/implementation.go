// Package handlerawsserverless is the aws-serverless implementation of
// celerity/handler: the Lambda function a handler's code runs in, the triggers
// that invoke it, and the shared execution roles they reference.
//
// Nothing here is reusable by another AWS target. A handler is where application
// code lives, so what it maps to is the deploy target's compute model rather than
// a service that model happens to use: on the containerised aws target the same
// abstract handler becomes a task, with its own triggers and its own identity.
package handlerawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Implementation is what the registry composes with handler.Resource.
func Implementation(deps *shared.Dependencies) shared.TargetImplementation {
	propertyMap := PropertyMap()
	emitter := newEmitter(deps)

	return shared.TargetImplementation{
		Target:      shared.AWSServerless,
		Emitter:     transformutils.TypedEmitter(emitter.emit),
		PropertyMap: propertyMap,
		Rewriter:    newRewriterRegistration(&propertyMap),
	}
}

// Registers the handler's rewriter for the aws-serverless target. It composes two
// contributions onto the SAME (target, *handler.ResolvedHandler) registry slot:
//
//  1. the declarative PropertyMap rewriter (handler.spec.* -> concrete Lambda),
//     which also seeds the capability matrix entry, and
//  2. one extra rewriter per absorbed topic-source consumer that maps
//     ${<consumer>.spec.subscriberId} to the concrete SQS fan-out queue created
//     for that consumer.
func newRewriterRegistration(
	pm *transformutils.PropertyMap,
) transformutils.RewriterRegistration {
	base := transformutils.RewriterFromPropertyMap(
		pm,
		func(r *handler.ResolvedHandler) string {
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
			func(r *handler.ResolvedHandler) []transformutils.ResourcePropertyRewriter {
				rewriters := []transformutils.ResourcePropertyRewriter{
					pm.Rewriter(r.Name, lambdaFuncResourceName(r.Name)),
				}
				return append(rewriters, consumerSubscriberRewriters(r)...)
			},
		)
	}
}
