package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// DatastoreToConsumerLink declares the celerity/datastore -> celerity/consumer
// relationship: the consumer processes change events from the datastore's stream.
// On aws-serverless the consumer is absorbed into its handler, whose Lambda is
// wired to the datastore's DynamoDB stream by an event source mapping.
func DatastoreToConsumerLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/datastore",
		ResourceTypeB:    "celerity/consumer",
		PlainTextSummary: "Delivers datastore change events to a consumer.",
		FormattedSummary: "Delivers `celerity/datastore` change events to a `celerity/consumer`.",
		PlainTextDescription: "Configures the consumer to process change events from the datastore's " +
			"stream. On aws-serverless the consumer's handler is wired to the datastore's DynamoDB stream " +
			"by an event source mapping.",
		FormattedDescription: "Configures the `celerity/consumer` to process change events from the " +
			"`celerity/datastore`'s stream. On aws-serverless the consumer's handler is wired to the " +
			"datastore's DynamoDB stream by an event source mapping.",
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			"celerity/consumer::" + handler.AnnotationKeyConsumerSourceDatastore: {
				Name:      handler.AnnotationKeyConsumerSourceDatastore,
				Label:     "Consumer datastore",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Names the in-blueprint datastore the consumer listens to. Only required to " +
					"disambiguate when the consumer matches several datastores by link selector; with a " +
					"single linked datastore the consumer listens to it by default.",
			},
			"celerity/consumer::" + handler.AnnotationKeyConsumerDatastoreStartFromBeginning: {
				Name:      handler.AnnotationKeyConsumerDatastoreStartFromBeginning,
				Label:     "Start from beginning",
				Type:      core.ScalarTypeBool,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Whether the consumer starts processing from the earliest available point in " +
					"the stream (TRIM_HORIZON) rather than only new events (LATEST). Supported on " +
					"aws-serverless.",
			},
		},
	}
}
