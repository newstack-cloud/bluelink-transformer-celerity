package queue

// Link annotation keys an author sets on a celerity/queue. Each is part of the
// abstract vocabulary, so it names what the author means rather than what any
// one deploy target does with it. Declared here because the links package
// references them and each target's emit translates them.
const (
	// AnnotationKeyDeadLetterMaxAttempts is the celerity/queue -> celerity/queue
	// link annotation set on the parent (source) queue, capping how many times a
	// message is delivered before it goes to the dead-letter queue.
	AnnotationKeyDeadLetterMaxAttempts = "celerity.queue.deadLetterMaxAttempts"

	// AnnotationKeyBucketEvents is the celerity/bucket -> celerity/queue link
	// annotation (comma-separated created | deleted | metadataUpdated) selecting
	// which object-storage events flow from a linked bucket into this queue.
	AnnotationKeyBucketEvents = "celerity.queue.bucket.events"

	// AnnotationKeyBucketFilterPrefix restricts bucket notifications into this
	// queue to object keys with the given prefix.
	AnnotationKeyBucketFilterPrefix = "celerity.queue.bucket.filterPrefix"

	// AnnotationKeyBucketFilterSuffix restricts bucket notifications into this
	// queue to object keys with the given suffix.
	AnnotationKeyBucketFilterSuffix = "celerity.queue.bucket.filterSuffix"
)
