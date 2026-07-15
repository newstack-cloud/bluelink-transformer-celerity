package transformer

import (
	"maps"

	"github.com/newstack-cloud/bluelink-transformer-celerity/links"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/api"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/cache"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/config"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/consumer"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handlerconfig"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/schedule"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func NewTransformer(deps *shared.Dependencies) transform.SpecTransformer {
	transformName := "celerity-2026-02-27-draft"

	return &transformerv1.TransformerPluginDefinition{
		TransformName:               transformName,
		TransformerConfigDefinition: configDefinition(),
		AbstractResources: map[string]*transformerv1.AbstractResourceDefinition{
			"celerity/api":           api.Resource(),
			"celerity/handler":       handler.Resource(deps),
			"celerity/bucket":        bucket.Resource(),
			"celerity/cache":         cache.Resource(),
			"celerity/queue":         queue.Resource(),
			"celerity/topic":         topic.Resource(),
			"celerity/datastore":     datastore.Resource(),
			"celerity/sqlDatabase":   sqldatabase.Resource(),
			"celerity/consumer":      consumer.Resource(),
			"celerity/schedule":      schedule.Resource(),
			"celerity/vpc":           vpc.Resource(),
			"celerity/config":        config.Resource(),
			"celerity/handlerConfig": handlerconfig.Resource(),
		},
		AbstractLinks: map[string]*transformerv1.AbstractLinkDefinition{
			// API links
			"celerity/api::celerity/handler": links.APIToHandlerLink(),
			"celerity/api::celerity/config":  links.APIToConfigLink(),
			// Handler links
			"celerity/handler::celerity/queue":         links.HandlerToQueueLink(),
			"celerity/handler::celerity/topic":         links.HandlerToTopicLink(),
			"celerity/handler::celerity/datastore":     links.HandlerToDatastoreLink(),
			"celerity/handler::celerity/sqlDatabase":   links.HandlerToSqlDatabaseLink(),
			"celerity/handler::celerity/bucket":        links.HandlerToBucketLink(),
			"celerity/handler::celerity/cache":         links.HandlerToCacheLink(),
			"celerity/handler::celerity/config":        links.HandlerToConfigLink(),
			"celerity/handler::celerity/handlerConfig": links.HandlerToHandlerConfigLink(),
			// Queue links
			"celerity/queue::celerity/queue":    links.QueueToQueueLink(),
			"celerity/queue::celerity/consumer": links.QueueToConsumerLink(),
			"celerity/queue::celerity/topic":    links.QueueToTopicLink(),
			// Bucket links
			"celerity/bucket::celerity/queue":    links.BucketToQueueLink(),
			"celerity/bucket::celerity/topic":    links.BucketToTopicLink(),
			"celerity/bucket::celerity/consumer": links.BucketToConsumerLink(),
			// Consumer links
			"celerity/consumer::celerity/handler": links.ConsumerToHandlerLink(),
			"celerity/consumer::celerity/config":  links.ConsumerToConfigLink(),
			// Datastore links
			"celerity/datastore::celerity/consumer": links.DatastoreToConsumerLink(),
			// VPC links
			"celerity/vpc::celerity/handler":     links.VPCToHandlerLink(),
			"celerity/vpc::celerity/cache":       links.VPCToCacheLink(),
			"celerity/vpc::celerity/sqlDatabase": links.VPCToSqlDatabaseLink(),
			// Schedule links
			"celerity/schedule::celerity/handler": links.ScheduleToHandlerLink(),
			"celerity/schedule::celerity/config":  links.ScheduleToConfigLink(),
		},
		Aggregators: map[string]transformutils.Aggregator{
			shared.AWSServerless: createAWSServerlessAggregator(),
		},
		OnRun: createRunHook(deps),
	}
}

func configDefinition() *core.ConfigDefinition {
	fields := map[string]*core.ConfigFieldDefinition{}
	maps.Copy(fields, aws.TransformerConfigFields())

	return &core.ConfigDefinition{
		Fields: fields,
	}
}
