package awsexternal

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	awsshared "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// Verifier looks an external identifier up against the service that owns
// it, using the same credentials the transform already resolves for the build
// manifest.
//
// One read per external resource, against a service the deploy principal already
// has credentials for. Each check is the cheapest read that distinguishes "not
// there" from "cannot see", which is the distinction the author needs: the second
// is expected for a resource in another account.
type Verifier struct {
	env    map[string]string
	loader awsshared.ConfigLoader
}

// NewVerifier builds a verifier over the transform's own credential
// resolution.
func NewVerifier(env map[string]string, loader awsshared.ConfigLoader) *Verifier {
	return &Verifier{env: env, loader: loader}
}

func (v *Verifier) Verify(
	ctx context.Context,
	transformCtx transform.Context,
	celerityType string,
	external *shared.External,
) (shared.ExternalVerification, error) {
	check, known := externalChecks[celerityType]
	if !known {
		return shared.ExternalVerification{Found: true}, nil
	}

	identifier, cfg, err := v.resolve(ctx, transformCtx, external)
	if err != nil {
		return shared.ExternalVerification{}, err
	}
	if identifier == nil {
		// Unparseable, which the emit reports in its own right. Claiming the
		// resource was found keeps one error rather than two for one mistake.
		return shared.ExternalVerification{Found: true}, nil
	}

	return check(ctx, cfg, transformCtx, identifier)
}

// ExternalKeyModel reads an external table's key model, which also answers
// whether the table exists.
func (v *Verifier) ExternalKeyModel(
	ctx context.Context,
	transformCtx transform.Context,
	external *shared.External,
) (shared.ExternalKeyModel, error) {
	identifier, cfg, err := v.resolve(ctx, transformCtx, external)
	if err != nil {
		return shared.ExternalKeyModel{}, err
	}
	if identifier == nil {
		return shared.ExternalKeyModel{Found: true}, nil
	}

	described, err := dynamodb.NewFromConfig(cfg).DescribeTable(
		ctx,
		&dynamodb.DescribeTableInput{TableName: aws.String(identifier.ResourceName())},
	)
	if err != nil {
		verification, classifyErr := classify(err, "ResourceNotFoundException")
		if classifyErr != nil {
			return shared.ExternalKeyModel{}, classifyErr
		}

		return shared.ExternalKeyModel{
			Invisible: verification.Invisible,
			Detail:    verification.Detail,
		}, nil
	}

	model := shared.ExternalKeyModel{Found: true}
	for _, element := range described.Table.KeySchema {
		switch element.KeyType {
		case dynamodbtypes.KeyTypeHash:
			model.PartitionKey = aws.ToString(element.AttributeName)
		case dynamodbtypes.KeyTypeRange:
			model.SortKey = aws.ToString(element.AttributeName)
		}
	}

	return model, nil
}

// A nil identifier with a nil error means the value is not an ARN. The client is
// built for the resource's own region, not the deployment's: a cross-region
// identifier is only answerable by the region it lives in.
func (v *Verifier) resolve(
	ctx context.Context,
	transformCtx transform.Context,
	external *shared.External,
) (*Identifier, aws.Config, error) {
	parsed, err := arn.Parse(external.ID)
	if err != nil {
		return nil, aws.Config{}, nil
	}

	cfg, err := awsshared.ConfigFromTransformContext(ctx, transformCtx, v.env, v.loader)
	if err != nil {
		return nil, aws.Config{}, err
	}

	identifier := &Identifier{ARN: parsed, Literal: external.ID, Region: parsed.Region}
	if identifier.Region == "" {
		identifier.Region = external.Region
	}

	regional := *cfg
	if identifier.Region != "" {
		regional.Region = identifier.Region
	}

	return identifier, regional, nil
}

type externalCheck func(
	ctx context.Context,
	cfg aws.Config,
	transformCtx transform.Context,
	identifier *Identifier,
) (shared.ExternalVerification, error)

var externalChecks = map[string]externalCheck{
	"celerity/topic":  verifyTopic,
	"celerity/queue":  verifyQueue,
	"celerity/bucket": verifyBucket,
}

func verifyTopic(
	ctx context.Context,
	cfg aws.Config,
	_ transform.Context,
	identifier *Identifier,
) (shared.ExternalVerification, error) {
	_, err := sns.NewFromConfig(cfg).GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{
		TopicArn: aws.String(identifier.Literal),
	})

	return classify(err, "NotFound")
}

func verifyQueue(
	ctx context.Context,
	cfg aws.Config,
	_ transform.Context,
	identifier *Identifier,
) (shared.ExternalVerification, error) {
	_, err := sqs.NewFromConfig(cfg).GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(identifier.QueueURL()),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})

	var nonExistent *sqstypes.QueueDoesNotExist
	if errors.As(err, &nonExistent) {
		return shared.ExternalVerification{Detail: "The queue does not exist."}, nil
	}

	return classify(err, "AWS.SimpleQueueService.NonExistentQueue")
}

func verifyBucket(
	ctx context.Context,
	cfg aws.Config,
	transformCtx transform.Context,
	identifier *Identifier,
) (shared.ExternalVerification, error) {
	client := s3.NewFromConfig(cfg, awsshared.S3OptionsFromTransformContext(transformCtx)...)
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(identifier.ResourceName()),
	})

	return classify(err, "NotFound", "NoSuchBucket")
}

// classify maps a lookup's outcome onto the three answers that matter. Anything
// that is neither a not-found nor an access denial is returned as an error, so a
// throttled or unreachable API is reported as unverified rather than as a missing
// resource.
func classify(err error, notFoundCodes ...string) (shared.ExternalVerification, error) {
	if err == nil {
		return shared.ExternalVerification{Found: true}, nil
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return shared.ExternalVerification{}, err
	}

	for _, code := range notFoundCodes {
		if apiErr.ErrorCode() == code {
			return shared.ExternalVerification{
				Detail: fmt.Sprintf("The service reported %s.", apiErr.ErrorCode()),
			}, nil
		}
	}

	if isAccessDenied(apiErr.ErrorCode()) {
		return shared.ExternalVerification{
			Invisible: true,
			Detail:    fmt.Sprintf("The lookup was refused with %s.", apiErr.ErrorCode()),
		}, nil
	}

	return shared.ExternalVerification{}, err
}

// Denial codes differ by service: S3 answers a HeadBucket on a bucket in another
// account with 403, SNS and SQS use their own authorization errors, and DynamoDB
// reports an IAM failure.
func isAccessDenied(code string) bool {
	switch code {
	case "AccessDenied",
		"AccessDeniedException",
		"Forbidden",
		"AuthorizationError",
		"KMS.AccessDeniedException",
		"AWS.SimpleQueueService.AccessDenied",
		"UnrecognizedClientException":
		return true
	}

	return false
}
