package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/queue"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// BucketToQueueLink declares the celerity/bucket -> celerity/queue relationship:
// the queue receives object-storage event notifications from the bucket. On
// aws-serverless this configures an S3 bucket notification targeting the queue.
func BucketToQueueLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/bucket",
		ResourceTypeB:    "celerity/queue",
		PlainTextSummary: "Sends bucket event notifications to a queue.",
		FormattedSummary: "Sends `celerity/bucket` event notifications to a `celerity/queue`.",
		PlainTextDescription: "Configures the queue to receive object-storage event notifications from " +
			"the bucket (for example object creation, deletion and updates). On aws-serverless this maps " +
			"to an S3 bucket notification targeting the queue.",
		FormattedDescription: "Configures the `celerity/queue` to receive object-storage event " +
			"notifications from the `celerity/bucket` (for example object creation, deletion and updates). " +
			"On aws-serverless this maps to an S3 bucket notification targeting the queue.",
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			"celerity/queue::" + queue.AnnotationKeyBucketEvents: {
				Name:      queue.AnnotationKeyBucketEvents,
				Label:     "Bucket events",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Examples: []*core.ScalarValue{
					core.ScalarFromString("created"),
					core.ScalarFromString("created,deleted"),
				},
				Description: "Comma-separated set of object-storage events that flow from the bucket into " +
					"the queue. Allowed values are created, deleted and metadataUpdated. Defaults to created.",
			},
			"celerity/queue::" + queue.AnnotationKeyBucketFilterPrefix: {
				Name:      queue.AnnotationKeyBucketFilterPrefix,
				Label:     "Bucket filter prefix",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Examples: []*core.ScalarValue{
					core.ScalarFromString("incoming/"),
				},
				Description: "Restricts notifications to object keys beginning with this prefix.",
			},
			"celerity/queue::" + queue.AnnotationKeyBucketFilterSuffix: {
				Name:      queue.AnnotationKeyBucketFilterSuffix,
				Label:     "Bucket filter suffix",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Examples: []*core.ScalarValue{
					core.ScalarFromString(".json"),
				},
				Description: "Restricts notifications to object keys ending with this suffix.",
			},
		},
	}
}
