package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// BucketToConsumerLink declares the celerity/bucket -> celerity/consumer
// relationship: the consumer processes object-storage events from the bucket. On
// aws-serverless the consumer is absorbed into its handler, whose Lambda is
// triggered directly by an S3 event notification on the bucket.
func BucketToConsumerLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/bucket",
		ResourceTypeB:    "celerity/consumer",
		PlainTextSummary: "Delivers bucket events to a consumer.",
		FormattedSummary: "Delivers `celerity/bucket` events to a `celerity/consumer`.",
		PlainTextDescription: "Configures the consumer to process object-storage events from the bucket. " +
			"On aws-serverless the consumer's handler is triggered directly by an S3 event notification.",
		FormattedDescription: "Configures the `celerity/consumer` to process object-storage events from " +
			"the `celerity/bucket`. On aws-serverless the consumer's handler is triggered directly by an " +
			"S3 event notification.",
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			"celerity/consumer::" + handler.AnnotationKeyConsumerSourceBucket: {
				Name:      handler.AnnotationKeyConsumerSourceBucket,
				Label:     "Consumer bucket",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Description: "Names the in-blueprint bucket the consumer listens to. Only required to " +
					"disambiguate when the consumer matches several buckets by link selector; with a single " +
					"linked bucket the consumer listens to it by default.",
			},
			"celerity/consumer::" + handler.AnnotationKeyConsumerBucketEvents: {
				Name:      handler.AnnotationKeyConsumerBucketEvents,
				Label:     "Consumer bucket events",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Examples: []*core.ScalarValue{
					core.ScalarFromString("created"),
					core.ScalarFromString("created,deleted"),
				},
				Description: "Comma-separated set of object-storage events that trigger the consumer. " +
					"Allowed values are created, deleted and metadataUpdated.",
			},
		},
	}
}
