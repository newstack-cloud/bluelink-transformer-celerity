// Package queueaws is the AWS implementation of celerity/queue: an SQS queue,
// plus the provider annotations SQS links consume.
//
// SQS is the same whichever compute model an app uses, so nothing here is
// specific to one AWS deploy target. It declares no Implementation of its own
// because the only AWS target registered so far, aws-serverless, needs more
// than an SQS queue: see queueawsserverless, which composes these pieces with
// the topic forwarder.
package queueaws
