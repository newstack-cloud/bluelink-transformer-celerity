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

func Test_stampCacheAnnotations_stamps_authMode_for_iam_caches(t *testing.T) {
	lambda := &schema.Resource{
		Metadata: &schema.Metadata{
			Annotations: &schema.StringOrSubstitutionsMap{
				Values: map[string]*substitutions.StringOrSubstitutions{},
			},
		},
	}
	cacheResource := func(authMode string) *schema.Resource {
		return &schema.Resource{Spec: core.MappingNodeFields("authMode", core.MappingNodeFromString(authMode))}
	}
	r := &ResolvedHandler{
		Caches: []*types.LinkedResource{
			{Name: "sessionCache", Resource: cacheResource("iam")},
			{Name: "otherCache", Resource: cacheResource("password")},
		},
	}

	stampCacheAnnotations(r, lambda)

	iam := lambda.Metadata.Annotations.Values["aws.lambda.elasticache.sessionCache_elasticache_rg.authMode"]
	require.NotNil(t, iam)
	require.Len(t, iam.Values, 1)
	require.NotNil(t, iam.Values[0].StringValue)
	assert.Equal(t, "iam", *iam.Values[0].StringValue)

	assert.Nil(t, lambda.Metadata.Annotations.Values["aws.lambda.elasticache.otherCache_elasticache_rg.authMode"],
		"password-mode caches must not stamp the iam authMode annotation")
}
