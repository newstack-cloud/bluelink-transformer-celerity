//go:build unit

package awslambda

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/suite"
)

type ExternalTargetsTestSuite struct {
	suite.Suite
}

func TestExternalTargetsTestSuite(t *testing.T) {
	suite.Run(t, new(ExternalTargetsTestSuite))
}

// A handler reaching an external resource must not share a role with one that
// does not, or the second would inherit access it was never granted.
func (s *ExternalTargetsTestSuite) Test_an_external_target_changes_the_fingerprint() {
	without := (&RolePlan{Links: []string{"celerity/bucket::uploads"}}).Fingerprint()
	with := (&RolePlan{
		Links: []string{"celerity/bucket::uploads"},
		ExternalTargets: []ExternalGrant{
			{Service: ExternalTargetServiceS3, ARN: "arn:aws:s3:::other-account-uploads"},
		},
	}).Fingerprint()

	s.NotEqual(without, with)
}

// ListBucket is granted on the bucket and the object actions on its contents.
// Granting only the bucket leaves every object call refused, which reads as a
// credentials problem rather than a policy one.
func (s *ExternalTargetsTestSuite) Test_a_bucket_grant_covers_the_bucket_and_its_objects() {
	doc := externalTargetsPolicyDoc([]ExternalGrant{
		{Service: ExternalTargetServiceS3, ARN: "arn:aws:s3:::other-account-uploads"},
	})
	s.Require().NotNil(doc)

	resources := doc.Fields["statement"].Items[0].Fields["resource"].Items
	s.Require().Len(resources, 2)
	s.Equal("arn:aws:s3:::other-account-uploads", core.StringValue(resources[0]))
	s.Equal("arn:aws:s3:::other-account-uploads/*", core.StringValue(resources[1]))
}

// A grant whose ARN could not be read is skipped rather than written against an
// empty resource, which would be a policy granting access to nothing and would
// hide the real diagnostic.
func (s *ExternalTargetsTestSuite) Test_a_grant_without_an_arn_is_skipped() {
	s.Nil(externalTargetsPolicyDoc([]ExternalGrant{
		{Service: ExternalTargetServiceS3},
	}))
	s.Nil(externalTargetsPolicyDoc(nil))
}

// An unrecognised service grants nothing rather than everything.
func (s *ExternalTargetsTestSuite) Test_an_unknown_service_grants_nothing() {
	s.Empty(externalTargetActions("not-a-service", ""))
	s.Nil(externalTargetsPolicyDoc([]ExternalGrant{
		{Service: "not-a-service", ARN: "arn:aws:unknown:::thing"},
	}))
}
