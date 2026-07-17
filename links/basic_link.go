package links

import (
	"strings"

	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

// Builds an AbstractLinkDefinition that only declares the resource types and
// human-readable text, for links that carry no cardinality limits, annotations
// or custom validation. The summary/description are the markdown-formatted forms;
// the plain-text fields are derived by stripping markdown backticks so plain-text
// consumers do not render stray markup.
func basicLink(
	resourceTypeA string,
	resourceTypeB string,
	summary string,
	description string,
) *transformerv1.AbstractLinkDefinition {
	return &transformerv1.AbstractLinkDefinition{
		ResourceTypeA:        resourceTypeA,
		ResourceTypeB:        resourceTypeB,
		PlainTextSummary:     stripMarkdown(summary),
		FormattedSummary:     summary,
		PlainTextDescription: stripMarkdown(description),
		FormattedDescription: description,
	}
}

// stripMarkdown removes the inline markdown markup basicLink callers use (only
// backticks today) so the plain-text link fields contain no markup.
func stripMarkdown(s string) string {
	return strings.ReplaceAll(s, "`", "")
}
