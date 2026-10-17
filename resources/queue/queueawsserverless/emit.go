package queueawsserverless

import (
	"context"
	"fmt"
	"maps"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue/queueaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Emit builds the SQS queue, then adds the topic forwarder when the queue
// forwards to any topic. A queue that emits nothing, whether external or blocked
// by a deferred structural value, gets no forwarder either.
func Emit(
	ctx context.Context,
	run *transformutils.Run,
	r *queue.ResolvedQueue,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	result, err := queueaws.Emit(ctx, run, shared.AWSServerless, r, rw)
	if err != nil {
		return nil, err
	}

	concrete, emitted := result.Resources[queueaws.ConcreteResourceName(r.Name)]
	if !emitted {
		result.Diagnostics = append(result.Diagnostics, unforwardedDiagnostics(r)...)

		return result, nil
	}

	diagnostics, err := addTopicForwarder(
		r, shared.ResolveAppName(run), concrete, result.Resources,
	)
	if err != nil {
		return nil, err
	}
	result.Diagnostics = append(result.Diagnostics, diagnostics...)

	return result, nil
}

// addTopicForwarder provisions the single forwarder (function + role) for a queue
// that forwards to one or more topics, and binds the source queue to it. It emits
// nothing (and returns an error diagnostic) when the queue is also consumed. An
// SQS queue cannot feed both a consumer's poller and the forwarder's poller
// (competing consumers) or when the forwarding edges do not carry any selector labels to
// resolve the topics.
func addTopicForwarder(
	r *queue.ResolvedQueue,
	appName string,
	concrete *schema.Resource,
	resources map[string]*schema.Resource,
) ([]*core.Diagnostic, error) {
	if len(r.TopicForwards) == 0 {
		return nil, nil
	}

	concrete.LinkSelector = linkSelectorWithForward(r)

	if r.Consumed {
		return []*core.Diagnostic{{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"celerity/queue %q is both consumed and forwarded to a topic; on aws-serverless a queue "+
					"feeds a single poller, so the two would compete for its messages. No forwarder was "+
					"emitted. Use separate queues, or subscribe the consumer to the topic instead",
				r.Name,
			),
			Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
		}}, nil
	}

	if len(forwardSelectorLabels(r.TopicForwards)) == 0 {
		return []*core.Diagnostic{{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"celerity/queue %q forwards to a topic but the link carries no label selector to resolve "+
					"the target topic; no forwarder was emitted",
				r.Name,
			),
			Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
		}}, nil
	}

	forwarder, err := buildTopicForwarder(r.Name, appName, r.TopicForwards)
	if err != nil {
		return nil, err
	}
	maps.Copy(resources, forwarder)

	return nil, nil
}

// Returns the source queue's link selector augmented with
// the synthetic forward label. Bluelink label matching is per-label (each label
// forms an independent selector group), so adding the forward label creates a
// match to the forwarder function without disturbing the existing topic matches.
func linkSelectorWithForward(r *queue.ResolvedQueue) *schema.LinkSelector {
	labels := map[string]string{}
	if r.Resource.LinkSelector != nil && r.Resource.LinkSelector.ByLabel != nil {
		maps.Copy(labels, r.Resource.LinkSelector.ByLabel.Values)
	}
	labels[forwardLabelKey(r.Name)] = "true"

	return &schema.LinkSelector{ByLabel: &schema.StringMap{Values: labels}}
}

func forwardSelectorLabels(edges []*queue.TopicForwardEdge) map[string]string {
	labels := map[string]string{}
	for _, edge := range edges {
		maps.Copy(labels, edge.SelectorLabels)
	}

	return labels
}

// Reports forwarding declared on an external queue, which emits nothing to
// forward from.
//
// The two other reasons a forwarder is not emitted each raise an error, so
// leaving this one quiet would make an external queue the single case where
// declared forwarding has no effect and nothing says so. The remaining case, a
// queue blocked by a deferred structural value, already carries its own error.
func unforwardedDiagnostics(r *queue.ResolvedQueue) []*core.Diagnostic {
	if len(r.TopicForwards) == 0 || r.External == nil {
		return nil
	}

	return []*core.Diagnostic{{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/queue %q forwards to a topic but no queue is emitted for it, so "+
				"nothing forwards the messages. An external queue is one Celerity does not "+
				"own, and forwarding from it would mean consuming it; publish to the topic "+
				"from a handler instead",
			r.Name,
		),
		Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
	}}
}
