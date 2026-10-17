// Package queueawsserverless is the aws-serverless implementation of
// celerity/queue: the SQS queue from queueaws, plus the intermediary Lambda that
// forwards the queue's messages to any topic it links to.
//
// Forwarding is specific to this target rather than to AWS. SQS has no native
// SNS forwarding, so something has to poll the queue and republish, and on
// aws-serverless that something is a function.
package queueawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue/queueaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Implementation is what the registry composes with queue.Resource.
//
// The property map and rewriter are queueaws's unchanged: the forwarder is not
// addressable from a blueprint, so it adds nothing a reference can point at.
func Implementation() shared.TargetImplementation {
	propertyMap := queueaws.PropertyMap()

	return shared.TargetImplementation{
		Target:      shared.AWSServerless,
		Emitter:     transformutils.TypedEmitter(Emit),
		PropertyMap: propertyMap,
		Rewriter:    queueaws.Rewriter(&propertyMap),
	}
}
