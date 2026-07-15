package vpc

import "github.com/newstack-cloud/bluelink/libs/blueprint/provider"

// vpcResourceSchema is a stub. The full spec is built in VPC build step 4
// (see docs/contract/resource-mapping-aws-serverless.md, "Gaps & build sequencing").
//
// Eventual fields, from celerity-vpc.mdx:
//   - name    (string, required) — also the shared-identity key for `referenced` mode.
//   - preset  (enum standard|public|isolated|light|light-public, default standard) —
//     ignored when mode is `referenced`.
//   - mode    (enum managed|referenced, default managed) — `referenced` shares an existing
//     Celerity-managed VPC by name (maps to aws/flex/vpc mode:reference, dropping
//     preset and the aws.vpc.* deploy keys). Output: id (the VPC id).
func vpcResourceSchema() *provider.ResourceDefinitionsSchema {
	return &provider.ResourceDefinitionsSchema{
		Type:  provider.ResourceDefinitionsSchemaTypeObject,
		Label: "VpcDefinition",
	}
}
