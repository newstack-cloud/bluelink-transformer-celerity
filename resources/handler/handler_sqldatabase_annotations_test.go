//go:build unit

package handler

import (
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_stampSQLDatabaseAnnotations_stamps_authMode_for_iam_databases(t *testing.T) {
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
	r := &ResolvedHandler{
		SQLDatabases: []*types.LinkedResource{
			{Name: "ordersDb", Resource: dbResource("iam")},
			{Name: "otherDb", Resource: dbResource("password")},
		},
	}

	// run is nil: no Aurora deploy config, so the standalone RDS Proxy is the target.
	stampSQLDatabaseAnnotations(r, nil, lambda)

	iam := lambda.Metadata.Annotations.Values["aws.lambda.rds.ordersDb_rds_proxy.authMode"]
	require.NotNil(t, iam, "iam-mode database must stamp the RDS proxy authMode annotation")
	require.Len(t, iam.Values, 1)
	require.NotNil(t, iam.Values[0].StringValue)
	assert.Equal(t, "iam", *iam.Values[0].StringValue)

	assert.Nil(t, lambda.Metadata.Annotations.Values["aws.lambda.rds.otherDb_rds_proxy.authMode"],
		"password-mode databases must not stamp the iam authMode annotation")
}

// A database with no authMode spec field defaults to password and stamps nothing.
func Test_stampSQLDatabaseAnnotations_defaults_to_password(t *testing.T) {
	lambda := &schema.Resource{
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{
				Values: map[string]*substitutions.StringOrSubstitutions{},
			},
		},
	}
	r := &ResolvedHandler{
		SQLDatabases: []*types.LinkedResource{
			{Name: "ordersDb", Resource: &schema.Resource{Spec: core.MappingNodeFields()}},
		},
	}

	stampSQLDatabaseAnnotations(r, nil, lambda)

	assert.Empty(t, lambda.Metadata.Annotations.Values,
		"a database without authMode defaults to password and stamps no annotation")
}
