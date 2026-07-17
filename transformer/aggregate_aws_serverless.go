package transformer

import (
	"context"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/consumer"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handlerconfig"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/schedule"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSServerlessAggregator() transformutils.Aggregator {
	return func(
		ctx context.Context,
		run *transformutils.Run,
		resolved []transformutils.ResolvedResource,
	) *transformutils.EmitPlan {
		primaries := []transformutils.ResolvedResource{}
		for _, r := range resolved {
			switch r.(type) {
			case *handlerconfig.ResolvedHandlerConfig,
				*schedule.ResolvedSchedule,
				*consumer.ResolvedConsumer:
				// Contributor-only resources.
				continue
			default:
				primaries = append(primaries, r)
			}
		}

		manifest, _ := transformutils.Use[*build.Manifest](run)
		parents := []transformutils.SharedParent{}
		parents = append(
			parents,
			handler.AWSServerlessSharedParents(ctx, primaries, manifest)...,
		)

		// The internal resources namespace config store: one aws/ssm/parameterTree
		// mapping each linked backing resource's configKey to its physical id, read by
		// handlers at runtime (see the store-read grant + STORE_ID/KIND env vars).
		store, err := collectResourcesStore(primaries, shared.ResourceLinksStorePath(run))
		if err != nil {
			return &transformutils.EmitPlan{Primaries: primaries, SharedParents: parents}
		}
		if store != nil {
			parents = append(parents, *store)
		}

		return &transformutils.EmitPlan{
			Primaries:     primaries,
			SharedParents: parents,
		}
	}
}
