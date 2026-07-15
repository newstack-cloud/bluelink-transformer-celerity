package handler

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// AnnotationKeyHTTPHandler is the annotation key that is used to mark
	// a handler to handle HTTP requests.
	AnnotationKeyHTTPHandler = "celerity.handler.http"

	// AnnotationKeyWebSocketHandler is the annotation key that is used to mark
	// a handler to handle WebSocket connections.
	AnnotationKeyWebSocketHandler = "celerity.handler.websocket"

	// AnnotationKeyConsumerHandler is the annotation key that is used to mark
	// a handler to handle events from an event consumer.
	AnnotationKeyConsumerHandler = "celerity.handler.consumer"

	// AnnotationKeyScheduleHandler is the annotation key that is used to mark
	// a handler to handle scheduled events.
	AnnotationKeyScheduleHandler = "celerity.handler.schedule"

	// AnnotationKeyConsumerRoute is the annotation key that is used to
	// specify the route for a consumer handler.
	AnnotationKeyConsumerRoute = "celerity.handler.consumer.route"

	// AnnotationKeyVPCSubnetType is the annotation key that is
	// used to specify the subnet type for a handler's VPC configuration.
	AnnotationKeyVPCSubnetType = "celerity.handler.vpc.subnetType"

	// AnnotationKeyHTTPMethod is the annotation key for the HTTP method
	// an HTTP handler responds to.
	AnnotationKeyHTTPMethod = "celerity.handler.http.method"

	// AnnotationKeyHTTPPath is the annotation key for the HTTP path
	// an HTTP handler responds to.
	AnnotationKeyHTTPPath = "celerity.handler.http.path"

	// AnnotationKeyWebSocketRoute is the annotation key for the route key
	// a WebSocket handler responds to.
	AnnotationKeyWebSocketRoute = "celerity.handler.websocket.route"

	// AnnotationKeyGuardProtectedBy is the annotation key listing the guard
	// (or ordered, comma-separated list of guards) that protects an HTTP handler.
	AnnotationKeyGuardProtectedBy = "celerity.handler.guard.protectedBy"

	// AnnotationKeyPublic is the annotation key that marks an HTTP handler as
	// public, opting it out of authentication even when a default guard is set.
	AnnotationKeyPublic = "celerity.handler.public"

	// AnnotationKeyGuardCustom is the annotation key naming the custom auth guard
	// that this handler implements for the linked API.
	AnnotationKeyGuardCustom = "celerity.handler.guard.custom"
)

const (
	// SubnetTypePrivate is the value used to indicate that a handler's VPC configuration should use private subnets.
	SubnetTypePrivate = "private"

	// SubnetTypePublic is the value used to indicate that a handler's VPC configuration should use public subnets.
	SubnetTypePublic = "public"
)

func sharedParentAnnotations(abstractName string, category string) *core.MappingNode {
	return core.MappingNodeFields(
		transformutils.AnnotationSourceAbstractName, core.MappingNodeFromString(abstractName),
		transformutils.AnnotationSourceAbstractType, core.MappingNodeFromString("celerity/handler"),
		transformutils.AnnotationResourceCategory, core.MappingNodeFromString(category),
	)
}
