package links

import "github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"

// Builds an AbstractLinkDefinition that only declares the resource types and
// human-readable text, for links that carry no cardinality limits, annotations
// or custom validation.
func basicLink(
	resourceTypeA string,
	resourceTypeB string,
	summary string,
	description string,
) *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:        resourceTypeA,
		ResourceTypeB:        resourceTypeB,
		PlainTextSummary:     summary,
		FormattedSummary:     summary,
		PlainTextDescription: description,
		FormattedDescription: description,
	}
}
