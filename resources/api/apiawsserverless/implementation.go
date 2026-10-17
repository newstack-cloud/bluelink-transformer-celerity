// Package apiawsserverless is the aws-serverless implementation of celerity/api.
//
// API Gateway v2 fronts Lambda, so this is specific to the serverless target
// rather than shared with the containerised one, which fronts services with a
// load balancer instead.
package apiawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Implementation is what the registry composes with api.Resource.
func Implementation() shared.TargetImplementation {
	propertyMap := PropertyMap()

	return shared.TargetImplementation{
		Target:      shared.AWSServerless,
		Emitter:     transformutils.TypedEmitter(Emit),
		PropertyMap: propertyMap,
		Rewriter:    Rewriter(&propertyMap),
	}
}
