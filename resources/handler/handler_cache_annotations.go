package handler

import (
	"fmt"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

// Stamps the aws/lambda/function::aws/elasticache/replicationGroup link's authMode
// annotation for each linked cache that uses ElastiCache IAM/RBAC authentication.
// The provider link creates the elasticache:Connect grant only when the function
// carries aws.lambda.elasticache.<targetCache>.authMode=iam, where <targetCache>
// is the concrete replication-group resource name (the provider resolves it from
// otherResourceInfo.ResourceName). The cache slice emits that resource as
// "<cache>_elasticache_rg", so the annotation key is keyed by the same name.
func stampCacheAnnotations(r *ResolvedHandler, lambda *schema.Resource) {
	for _, c := range r.Caches {
		if c == nil || c.Resource == nil {
			continue
		}
		mode, _ := pluginutils.GetValueByPath("$.authMode", c.Resource.Spec)
		if core.StringValue(mode) != "iam" {
			continue
		}
		rgName := fmt.Sprintf("%s_elasticache_rg", c.Name)
		setStringAnnotation(lambda.Metadata, fmt.Sprintf("aws.lambda.elasticache.%s.authMode", rgName), "iam")
	}
}
