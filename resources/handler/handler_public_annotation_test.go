//go:build unit

package handler

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/substitutions"
	"github.com/stretchr/testify/suite"
)

type PublicAnnotationSuite struct {
	suite.Suite
}

func handlerWithAnnotations(values map[string]string) *ResolvedHandler {
	annotations := &schema.StringOrSubstitutionsMap{
		Values: map[string]*substitutions.StringOrSubstitutions{},
	}
	for key, value := range values {
		annotations.Values[key] = &substitutions.StringOrSubstitutions{
			Values: []*substitutions.StringOrSubstitution{
				{StringValue: &value},
			},
		}
	}

	return &ResolvedHandler{
		Name:     "healthController_check",
		Resource: &schema.Resource{Metadata: &schema.Metadata{Annotations: annotations}},
	}
}

// run57 deployed GET /health behind the API's default guard despite the handler
// carrying @Public(). Blueprint annotations are string-typed by construction, so the
// SDK's boolean true arrives as the string "true" and core.BoolValue read it as false.
func (s *PublicAnnotationSuite) Test_a_public_handler_is_recognised() {
	s.True(isPublicHandler(handlerWithAnnotations(map[string]string{
		AnnotationKeyPublic: "true",
	})))
}

func (s *PublicAnnotationSuite) Test_a_handler_that_is_not_public_is_not_recognised() {
	s.False(isPublicHandler(handlerWithAnnotations(map[string]string{
		AnnotationKeyPublic: "false",
	})))
}

// Absent must not read as public: that would open every route the guard protects.
func (s *PublicAnnotationSuite) Test_an_absent_annotation_is_not_public() {
	s.False(isPublicHandler(handlerWithAnnotations(map[string]string{})))
}

// Neither must a value that is not a flag at all.
func (s *PublicAnnotationSuite) Test_a_non_flag_value_is_not_public() {
	s.False(isPublicHandler(handlerWithAnnotations(map[string]string{
		AnnotationKeyPublic: "yes please",
	})))
}

func TestPublicAnnotationSuite(t *testing.T) {
	suite.Run(t, new(PublicAnnotationSuite))
}
