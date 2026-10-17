//go:build unit

package handlerawsserverless

import (
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"

	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/stretchr/testify/suite"
)

type SQLDatabaseAnnotationsTestSuite struct {
	suite.Suite
}

func TestSQLDatabaseAnnotationsTestSuite(t *testing.T) {
	suite.Run(t, new(SQLDatabaseAnnotationsTestSuite))
}

func (s *SQLDatabaseAnnotationsTestSuite) Test_stampSQLDatabaseAnnotations_stamps_authMode_for_iam_databases() {
	lambda := &schema.Resource{
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{
				Values: map[string]*substitutions.StringOrSubstitutions{},
			},
		},
	}
	dbResource := func(authMode string) *schema.Resource {
		return &schema.Resource{Spec: core.MappingNodeFields("authMode", core.MappingNodeFromString(authMode))}
	}
	r := &handler.ResolvedHandler{
		SQLDatabases: []*types.LinkedResource{
			{Name: "ordersDb", Resource: dbResource("iam")},
			{Name: "otherDb", Resource: dbResource("password")},
		},
	}

	// run is nil, there is no Aurora deploy config, so the standalone RDS Proxy is the target.
	stampSQLDatabaseAnnotations(r, nil, lambda)

	iam := lambda.Metadata.Annotations.Values["aws.lambda.rds.ordersDb_rds_proxy.authMode"]
	s.Require().NotNil(iam, "iam-mode database must stamp the RDS proxy authMode annotation")
	s.Require().Len(iam.Values, 1)
	s.Require().NotNil(iam.Values[0].StringValue)
	s.Equal("iam", *iam.Values[0].StringValue)

	s.Nil(lambda.Metadata.Annotations.Values["aws.lambda.rds.otherDb_rds_proxy.authMode"],
		"password-mode databases must not stamp the iam authMode annotation")
}

// A database with no authMode spec field defaults to password and stamps nothing.
func (s *SQLDatabaseAnnotationsTestSuite) Test_stampSQLDatabaseAnnotations_defaults_to_password() {
	lambda := &schema.Resource{
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{
				Values: map[string]*substitutions.StringOrSubstitutions{},
			},
		},
	}
	r := &handler.ResolvedHandler{
		SQLDatabases: []*types.LinkedResource{
			{Name: "ordersDb", Resource: &schema.Resource{Spec: core.MappingNodeFields()}},
		},
	}

	stampSQLDatabaseAnnotations(r, nil, lambda)

	s.Empty(lambda.Metadata.Annotations.Values,
		"a database without authMode defaults to password and stamps no annotation")
}
