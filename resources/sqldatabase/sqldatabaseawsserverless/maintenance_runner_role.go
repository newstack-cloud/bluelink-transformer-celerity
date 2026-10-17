package sqldatabaseawsserverless

import (
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase/sqldatabaseaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// lambdaVPCExecutionPolicyARN lets the function create the network interfaces
// its VPC placement needs. Without it the function deploys and every
// invocation fails at start-up, before any of its own code runs.
const lambdaVPCExecutionPolicyARN = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"

// buildMaintenanceRunnerRole builds the runner's execution role.
//
// This role is deliberately not shared with any handler, and holds the one
// grant handlers must never have: read access to the database's master
// credential. Provisioning creates a role and grants privileges over the
// owner's objects, which only the owner can do, but an application handler
// holding the same credential would have the whole instance, which is the
// reason the application user exists at all.
func buildMaintenanceRunnerRole(
	r *sqldatabase.ResolvedSQLDatabase,
	in maintenanceRunnerInput,
) (*schema.Resource, error) {
	spec := core.MappingNodeFields(
		"roleName", shared.DerivedName(in.physicalName, "-maint-role"),
		"assumeRolePolicyDocument", core.MappingNodeFields(
			"version", core.MappingNodeFromString(sqldatabaseaws.PolicyDocVersion),
			"statement", core.MappingNodeItems(core.MappingNodeFields(
				"effect", core.MappingNodeFromString("Allow"),
				"principal", core.MappingNodeFields(
					"service", core.MappingNodeFromString("lambda.amazonaws.com"),
				),
				"action", core.MappingNodeFromString("sts:AssumeRole"),
			)),
		),
		"managedPolicyArns", core.MappingNodeItems(
			core.MappingNodeFromString(lambdaVPCExecutionPolicyARN),
		),
	)

	policies, err := maintenanceRunnerPolicies(r, in)
	if err != nil {
		return nil, err
	}
	if len(policies) > 0 {
		spec.Fields["policies"] = core.MappingNodeItems(policies...)
	}

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/iam/role"},
		Spec:     spec,
		Metadata: sqldatabaseaws.InfraMetadata(r.Name),
	}, nil
}

// maintenanceRunnerPolicies builds the inline policies for the credentials the
// runner reads, each scoped to one secret rather than to Secrets Manager.
func maintenanceRunnerPolicies(
	r *sqldatabase.ResolvedSQLDatabase,
	in maintenanceRunnerInput,
) ([]*core.MappingNode, error) {
	if sqldatabase.AuthMode(r.Resource) == "iam" {
		// Nothing stored to read: the runner exchanges its execution role for a
		// token to connect as the owner, and the application user authenticates
		// the same way. The role still has to be created and granted rds_iam in
		// the database, which is the work provisioning does either way.
		connectResource, err := shared.SubstitutionMappingNode(
			fmt.Sprintf(
				"arn:aws:rds-db:*:*:dbuser:${resources.%s.spec.dbiResourceId}/%s",
				sqldatabaseaws.InstanceResourceName(r.Name), sqldatabase.MasterUsername,
			),
		)
		if err != nil {
			return nil, err
		}
		return []*core.MappingNode{policyDocument(
			fmt.Sprintf("%s-maintenance-connect", in.name),
			[]string{"rds-db:connect"},
			core.MappingNodeItems(connectResource),
		)}, nil
	}

	master, err := shared.SubstitutionMappingNode(
		fmt.Sprintf(
			"${resources.%s.spec.masterUserSecret.secretArn}",
			sqldatabaseaws.MasterSecretHolderResourceName(r.Name, in.auroraEnabled),
		),
	)
	if err != nil {
		return nil, err
	}
	// spec.id, not spec.arn: a Secrets Manager secret has no arn field, and its
	// Cloud Control primary identifier is the ARN.
	appUser, err := shared.SubstitutionMappingNode(fmt.Sprintf(
		"${resources.%s.spec.id}", sqldatabaseaws.AppUserSecretResourceName(r.Name)))
	if err != nil {
		return nil, err
	}

	return []*core.MappingNode{policyDocument(
		fmt.Sprintf("%s-maintenance-secrets", in.name),
		[]string{"secretsmanager:GetSecretValue"},
		core.MappingNodeItems(master, appUser),
	)}, nil
}

func policyDocument(name string, actions []string, resources *core.MappingNode) *core.MappingNode {
	return core.MappingNodeFields(
		"policyName", core.MappingNodeFromString(name),
		"policyDocument", core.MappingNodeFields(
			"version", core.MappingNodeFromString(sqldatabaseaws.PolicyDocVersion),
			"statement", core.MappingNodeItems(core.MappingNodeFields(
				"effect", core.MappingNodeFromString("Allow"),
				"action", core.MappingNodeFromStringSlice(actions),
				"resource", resources,
			)),
		),
	)
}
