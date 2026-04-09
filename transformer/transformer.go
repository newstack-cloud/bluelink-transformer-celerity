package transformer

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

func NewTransformer() transform.SpecTransformer {
	return &transformerv1.TransformerPluginDefinition{
		TransformName: "celerity-2026-02-27-draft",
	}
}
