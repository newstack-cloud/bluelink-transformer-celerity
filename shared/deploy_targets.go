package shared

const (
	// AWSServerless is the deploy target string for AWS Serverless (Lambda + API Gateway).
	// For infrastructure resources that do not host application code, the same concrete resources
	// as "aws" will be emitted.
	AWSServerless = "aws-serverless"
	// AWS is the deploy target string for AWS with traditional compute (EC2, EKS, etc).
	AWS = "aws"
)

const (
	// PlatformAWS is the string used in emitted resource environment variables to
	// indicate AWS as the target platform.
	// This is separate from the deploy target and is passed through environment variables
	// to the Celerity application handler code to tune it to the wider platform context
	// (e.g. for AWS, GCP, Azure, etc).
	PlatformAWS = "aws"
)

// WorkloadsAreShortLived reports whether a deploy target runs application code
// as processes that start and stop around a request rather than as long-running
// ones.
//
// It decides whether something has to pool database connections on the
// workload's behalf. A short-lived process opens its own connection and cannot
// reuse one, so a database's connection limit is reached long before its CPU is,
// and a pooler in front of the database is what keeps it reachable under load. A
// long-running workload pools in process, where an external pooler adds a hop
// and a cost for nothing.
//
// Read by a database's emit, which provisions the pooler, and by a VPC's, which
// provisions the network identity the pooler is reached through. The two have to
// agree: a VPC that prepares an identity for a pooler that was never emitted
// leaves it with nothing in it, and a pooler emitted without one cannot be
// reached at all.
func WorkloadsAreShortLived(target string) bool {
	return target == AWSServerless
}

// DatabasePoolerRequired reports whether a database on this deploy target needs
// a connection pooler in front of it.
//
// Two things decide it. A database that pools internally needs nothing in front
// of it on any target. Otherwise it comes down to whether the target's workloads
// outlive a request.
//
// Kept here, rather than with a database's emit, because more than one emit has
// to reach the same answer: the database's provisions the pooler and a VPC's
// provisions the network identity it is reached through. If the two disagree,
// either an identity is left with nothing in it or the pooler cannot be reached
// at all.
func DatabasePoolerRequired(target string, poolsInternally bool) bool {
	return !poolsInternally && WorkloadsAreShortLived(target)
}
