package cache

import (
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

func createAWSPropertyMap() transformutils.PropertyMap {
	return transformutils.PropertyMap{
		Renames: map[string][]string{
			// celerity/cache.spec.id resolves to the replication group's id (the
			// ElastiCache primary identifier).
			"spec.id": {"spec", "id"},
			// spec.port resolves to the replication group's configured port (6379).
			"spec.port": {"spec", "port"},
		},
		ValueRefs: map[string]*transformutils.ValueRefSpec{
			// spec.host is a transformer-derived value because the correct endpoint
			// depends on cluster mode (configuration endpoint when clustered, primary
			// endpoint otherwise); keyed <replicationGroup>_host.
			"spec.host": {Suffix: "_host"},
		},
	}
}
