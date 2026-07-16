//go:build unit

package aws

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/stretchr/testify/suite"
)

type DeployConfigTestSuite struct {
	suite.Suite
}

func TestDeployConfigTestSuite(t *testing.T) {
	suite.Run(t, new(DeployConfigTestSuite))
}

func (s *DeployConfigTestSuite) Test_per_resource_key_wins_over_global() {
	ctx := &fakeConfigContext{vars: map[string]*core.ScalarValue{
		"aws.sqs.messageRetentionPeriod":        core.ScalarFromInt(60),
		"aws.sqs.orders.messageRetentionPeriod": core.ScalarFromInt(120),
	}}

	v, ok := ResolveDeployConfig(ctx, "aws.sqs", "orders", "messageRetentionPeriod")
	s.Require().True(ok)
	s.Equal(120, *v.IntValue)
}

func (s *DeployConfigTestSuite) Test_falls_back_to_global_when_no_per_resource_key() {
	ctx := &fakeConfigContext{vars: map[string]*core.ScalarValue{
		"aws.sqs.messageRetentionPeriod": core.ScalarFromInt(60),
	}}

	v, ok := ResolveDeployConfig(ctx, "aws.sqs", "orders", "messageRetentionPeriod")
	s.Require().True(ok)
	s.Equal(60, *v.IntValue)
}

func (s *DeployConfigTestSuite) Test_infix_is_carried_by_the_prefix() {
	ctx := &fakeConfigContext{vars: map[string]*core.ScalarValue{
		"aws.sns.fifo.events.messageRetentionPeriod": core.ScalarFromInt(7),
	}}

	v, ok := ResolveDeployConfig(ctx, "aws.sns.fifo", "events", "messageRetentionPeriod")
	s.Require().True(ok)
	s.Equal(7, *v.IntValue)
}

func (s *DeployConfigTestSuite) Test_returns_false_when_unset() {
	ctx := &fakeConfigContext{vars: map[string]*core.ScalarValue{}}
	_, ok := ResolveDeployConfig(ctx, "aws.sqs", "orders", "maxMessageSize")
	s.False(ok)
}

func (s *DeployConfigTestSuite) Test_node_wraps_the_scalar() {
	ctx := &fakeConfigContext{vars: map[string]*core.ScalarValue{
		"aws.dynamodb.orders.billingMode": core.ScalarFromString("PROVISIONED"),
	}}
	node, ok := ResolveDeployConfigNode(ctx, "aws.dynamodb", "orders", "billingMode")
	s.Require().True(ok)
	s.Equal("PROVISIONED", core.StringValue(node))
}

type fakeConfigContext struct {
	vars map[string]*core.ScalarValue
}

func (f *fakeConfigContext) TransformerConfigVariable(name string) (*core.ScalarValue, bool) {
	v, ok := f.vars[name]
	return v, ok
}

func (f *fakeConfigContext) TransformerConfigVariables() map[string]*core.ScalarValue {
	return f.vars
}

func (f *fakeConfigContext) ContextVariable(string) (*core.ScalarValue, bool) {
	return nil, false
}

func (f *fakeConfigContext) ContextVariables() map[string]*core.ScalarValue {
	return nil
}
