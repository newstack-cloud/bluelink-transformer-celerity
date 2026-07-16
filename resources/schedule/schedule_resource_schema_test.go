//go:build unit

package schedule

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/stretchr/testify/suite"
)

type ScheduleSchemaTestSuite struct {
	suite.Suite
}

func TestScheduleSchemaTestSuite(t *testing.T) {
	suite.Run(t, new(ScheduleSchemaTestSuite))
}

// The abstract spec is validated strictly before transform, so an optional input
// attribute must be declared or any blueprint setting it (including the spec's own
// examples) is rejected.
func (s *ScheduleSchemaTestSuite) Test_input_is_an_optional_permissive_attribute() {
	schema := scheduleResourceSchema()

	input, ok := schema.Attributes["input"]
	s.Require().True(ok, "expected an input attribute on the schedule schema")
	s.Equal(provider.ResourceDefinitionsSchemaTypeUnion, input.Type)
	s.True(input.Nullable, "input must accept null")
	s.NotContains(schema.Required, "input", "input must be optional")

	// The union covers every JSON shape: object|string|number|boolean|array|null.
	types := map[provider.ResourceDefinitionsSchemaType]bool{}
	for _, member := range input.OneOf {
		types[member.Type] = true
	}
	s.True(types[provider.ResourceDefinitionsSchemaTypeString])
	s.True(types[provider.ResourceDefinitionsSchemaTypeInteger])
	s.True(types[provider.ResourceDefinitionsSchemaTypeFloat])
	s.True(types[provider.ResourceDefinitionsSchemaTypeBoolean])
	s.True(types[provider.ResourceDefinitionsSchemaTypeArray], "array (recursive) member expected")
	s.True(types[provider.ResourceDefinitionsSchemaTypeMap], "object/map (recursive) member expected")
}

func (s *ScheduleSchemaTestSuite) Test_schedule_is_required() {
	schema := scheduleResourceSchema()
	s.Contains(schema.Required, "schedule")
}
