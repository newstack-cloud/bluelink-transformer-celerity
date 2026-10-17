// Package vpcaws is the AWS implementation of celerity/vpc.
//
// The networking fabric is the same whichever compute model an app uses, so this serves the containerised
// aws target as well as aws-serverless.
package vpcaws

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Implementation is what the registry composes with vpc.Resource, for one of
// the AWS deploy targets.
func Implementation(target string) shared.TargetImplementation {
	propertyMap := PropertyMap()

	return shared.TargetImplementation{
		Target:      target,
		Emitter:     transformutils.TypedEmitter(shared.BindTarget(target, Emit)),
		PropertyMap: propertyMap,
		Rewriter:    Rewriter(&propertyMap),
	}
}
