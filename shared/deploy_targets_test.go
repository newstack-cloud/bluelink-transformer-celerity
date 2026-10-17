//go:build unit

package shared

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type DeployTargetsTestSuite struct {
	suite.Suite
}

func TestDeployTargetsTestSuite(t *testing.T) {
	suite.Run(t, new(DeployTargetsTestSuite))
}

// A database's emit provisions the pooler and a VPC's provisions the network
// identity it is reached through, so both read this. The cases below are the
// contract between them: whenever this says a pooler is required, exactly one
// identity is prepared for it, and whenever it does not, none is.
func (s *DeployTargetsTestSuite) Test_pooler_is_required_only_for_a_short_lived_target() {
	cases := []struct {
		target          string
		poolsInternally bool
		required        bool
		why             string
	}{
		{
			target:   AWSServerless,
			required: true,
			why: "a workload that does not outlive a request cannot reuse a " +
				"connection, so something has to pool on its behalf",
		},
		{
			target:          AWSServerless,
			poolsInternally: true,
			required:        false,
			why:             "a database that pools internally needs nothing in front of it",
		},
		{
			target:   AWS,
			required: false,
			why: "a long-running workload pools in process, where an external " +
				"pooler adds a hop and a cost for nothing",
		},
		{
			target:          AWS,
			poolsInternally: true,
			required:        false,
			why:             "neither half asks for a pooler",
		},
	}

	for _, testCase := range cases {
		s.Equal(
			testCase.required,
			DatabasePoolerRequired(testCase.target, testCase.poolsInternally),
			"target %q, poolsInternally %v: %s",
			testCase.target, testCase.poolsInternally, testCase.why,
		)
	}
}

// Short-lived workloads are what every pooling decision turns on, so the two
// declared targets must not both answer the same way: a rule that cannot
// distinguish them is not a rule.
func (s *DeployTargetsTestSuite) Test_the_declared_targets_differ_on_workload_lifetime() {
	s.True(WorkloadsAreShortLived(AWSServerless))
	s.False(WorkloadsAreShortLived(AWS))
	s.False(
		WorkloadsAreShortLived("some-target-that-does-not-exist"),
		"an unrecognised target must not be assumed to be short-lived, since "+
			"that would provision a pooler nothing reaches",
	)
}
