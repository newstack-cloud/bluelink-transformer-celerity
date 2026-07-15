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
