//go:build unit

package transformer

import (
	"context"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
	"github.com/stretchr/testify/suite"
)

type BucketTransformTestSuite struct {
	suite.Suite
}

func TestBucketTransformTestSuite(t *testing.T) {
	suite.Run(t, new(BucketTransformTestSuite))
}

func (s *BucketTransformTestSuite) Test_emits_an_s3_bucket_with_the_mapped_configs() {
	b := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/bucket"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("uploads"),
			"encryption", core.MappingNodeFields(
				"encryptionKeyId", core.MappingNodeFromString("alias/uploads-key"),
			),
			"versioning", core.MappingNodeFields(
				"status", core.MappingNodeFromString("Enabled"),
			),
			"website", core.MappingNodeFields(
				"mainPageDocument", core.MappingNodeFromString("index.html"),
				"notFoundDocument", core.MappingNodeFromString("404.html"),
			),
			"logging", core.MappingNodeFields(
				"destinationBucket", core.MappingNodeFromString("log-bucket"),
				"logFilePrefix", core.MappingNodeFromString("uploads/"),
			),
		),
		Metadata: &schema.Metadata{
			Labels: &schema.StringMap{Values: map[string]string{"team": "payments"}},
		},
	}

	out := s.transformBucket(map[string]*schema.Resource{"myBucket": b})
	s3 := out.TransformedBlueprint.Resources.Values["myBucket_s3_bucket"]
	s.Require().NotNil(s3)
	s.Equal("aws/s3/bucket", s3.Type.Value)
	s.Equal("uploads", core.StringValue(s3.Spec.Fields["bucketName"]))

	// encryption -> bucketEncryption.serverSideEncryptionConfiguration[0], KMS by default
	// when a key is supplied.
	sse := s3.Spec.Fields["bucketEncryption"].Fields["serverSideEncryptionConfiguration"].Items
	s.Require().Len(sse, 1)
	byDefault := sse[0].Fields["serverSideEncryptionByDefault"]
	s.Equal("aws:kms", core.StringValue(byDefault.Fields["sseAlgorithm"]))
	s.Equal("alias/uploads-key", core.StringValue(byDefault.Fields["kmsMasterKeyID"]))

	// versioning.status -> versioningConfiguration.status
	s.Equal("Enabled", core.StringValue(s3.Spec.Fields["versioningConfiguration"].Fields["status"]))

	// website -> index/error document
	web := s3.Spec.Fields["websiteConfiguration"]
	s.Equal("index.html", core.StringValue(web.Fields["indexDocument"]))
	s.Equal("404.html", core.StringValue(web.Fields["errorDocument"]))

	// logging -> destinationBucketName / logFilePrefix
	log := s3.Spec.Fields["loggingConfiguration"]
	s.Equal("log-bucket", core.StringValue(log.Fields["destinationBucketName"]))
	s.Equal("uploads/", core.StringValue(log.Fields["logFilePrefix"]))

	// Labels preserved; infrastructure category.
	s.Equal("payments", s3.Metadata.Labels.Values["team"])
	s.Equal("infrastructure", annotationLiteral(s3.Metadata.Annotations, transformutils.AnnotationResourceCategory))
}

// A substitution-valued name (e.g. "${variables.namePrefix}-uploads") must be
// passed through to bucketName verbatim so it resolves at deploy time, not
// stringified to "" and silently dropped (which would auto-generate the
// physical bucket name).
func (s *BucketTransformTestSuite) Test_substitution_valued_name_is_passed_through() {
	nameNode, err := shared.SubstitutionMappingNode("${variables.namePrefix}-uploads")
	s.Require().NoError(err)
	b := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/bucket"},
		Spec: core.MappingNodeFields("name", nameNode),
	}

	out := s.transformBucket(map[string]*schema.Resource{"myBucket": b})

	s3 := out.TransformedBlueprint.Resources.Values["myBucket_s3_bucket"]
	s.Require().NotNil(s3)
	s.Equal(
		[]string{"${namePrefix}", "-uploads"},
		substitutionSegments(s3.Spec.Fields["bucketName"]),
	)
}

func (s *BucketTransformTestSuite) Test_encryption_defaults_to_sse_s3_without_a_key() {
	b := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/bucket"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("uploads"),
			"encryption", core.MappingNodeFields(),
		),
	}

	out := s.transformBucket(map[string]*schema.Resource{"myBucket": b})
	s3 := out.TransformedBlueprint.Resources.Values["myBucket_s3_bucket"]
	byDefault := s3.Spec.Fields["bucketEncryption"].
		Fields["serverSideEncryptionConfiguration"].Items[0].
		Fields["serverSideEncryptionByDefault"]
	s.Equal("AES256", core.StringValue(byDefault.Fields["sseAlgorithm"]))
	s.Nil(byDefault.Fields["kmsMasterKeyID"])
}

// A key paired with a non-KMS algorithm is invalid on S3; the emit normalises to
// KMS so kmsMasterKeyID is never attached to an AES256 default.
func (s *BucketTransformTestSuite) Test_encryption_key_forces_kms_over_non_kms_algorithm() {
	b := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/bucket"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("uploads"),
			"encryption", core.MappingNodeFields(
				"encryptionKeyId", core.MappingNodeFromString("alias/uploads-key"),
				"encryptionAlgorithm", core.MappingNodeFromString("AES256"),
			),
		),
	}

	out := s.transformBucket(map[string]*schema.Resource{"myBucket": b})
	s3 := out.TransformedBlueprint.Resources.Values["myBucket_s3_bucket"]
	byDefault := s3.Spec.Fields["bucketEncryption"].
		Fields["serverSideEncryptionConfiguration"].Items[0].
		Fields["serverSideEncryptionByDefault"]
	s.Equal("aws:kms", core.StringValue(byDefault.Fields["sseAlgorithm"]),
		"a key implies KMS even when AES256 is requested")
	s.Equal("alias/uploads-key", core.StringValue(byDefault.Fields["kmsMasterKeyID"]))
}

func (s *BucketTransformTestSuite) Test_deferred_configs_raise_a_warning_and_are_not_emitted() {
	b := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "celerity/bucket"},
		Spec: core.MappingNodeFields(
			"name", core.MappingNodeFromString("uploads"),
			"replication", core.MappingNodeFields(
				"someField", core.MappingNodeFromString("x"),
			),
		),
	}

	out := s.transformBucket(map[string]*schema.Resource{"myBucket": b})

	found := false
	for _, d := range out.Diagnostics {
		if d.Level == core.DiagnosticLevelWarning && strings.Contains(d.Message, "replication") {
			found = true
		}
	}
	s.True(found, "expected a warning diagnostic for the deferred replication config")

	// The deferred config is not mapped onto the emitted bucket.
	s3 := out.TransformedBlueprint.Resources.Values["myBucket_s3_bucket"]
	s.Require().NotNil(s3)
	s.Nil(s3.Spec.Fields["replicationConfiguration"])
}

func (s *BucketTransformTestSuite) transformBucket(
	resources map[string]*schema.Resource,
) *transform.SpecTransformerTransformOutput {
	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: resources}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          emptyLinkGraph{},
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(out.TransformedBlueprint)
	return out
}
