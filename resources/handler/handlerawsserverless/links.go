package handlerawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// Copies the abstract handler's linkSelector as-is onto the emitted Lambda.
func declareOutboundLinks(r *handler.ResolvedHandler, lambda *schema.Resource) {
	if r.Resource.LinkSelector != nil {
		lambda.LinkSelector = r.Resource.LinkSelector
	}
}
