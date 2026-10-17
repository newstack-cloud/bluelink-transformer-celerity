// Package awsexternal is the AWS reading of an external resource's
// identifier.
//
// The abstract layer carries that identifier as an opaque string, because its
// format belongs to the deploy target. Everything that treats it as an ARN lives
// here, so an abstract resource package never depends on one target's identifier
// format and a second target can read the same field its own way.
package awsexternal

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// Identifier is an external resource's identifier parsed as an ARN, along with
// what an AWS target derives from it.
type Identifier struct {
	ARN arn.ARN
	// Literal is the identifier exactly as authored.
	Literal string
	// Region the resource lives in: the ARN's own, or the authored region where
	// the ARN carries none. An S3 ARN has empty region and account segments,
	// which is the ARN format for S3 rather than an omission.
	Region string
}

// Parse reads an external identifier as an ARN.
//
// An unparseable identifier is the target's judgement to make, not the abstract
// layer's, so the diagnostic is raised here.
func Parse(
	resourceType string,
	name string,
	external *shared.External,
	resource *schema.Resource,
) (*Identifier, []*core.Diagnostic) {
	identifier := ParseOrNil(external)
	if identifier == nil {
		return nil, []*core.Diagnostic{shared.ExternalDiagnostic(
			resource,
			fmt.Sprintf(
				"%s %q declares an \"externalId\" that is not a valid ARN: %q.",
				resourceType, name, external.ID,
			),
		)}
	}

	return identifier, nil
}

// ResourceName returns the final segment of the ARN's resource part, which is how
// a resource is named where the ARN qualifies it with a type: a DynamoDB table ARN
// ends "table/Orders", an SQS queue ARN carries the name alone, and an S3 bucket
// ARN is the bucket name with no account or region.
func (i *Identifier) ResourceName() string {
	resource := i.ARN.Resource
	if index := strings.LastIndex(resource, "/"); index != -1 {
		return resource[index+1:]
	}

	return resource
}

// QueueURL returns the SQS queue URL for a queue's ARN. SQS addresses a queue by
// URL rather than by ARN, and the URL is derived rather than looked up so the
// transform makes no AWS call.
//
// The host differs by partition: aws-cn uses the .com.cn suffix, and every other
// partition in use follows the amazonaws.com form.
func (i *Identifier) QueueURL() string {
	host := "amazonaws.com"
	if i.ARN.Partition == "aws-cn" {
		host = "amazonaws.com.cn"
	}

	return fmt.Sprintf(
		"https://sqs.%s.%s/%s/%s",
		i.ARN.Region, host, i.ARN.AccountID, i.ResourceName(),
	)
}

// ParameterPath returns the Parameter Store path an SSM parameter ARN names.
// Parameter Store addresses a parameter by path rather than by ARN.
func (i *Identifier) ParameterPath() string {
	return strings.TrimPrefix(i.ARN.Resource, "parameter")
}

// ParseOrNil is Parse for a caller that cannot report a diagnostic, such as a
// rewriter factory. A nil result means the identifier is not an ARN, which the
// resource's emit reports in its own right.
func ParseOrNil(external *shared.External) *Identifier {
	if external == nil {
		return nil
	}

	parsed, err := arn.Parse(external.ID)
	if err != nil {
		return nil
	}

	region := parsed.Region
	if region == "" {
		region = external.Region
	}

	return &Identifier{ARN: parsed, Literal: external.ID, Region: region}
}

// Resolve reads external mode and parses the identifier as an ARN, without
// checking that the resource it names exists.
//
// Stop is true for every external resource, whether or not the identifier
// parsed: an external resource emits nothing either way. A caller that verifies
// differently from EmitOutcome starts here, and gets back both the parsed
// identifier and the authored form a verifier takes.
func Resolve(
	resourceType string,
	name string,
	resource *schema.Resource,
) (identifier *Identifier, external *shared.External, diagnostics []*core.Diagnostic, stop bool) {
	external, diagnostics = shared.ResolvedExternal(resourceType, name, resource)
	if len(diagnostics) > 0 {
		return nil, nil, diagnostics, true
	}
	if external == nil {
		return nil, nil, nil, false
	}

	identifier, diagnostics = Parse(resourceType, name, external, resource)

	return identifier, external, diagnostics, true
}

// EmitOutcome is the AWS reading of external mode for one resource: resolve the
// mode, parse the identifier as an ARN, and check the resource it names exists.
//
// Stop is true for every external resource, whether or not it verified. The
// agnostic half, in shared, stops at the mode and the identifier's presence,
// because it cannot know the format.
func EmitOutcome(
	ctx context.Context,
	run *shared.RunContext,
	resourceType string,
	name string,
	resource *schema.Resource,
) (identifier *Identifier, diagnostics []*core.Diagnostic, stop bool) {
	identifier, external, diagnostics, stop := Resolve(resourceType, name, resource)
	if !stop || identifier == nil {
		return identifier, diagnostics, stop
	}

	return identifier, shared.VerifyExternal(
		ctx, run, resourceType, name, resource, external,
	), true
}
