package handler

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// AnnotationKeyHTTPHandler marks a handler to handle HTTP requests.
	AnnotationKeyHTTPHandler = "celerity.handler.http"

	// AnnotationKeyWebSocketHandler marks a handler to handle WebSocket connections.
	AnnotationKeyWebSocketHandler = "celerity.handler.websocket"

	// AnnotationKeyConsumerHandler marks a handler to handle events from an event consumer.
	AnnotationKeyConsumerHandler = "celerity.handler.consumer"

	// AnnotationKeyScheduleHandler marks a handler to handle scheduled events.
	AnnotationKeyScheduleHandler = "celerity.handler.schedule"

	// AnnotationKeyConsumerRoute specifies the route for a consumer handler.
	AnnotationKeyConsumerRoute = "celerity.handler.consumer.route"

	// AnnotationKeyVPCSubnetType specifies the subnet type for a handler's VPC configuration.
	AnnotationKeyVPCSubnetType = "celerity.handler.vpc.subnetType"

	// AnnotationKeyHTTPMethod is the HTTP method an HTTP handler responds to.
	AnnotationKeyHTTPMethod = "celerity.handler.http.method"

	// AnnotationKeyHTTPPath is the HTTP path an HTTP handler responds to.
	AnnotationKeyHTTPPath = "celerity.handler.http.path"

	// AnnotationKeyWebSocketRoute is the route key a WebSocket handler responds to.
	AnnotationKeyWebSocketRoute = "celerity.handler.websocket.route"

	// AnnotationKeyGuardProtectedBy lists the guard (or ordered, comma-separated
	// list of guards) that protects an HTTP handler.
	AnnotationKeyGuardProtectedBy = "celerity.handler.guard.protectedBy"

	// AnnotationKeyPublic marks an HTTP handler as public, opting it out of
	// authentication even when a default guard is set.
	AnnotationKeyPublic = "celerity.handler.public"

	// AnnotationKeyGuardCustom names the custom auth guard that this handler
	// implements for the linked API.
	AnnotationKeyGuardCustom = "celerity.handler.guard.custom"
)

const (
	// SubnetTypePrivate places a handler's VPC configuration in private subnets.
	SubnetTypePrivate = "private"

	// SubnetTypePublic places a handler's VPC configuration in public subnets.
	SubnetTypePublic = "public"
)

func sharedParentAnnotations(abstractName string, category string) *core.MappingNode {
	return core.MappingNodeFields(
		transformutils.AnnotationSourceAbstractName, core.MappingNodeFromString(abstractName),
		transformutils.AnnotationSourceAbstractType, core.MappingNodeFromString("celerity/handler"),
		transformutils.AnnotationResourceCategory, core.MappingNodeFromString(category),
	)
}
