package handler

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// Wires the emitted Lambda function to the concrete
// backing-target resources the handler links to.
//
// The mechanism is deliberately generic: the abstract handler's linkSelector
// selected its backing targets (queue, topic, datastore, bucket, config) by
// label, and each of those emits a concrete resource that preserves those same
// labels. Copying the linkSelector verbatim onto the emitted Lambda therefore
// re-establishes every one of those edges against the concrete resources, and
// the corresponding bluelink-provider-aws links (aws/lambda/function::aws/sqs/queue,
// ::aws/sns/topic, ::aws/dynamodb/table, ::aws/s3/bucket, ::aws/ssm/parameterTree,
// ::aws/secretsmanager/secret) do the wiring; IAM statements, env vars and event
// source mappings are handled at deploy time.
//
// Per-target AWS link annotations (for example aws.lambda.sqs.<queue>.accessLevel)
// are only needed to override provider defaults; those are layered on here as the
// backing-target slices grow. The provider defaults (SQS send access, a
// SQS_QUEUE_<name> env var, and so on) apply when no annotation is stamped.
func declareOutboundLinks(r *ResolvedHandler, lambda *schema.Resource) {
	if r.Resource.LinkSelector != nil {
		lambda.LinkSelector = r.Resource.LinkSelector
	}
}
