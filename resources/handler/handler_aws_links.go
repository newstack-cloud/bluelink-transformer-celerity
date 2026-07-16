package handler

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// Copies the abstract handler's linkSelector verbatim onto the emitted Lambda.
// The handler selected its backing targets (queue, topic, datastore, bucket,
// config) by label, and each emits a concrete resource preserving those labels, so
// copying the selector re-establishes every edge against the concrete resources;
// the bluelink-provider-aws links then do the wiring (IAM statements, env vars and
// event source mappings at deploy time).
//
// Per-target AWS link annotations (e.g. aws.lambda.sqs.<queue>.accessLevel) are
// only needed to override provider defaults and are layered on here as the
// backing-target slices grow.
func declareOutboundLinks(r *ResolvedHandler, lambda *schema.Resource) {
	if r.Resource.LinkSelector != nil {
		lambda.LinkSelector = r.Resource.LinkSelector
	}
}
