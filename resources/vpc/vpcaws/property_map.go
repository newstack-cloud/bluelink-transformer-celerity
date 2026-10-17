package vpcaws

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// PropertyMap maps the abstract resource's properties onto the concrete ones.
func PropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/vpc.spec.id resolves to the synthetic flex VPC's vpcId output.
			"spec.id": {"spec", "vpcId"},
		},
	}
}

// Rewriter resolves the resource's references onto the concrete resources this
// target emits.
func Rewriter(pm *transformutils.PropertyMap) transformutils.RewriterRegistration {
	return transformutils.RewriterFromPropertyMap(pm, func(r *vpc.ResolvedVPC) string {
		return ConcreteResourceName(r.Name)
	})
}
