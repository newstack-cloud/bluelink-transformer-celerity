//go:build unit

package transformer

import (
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/provider"
	"github.com/stretchr/testify/suite"
)

// LinksRegistryTestSuite structurally checks every registered abstract link
// definition. It is the invariant that guards against empty/mis-keyed link defs
// (the declarative shape the handler link doc comments refer to).
type LinksRegistryTestSuite struct {
	suite.Suite
}

func TestLinksRegistryTestSuite(t *testing.T) {
	suite.Run(t, new(LinksRegistryTestSuite))
}

// Every registered link must declare resource types that match its map key, carry
// human-readable summaries and descriptions, and reference itself consistently.
func (s *LinksRegistryTestSuite) Test_every_registered_link_is_structurally_complete() {
	for key, def := range abstractLinks() {
		s.Require().NotNilf(def, "link %q is nil", key)

		s.NotEmptyf(def.ResourceTypeA, "link %q has empty ResourceTypeA", key)
		s.NotEmptyf(def.ResourceTypeB, "link %q has empty ResourceTypeB", key)

		// The registration key must equal "<ResourceTypeA>::<ResourceTypeB>" so the
		// SDK's edge lookup (which keys off the map key) and GetType() agree.
		wantKey := def.ResourceTypeA + "::" + def.ResourceTypeB
		s.Equalf(wantKey, key, "link %q key does not match its resource types (%q)", key, wantKey)

		s.NotEmptyf(def.PlainTextSummary, "link %q has empty PlainTextSummary", key)
		s.NotEmptyf(def.FormattedSummary, "link %q has empty FormattedSummary", key)
		s.NotEmptyf(def.PlainTextDescription, "link %q has empty PlainTextDescription", key)
		s.NotEmptyf(def.FormattedDescription, "link %q has empty FormattedDescription", key)
	}
}

// Every annotation definition must be keyed "<resourceType>::<name>", where the
// resource type is the link's A or B side matching the annotation's AppliesTo, and
// the suffix equals the annotation's own Name.
func (s *LinksRegistryTestSuite) Test_annotation_definition_keys_are_consistent() {
	for key, def := range abstractLinks() {
		for annKey, ann := range def.AnnotationDefinitions {
			s.Require().NotNilf(ann, "link %q annotation %q is nil", key, annKey)

			parts := strings.SplitN(annKey, "::", 2)
			s.Require().Lenf(parts, 2, "link %q annotation key %q is not <resourceType>::<name>", key, annKey)
			resType, name := parts[0], parts[1]

			s.Equalf(ann.Name, name, "link %q annotation key %q suffix does not match Name %q", key, annKey, ann.Name)

			wantResType := def.ResourceTypeB
			if ann.AppliesTo == provider.LinkAnnotationResourceA {
				wantResType = def.ResourceTypeA
			}
			s.Equalf(wantResType, resType,
				"link %q annotation %q prefix %q does not match its AppliesTo resource type %q",
				key, annKey, resType, wantResType)
		}
	}
}
