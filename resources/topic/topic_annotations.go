package topic

// Abstract link annotations on a celerity/topic. These are part of the portable
// vocabulary an author and an abstract link use, so they stay with the abstract
// resource even though each deploy target maps them onto something of its own.
const (
	// AnnotationKeyBucketEvents is the celerity/bucket -> celerity/topic link
	// annotation (comma-separated created | deleted | metadataUpdated) selecting
	// which object-storage events flow from a linked bucket into this topic.
	AnnotationKeyBucketEvents = "celerity.topic.bucket.events"

	// AnnotationKeyBucketFilterPrefix restricts bucket notifications into this
	// topic to object keys with the given prefix.
	AnnotationKeyBucketFilterPrefix = "celerity.topic.bucket.filterPrefix"

	// AnnotationKeyBucketFilterSuffix restricts bucket notifications into this
	// topic to object keys with the given suffix.
	AnnotationKeyBucketFilterSuffix = "celerity.topic.bucket.filterSuffix"
)
