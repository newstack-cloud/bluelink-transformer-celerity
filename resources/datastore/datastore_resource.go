package datastore

import (
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Resource defines the abstract resource for the Celerity Datastore.
func Resource() *transformerv1.AbstractResourceDefinition {
	awsPropertyMap := createAWSPropertyMap()

	return &transformerv1.AbstractResourceDefinition{
		Type:    "celerity/datastore",
		Label:   "Celerity Datastore",
		Schema:  datastoreResourceSchema(),
		Resolve: resolveDatastore,
		Emitters: map[string]transformutils.EmitterRegistration{
			shared.AWSServerless: transformutils.TypedEmitter(emitDatastore),
		},
		PropertyMaps: map[string]transformutils.PropertyMap{
			shared.AWSServerless: awsPropertyMap,
		},
		Rewriters: map[string]transformutils.RewriterRegistration{
			shared.AWSServerless: transformutils.RewriterFromPropertyMap(
				&awsPropertyMap,
				func(r *ResolvedDatastore) string {
					return datastoreConcreteName(r.Name)
				},
			),
		},
	}
}
