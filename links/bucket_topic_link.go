package links

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/topic"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// BucketToTopicLink declares the celerity/bucket -> celerity/topic relationship:
// the topic receives object-storage event notifications from the bucket. On
// aws-serverless this configures an S3 bucket notification targeting the topic.
func BucketToTopicLink() *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:    "celerity/bucket",
		ResourceTypeB:    "celerity/topic",
		PlainTextSummary: "Sends bucket event notifications to a topic.",
		FormattedSummary: "Sends `celerity/bucket` event notifications to a `celerity/topic`.",
		PlainTextDescription: "Configures the topic to receive object-storage event notifications from " +
			"the bucket (for example object creation, deletion and updates). On aws-serverless this maps " +
			"to an S3 bucket notification targeting the topic.",
		FormattedDescription: "Configures the `celerity/topic` to receive object-storage event " +
			"notifications from the `celerity/bucket` (for example object creation, deletion and updates). " +
			"On aws-serverless this maps to an S3 bucket notification targeting the topic.",
		AnnotationDefinitions: map[string]*provider.LinkAnnotationDefinition{
			"celerity/topic::" + topic.AnnotationKeyBucketEvents: {
				Name:      topic.AnnotationKeyBucketEvents,
				Label:     "Bucket events",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Examples: []*core.ScalarValue{
					core.ScalarFromString("created"),
					core.ScalarFromString("created,deleted"),
				},
				Description: "Comma-separated set of object-storage events that flow from the bucket into " +
					"the topic. Allowed values are created, deleted and metadataUpdated. Defaults to created.",
			},
			"celerity/topic::" + topic.AnnotationKeyBucketFilterPrefix: {
				Name:      topic.AnnotationKeyBucketFilterPrefix,
				Label:     "Bucket filter prefix",
				Type:      core.ScalarTypeString,
				AppliesTo: provider.LinkAnnotationResourceB,
				Examples: []*core.ScalarValue{
					core.ScalarFromString("incoming/"),
				},
				Description: "Restricts notifications to object keys beginning with this prefix.",
			},
			"celerity/topic::" + topic.AnnotationKeyBucketFilterSuffix: {
				Name:      topic.AnnotationKeyBucketFilterSuffix,
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
