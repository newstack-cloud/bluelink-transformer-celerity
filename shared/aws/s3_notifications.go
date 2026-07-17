package aws

import (
	"fmt"
	"strings"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// S3EventForCelerityEvent maps a Celerity object-storage event name
// (created | deleted) to its concrete S3 event string. metadataUpdated has no S3
// equivalent and returns ok=false so callers can warn rather than drop it silently.
func S3EventForCelerityEvent(name string) (string, bool) {
	switch name {
	case "created":
		return "s3:ObjectCreated:*", true
	case "deleted":
		return "s3:ObjectRemoved:*", true
	default:
		return "", false
	}
}

// BucketNotificationKeys names the celerity-level annotations read from a bucket
// notification receiver (a queue or topic) and the provider annotation prefix the
// values are stamped under.
type BucketNotificationKeys struct {
	// CelerityEvents is the receiver's comma-separated event-set annotation, e.g.
	// "celerity.queue.bucket.events".
	CelerityEvents string
	// CelerityFilterPrefix / CelerityFilterSuffix are the object-key filter
	// annotations, e.g. "celerity.queue.bucket.filterPrefix".
	CelerityFilterPrefix string
	CelerityFilterSuffix string
	// ProviderPrefix is the provider S3-notification annotation prefix, e.g.
	// "aws.s3.sqs" (bucket->queue) or "aws.s3.sns" (bucket->topic).
	ProviderPrefix string
}

// StampBucketNotifications reads the celerity bucket-notification annotations from
// src (the abstract queue/topic receiving notifications) and stamps the matching
// provider S3-notification annotations onto dst (the emitted concrete resource's
// metadata): <prefix>.event.<index> for each mapped event, plus <prefix>.filterPrefix
// and <prefix>.filterSuffix. When no events annotation is set nothing is stamped,
// so the provider applies its default (ObjectCreated). Returns any celerity events
// with no S3 equivalent (for example metadataUpdated) so the caller can warn.
func StampBucketNotifications(
	src *schema.Resource,
	dst *schema.Metadata,
	keys BucketNotificationKeys,
) []string {
	var unsupported []string

	if value, ok := transformutils.GetAnnotation(src, keys.CelerityEvents, ""); ok {
		index := 0
		for _, raw := range splitAndTrimList(core.StringValue(value)) {
			event, mappable := S3EventForCelerityEvent(raw)
			if !mappable {
				unsupported = append(unsupported, raw)
				continue
			}
			setStringAnnotation(dst, fmt.Sprintf("%s.event.%d", keys.ProviderPrefix, index), event)
			index++
		}
	}

	if value, ok := transformutils.GetAnnotation(src, keys.CelerityFilterPrefix, ""); ok {
		if s := core.StringValue(value); s != "" {
			setStringAnnotation(dst, keys.ProviderPrefix+".filterPrefix", s)
		}
	}
	if value, ok := transformutils.GetAnnotation(src, keys.CelerityFilterSuffix, ""); ok {
		if s := core.StringValue(value); s != "" {
			setStringAnnotation(dst, keys.ProviderPrefix+".filterSuffix", s)
		}
	}

	return unsupported
}

func setStringAnnotation(meta *schema.Metadata, key, value string) {
	if meta.Annotations == nil {
		meta.Annotations = &schema.StringOrSubstitutionsMap{
			Values: map[string]*substitutions.StringOrSubstitutions{},
		}
	}
	meta.Annotations.Values[key] = pluginutils.StringToSubstitutions(value)
}

func splitAndTrimList(value string) []string {
	parts := []string{}
	for _, raw := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}
