package transformer

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/links"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/api"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/api/apiawsserverless"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/bucket/bucketaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/cache"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/cache/cacheaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/config"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/config/configaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/consumer"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/datastore/datastoreaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler/handlerawsserverless"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handlerconfig"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue/queueawsserverless"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/schedule"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase/sqldatabaseawsserverless"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic/topicaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc/vpcaws"
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
			"celerity/api":           api.Resource(apiawsserverless.Implementation()),
			"celerity/handler":       handler.Resource(handlerawsserverless.Implementation(deps)),
			"celerity/bucket":        bucket.Resource(bucketaws.Implementation(shared.AWSServerless)),
			"celerity/cache":         cache.Resource(cacheaws.Implementation(shared.AWSServerless)),
			"celerity/queue":         queue.Resource(queueawsserverless.Implementation()),
			"celerity/topic":         topic.Resource(topicaws.Implementation(shared.AWSServerless)),
			"celerity/datastore":     datastore.Resource(datastoreaws.Implementation(shared.AWSServerless)),
			"celerity/sqlDatabase":   sqldatabase.Resource(sqldatabaseawsserverless.Implementation()),
			"celerity/consumer":      consumer.Resource(),
			"celerity/schedule":      schedule.Resource(),
			"celerity/vpc":           vpc.Resource(vpcaws.Implementation(shared.AWSServerless)),
			"celerity/config":        config.Resource(configaws.Implementation(shared.AWSServerless)),
			"celerity/handlerConfig": handlerconfig.Resource(),
		},
		AbstractLinks: abstractLinks(),
		Aggregators: map[string]transformutils.Aggregator{
			shared.AWSServerless: createAWSServerlessAggregator(),
		},
		OnRun: createRunHook(deps),
	}
}

// abstractLinks is the single registry of every abstract link definition the
// transformer recognises. Keyed by "<resourceTypeA>::<resourceTypeB>", it is
// consumed by NewTransformer and exercised by the registry-invariant test.
func abstractLinks() map[string]*transformerv1.AbstractLinkDefinition {
	return map[string]*transformerv1.AbstractLinkDefinition{
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
		"celerity/vpc::celerity/api":         links.VPCToAPILink(),
		"celerity/vpc::celerity/consumer":    links.VPCToConsumerLink(),
		"celerity/vpc::celerity/schedule":    links.VPCToScheduleLink(),
		// Schedule links
		"celerity/schedule::celerity/handler": links.ScheduleToHandlerLink(),
		"celerity/schedule::celerity/config":  links.ScheduleToConfigLink(),
	}
}

func configDefinition() *core.ConfigDefinition {
	return &core.ConfigDefinition{
		Fields: aws.AllTransformerConfigFields(),
	}
}
