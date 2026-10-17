//go:build unit

package handlerawsserverless

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/types"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/stretchr/testify/suite"
)

type CacheAnnotationsTestSuite struct {
	suite.Suite
}

func TestCacheAnnotationsTestSuite(t *testing.T) {
	suite.Run(t, new(CacheAnnotationsTestSuite))
}

func (s *CacheAnnotationsTestSuite) Test_stampCacheAnnotations_stamps_authMode_for_iam_caches() {
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
	r := &handler.ResolvedHandler{
		Caches: []*types.LinkedResource{
			{Name: "sessionCache", Resource: cacheResource("iam")},
			{Name: "otherCache", Resource: cacheResource("password")},
		},
	}

	stampCacheAnnotations(r, lambda)

	iam := lambda.Metadata.Annotations.Values["aws.lambda.elasticache.sessionCache_elasticache_rg.authMode"]
	s.Require().NotNil(iam)
	s.Require().Len(iam.Values, 1)
	s.Require().NotNil(iam.Values[0].StringValue)
	s.Equal("iam", *iam.Values[0].StringValue)

	s.Nil(lambda.Metadata.Annotations.Values["aws.lambda.elasticache.otherCache_elasticache_rg.authMode"],
		"password-mode caches must not stamp the iam authMode annotation")
}
