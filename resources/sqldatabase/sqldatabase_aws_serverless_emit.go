package sqldatabase

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/vpc"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	sharedaws "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subwalk"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Defaults for RDS fields the abstract spec does not carry. These become
// deploy-config overrides (aws.rds.<db>.*) in a follow-up.
const (
	defaultInstanceClass    = "db.t3.micro"
	defaultAllocatedStorage = 20
	defaultMasterUsername   = "celerity"

	// serverlessInstanceClass is the DB instance class Aurora Serverless v2
	// instances run under; capacity is governed by the cluster's ACU range.
	serverlessInstanceClass = "db.serverless"

	// Aurora Serverless v2 ACU (Aurora Capacity Unit) scaling defaults, overridable
	// via aws.aurora.<db>.{minACU,maxACU}.
	defaultAuroraMinCapacity = 0.5
	defaultAuroraMaxCapacity = 8.0

	// policyDocVersion is the IAM policy language version used for the proxy role.
	policyDocVersion = "2012-10-17"
)

// presetsUnsuitableForSQL are managed VPC presets that cannot host an RDS
// database: public/light-public have no private subnets, and light is single-AZ
// (RDS subnet groups require at least two availability zones).
var presetsUnsuitableForSQL = map[string]struct{}{
	"public":       {},
	"light-public": {},
	"light":        {},
}

func emitSQLDatabase(
	_ context.Context,
	run *transformutils.Run,
	r *ResolvedSQLDatabase,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	name := core.StringValue(specGet(r, "$.name"))
	if name == "" {
		name = r.Name
	}

	// Preset-suitability validation (managed VPC only).
	if r.VPCName != "" && !r.VPCReferenced {
		if _, unsuitable := presetsUnsuitableForSQL[r.VPCPreset]; unsuitable {
			return &transformutils.EmitResult{
				Diagnostics: []*core.Diagnostic{
					{
						Level: core.DiagnosticLevelError,
						Message: fmt.Sprintf(
							"celerity/sqlDatabase %q requires private subnets across at least two "+
								"availability zones, but its placement VPC preset %q does not provide them; "+
								"use \"standard\" or \"isolated\"",
							name, r.VPCPreset,
						),
					},
				},
			}, nil
		}
	}

	var diagnostics []*core.Diagnostic
	resources := map[string]*schema.Resource{}

	engine := core.StringValue(specGet(r, "$.engine"))

	// Resolve the VPC subnet / security-group references once; every emitted
	// resource (subnet group, instance, cluster, proxy) shares them.
	vpcRefs, err := resolveVPCRefs(r, name)
	if err != nil {
		return nil, err
	}
	if vpcRefs != nil {
		resources[subnetGroupResourceName(r.Name)] = vpcRefs.subnetGroup
	} else {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"celerity/sqlDatabase %q is not linked to a celerity/vpc; databases require VPC placement",
				name,
			),
		})
	}

	auroraEnabled := core.BoolValue(auroraConfig(run, name, "enabled"))
	labels := dbLabels(r)

	proxyEmitted := false
	if auroraEnabled {
		// Aurora Serverless v2: a cluster plus a db.serverless writer instance.
		// The cluster is the resource handlers link to (aws/lambda/function::
		// aws/rds/dbCluster uses Aurora's built-in connection pooling, so no RDS
		// Proxy is emitted for this path).
		resources[clusterResourceName(r.Name)] = buildAuroraCluster(r, run, name, engine, vpcRefs, labels)
		resources[instanceResourceName(r.Name)] = buildAuroraInstance(r, name, engine, labels)
	} else {
		resources[instanceResourceName(r.Name)] = buildStandaloneInstance(r, name, engine, vpcRefs, labels)
		// Standalone RDS: handlers pool connections through an RDS Proxy
		// (aws/lambda/function::aws/rds/dbProxy). The proxy needs private subnets,
		// so it is only emitted when the database is VPC-placed.
		if vpcRefs != nil {
			proxyResources, err := buildProxyResources(r, name, engine, vpcRefs, labels)
			if err != nil {
				return nil, err
			}
			for resName, res := range proxyResources {
				resources[resName] = res
			}
			proxyEmitted = true
		} else {
			diagnostics = append(diagnostics, &core.Diagnostic{
				Level: core.DiagnosticLevelWarning,
				Message: fmt.Sprintf(
					"celerity/sqlDatabase %q has no VPC placement, so no RDS Proxy is emitted; "+
						"handlers cannot pool connections to it",
					name,
				),
			})
		}
	}

	// Read replicas: expose a reader instance (and the readHost output). For
	// Aurora this is an additional db.serverless reader in the cluster; for
	// standalone RDS it is a read replica of the primary instance.
	readReplicas := core.BoolValue(specGet(r, "$.readReplicas"))
	if readReplicas {
		resources[readerResourceName(r.Name)] = buildReaderInstance(r, name, engine, auroraEnabled, labels)
	}

	for _, res := range resources {
		res.Spec = subwalk.WalkMappingNode(res.Spec, transformutils.RewriteResourcePropertyRefs(rw))
	}

	derivedValues, err := dbDerivedValues(r, name, auroraEnabled, proxyEmitted, readReplicas)
	if err != nil {
		return nil, err
	}

	return &transformutils.EmitResult{
		Resources:     resources,
		DerivedValues: derivedValues,
		Diagnostics:   diagnostics,
	}, nil
}

// host and readHost cannot be static renames because the correct endpoint
// branches on proxy-presence and Aurora:
//
//   - host      -> proxy endpoint when a proxy is emitted; else the Aurora cluster
//     writer endpoint; else the standalone instance endpoint.
//   - readHost  -> Aurora cluster reader endpoint (load-balanced) when Aurora;
//     else the standalone reader-instance endpoint. Only emitted when
//     readReplicas is enabled.
//   - databaseName -> the database name (known at emit time).
func dbDerivedValues(
	r *ResolvedSQLDatabase,
	name string,
	auroraEnabled, proxyEmitted, readReplicas bool,
) (map[string]*schema.Value, error) {
	var hostExpr string
	switch {
	case proxyEmitted:
		hostExpr = fmt.Sprintf("${resources.%s.spec.endpoint}", proxyResourceName(r.Name))
	case auroraEnabled:
		hostExpr = fmt.Sprintf("${resources.%s.spec.endpoint.address}", clusterResourceName(r.Name))
	default:
		hostExpr = fmt.Sprintf("${resources.%s.spec.endpoint.address}", instanceResourceName(r.Name))
	}
	hostValue, err := shared.SubstitutionBlueprintValue(hostExpr)
	if err != nil {
		return nil, err
	}

	values := map[string]*schema.Value{
		dbHostKey(r.Name):         hostValue,
		dbDatabaseNameKey(r.Name): shared.LiteralStringBlueprintValue(name),
	}

	if readReplicas {
		var readExpr string
		if auroraEnabled {
			readExpr = fmt.Sprintf("${resources.%s.spec.readEndpoint.address}", clusterResourceName(r.Name))
		} else {
			readExpr = fmt.Sprintf("${resources.%s.spec.endpoint.address}", readerResourceName(r.Name))
		}
		readValue, err := shared.SubstitutionBlueprintValue(readExpr)
		if err != nil {
			return nil, err
		}
		values[dbReadHostKey(r.Name)] = readValue
	}

	return values, nil
}

// Must equal <instance>_host / _read_host / _database_name: the property map's
// ValueRefs resolve to concreteName (the RDS instance) + suffix.
func dbHostKey(name string) string {
	return instanceResourceName(name) + "_host"
}

func dbReadHostKey(name string) string {
	return instanceResourceName(name) + "_read_host"
}

func dbDatabaseNameKey(name string) string {
	return instanceResourceName(name) + "_database_name"
}

// vpcReferences carries the shared VPC-derived references plus the subnet group
// resource, resolved once and reused across every DB resource.
type vpcReferences struct {
	subnetGroupName string
	subnetIdsRef    *core.MappingNode
	securityGroups  *core.MappingNode
	subnetGroup     *schema.Resource
}

func resolveVPCRefs(r *ResolvedSQLDatabase, name string) (*vpcReferences, error) {
	if r.VPCName == "" {
		return nil, nil
	}
	vpcConcrete := vpc.ConcreteResourceName(r.VPCName)
	subnetGroupName := fmt.Sprintf("%s-db-subnets", name)

	subnetIdsRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.privateSubnetIds}", vpcConcrete))
	if err != nil {
		return nil, err
	}
	sgRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.securityGroups}", vpcConcrete))
	if err != nil {
		return nil, err
	}

	subnetGroup := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/rds/dbSubnetGroup"},
		Spec: core.MappingNodeFields(
			"dbSubnetGroupName", core.MappingNodeFromString(subnetGroupName),
			"dbSubnetGroupDescription", core.MappingNodeFromString(
				fmt.Sprintf("Subnets for Celerity database %s", name)),
			"subnetIds", subnetIdsRef,
		),
		Metadata: infraMeta(r.Name),
	}

	return &vpcReferences{
		subnetGroupName: subnetGroupName,
		subnetIdsRef:    subnetIdsRef,
		securityGroups:  sgRef,
		subnetGroup:     subnetGroup,
	}, nil
}

// Password mode -> RDS manages the master-user secret itself (exposing the
// computed masterUserSecret); iam mode -> IAM database authentication.
func buildStandaloneInstance(
	r *ResolvedSQLDatabase,
	name, engine string,
	vpcRefs *vpcReferences,
	labels *schema.StringMap,
) *schema.Resource {
	spec := core.MappingNodeFields(
		"dbInstanceIdentifier", core.MappingNodeFromString(name),
		"dbName", core.MappingNodeFromString(name),
		"engine", core.MappingNodeFromString(engine),
		"dbInstanceClass", core.MappingNodeFromString(defaultInstanceClass),
		"allocatedStorage", core.MappingNodeFromString(fmt.Sprintf("%d", defaultAllocatedStorage)),
		"masterUsername", core.MappingNodeFromString(defaultMasterUsername),
	)
	applyInstanceAuth(spec, r)
	if vpcRefs != nil {
		spec.Fields["dbSubnetGroupName"] = core.MappingNodeFromString(vpcRefs.subnetGroupName)
		spec.Fields["vpcSecurityGroups"] = vpcRefs.securityGroups
	}

	meta := infraMeta(r.Name)
	meta.Labels = labels
	return &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/rds/dbInstance"},
		Spec:         spec,
		Metadata:     meta,
		LinkSelector: r.Resource.LinkSelector,
	}
}

// The cluster carries the database's labels so a handler that links to the
// sqlDatabase resolves to the cluster.
func buildAuroraCluster(
	r *ResolvedSQLDatabase,
	run *transformutils.Run,
	name, engine string,
	vpcRefs *vpcReferences,
	labels *schema.StringMap,
) *schema.Resource {
	spec := core.MappingNodeFields(
		"dbClusterIdentifier", core.MappingNodeFromString(name),
		"databaseName", core.MappingNodeFromString(name),
		"engine", core.MappingNodeFromString(auroraEngine(engine)),
		"masterUsername", core.MappingNodeFromString(defaultMasterUsername),
		"serverlessV2ScalingConfiguration", core.MappingNodeFields(
			// The provider field is minCapacity/maxCapacity; the deploy-config keys
			// are aws.aurora.<db>.minACU / maxACU (spec vocabulary).
			"minCapacity", auroraCapacity(run, name, "minACU", defaultAuroraMinCapacity),
			"maxCapacity", auroraCapacity(run, name, "maxACU", defaultAuroraMaxCapacity),
		),
	)
	if sqlAuthMode(r) == "iam" {
		spec.Fields["enableIAMDatabaseAuthentication"] = core.MappingNodeFromBool(true)
	} else {
		spec.Fields["manageMasterUserPassword"] = core.MappingNodeFromBool(true)
	}
	if vpcRefs != nil {
		spec.Fields["dbSubnetGroupName"] = core.MappingNodeFromString(vpcRefs.subnetGroupName)
		spec.Fields["vpcSecurityGroupIds"] = vpcRefs.securityGroups
	}

	meta := infraMeta(r.Name)
	meta.Labels = labels
	return &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/rds/dbCluster"},
		Spec:         spec,
		Metadata:     meta,
		LinkSelector: r.Resource.LinkSelector,
	}
}

func buildAuroraInstance(
	r *ResolvedSQLDatabase,
	name, engine string,
	labels *schema.StringMap,
) *schema.Resource {
	spec := core.MappingNodeFields(
		"dbInstanceIdentifier", core.MappingNodeFromString(name),
		"dbClusterIdentifier", clusterRef(r.Name, "dbClusterIdentifier"),
		"engine", core.MappingNodeFromString(auroraEngine(engine)),
		"dbInstanceClass", core.MappingNodeFromString(serverlessInstanceClass),
	)
	meta := infraMeta(r.Name)
	meta.Labels = labels
	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/rds/dbInstance"},
		Spec:     spec,
		Metadata: meta,
	}
}

// For Aurora the reader is a db.serverless reader in the cluster; for standalone
// RDS it is a read replica of the primary instance.
func buildReaderInstance(
	r *ResolvedSQLDatabase,
	name, engine string,
	auroraEnabled bool,
	labels *schema.StringMap,
) *schema.Resource {
	readerID := fmt.Sprintf("%s-reader", name)
	var spec *core.MappingNode
	if auroraEnabled {
		spec = core.MappingNodeFields(
			"dbInstanceIdentifier", core.MappingNodeFromString(readerID),
			"dbClusterIdentifier", clusterRef(r.Name, "dbClusterIdentifier"),
			"engine", core.MappingNodeFromString(auroraEngine(engine)),
			"dbInstanceClass", core.MappingNodeFromString(serverlessInstanceClass),
		)
	} else {
		spec = core.MappingNodeFields(
			"dbInstanceIdentifier", core.MappingNodeFromString(readerID),
			"sourceDBInstanceIdentifier", instanceRef(r.Name, "dbInstanceIdentifier"),
			"dbInstanceClass", core.MappingNodeFromString(defaultInstanceClass),
		)
	}
	meta := infraMeta(r.Name)
	meta.Labels = labels
	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/rds/dbInstance"},
		Spec:     spec,
		Metadata: meta,
	}
}

// Emits the RDS Proxy, its IAM role and the target group registering the primary
// instance. The proxy carries the database's labels so handlers that select the
// sqlDatabase by label pool through it.
func buildProxyResources(
	r *ResolvedSQLDatabase,
	name, engine string,
	vpcRefs *vpcReferences,
	labels *schema.StringMap,
) (map[string]*schema.Resource, error) {
	roleArnRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", proxyRoleResourceName(r.Name)))
	if err != nil {
		return nil, err
	}

	proxySpec := core.MappingNodeFields(
		"dbProxyName", core.MappingNodeFromString(name),
		"engineFamily", core.MappingNodeFromString(engineFamily(engine)),
		"roleArn", roleArnRef,
		"vpcSubnetIds", vpcRefs.subnetIdsRef,
		"vpcSecurityGroupIds", vpcRefs.securityGroups,
	)

	iamMode := sqlAuthMode(r) == "iam"
	if iamMode {
		proxySpec.Fields["auth"] = core.MappingNodeItems(
			core.MappingNodeFields("iamAuth", core.MappingNodeFromString("REQUIRED")),
		)
	} else {
		secretArnRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.masterUserSecret.secretArn}", instanceResourceName(r.Name)))
		if err != nil {
			return nil, err
		}
		proxySpec.Fields["auth"] = core.MappingNodeItems(
			core.MappingNodeFields(
				"authScheme", core.MappingNodeFromString("SECRETS"),
				"secretArn", secretArnRef,
			),
		)
	}

	proxyMeta := infraMeta(r.Name)
	proxyMeta.Labels = labels

	roleResource, err := buildProxyRole(r, name, iamMode)
	if err != nil {
		return nil, err
	}

	return map[string]*schema.Resource{
		proxyResourceName(r.Name): {
			Type:     &schema.ResourceTypeWrapper{Value: "aws/rds/dbProxy"},
			Spec:     proxySpec,
			Metadata: proxyMeta,
		},
		proxyRoleResourceName(r.Name): roleResource,
		proxyTargetGroupResourceName(r.Name): {
			Type: &schema.ResourceTypeWrapper{Value: "aws/rds/dbProxyTargetGroup"},
			Spec: core.MappingNodeFields(
				"dbProxyName", core.MappingNodeFromString(name),
				"targetGroupName", core.MappingNodeFromString("default"),
				"dbInstanceIdentifiers", core.MappingNodeItems(
					instanceRef(r.Name, "dbInstanceIdentifier"),
				),
			),
			Metadata: infraMeta(r.Name),
		},
	}, nil
}

// The IAM role trusts rds.amazonaws.com; in password mode it also grants
// secretsmanager:GetSecretValue on the instance's RDS-managed master secret so
// the proxy can read credentials.
func buildProxyRole(r *ResolvedSQLDatabase, name string, iamMode bool) (*schema.Resource, error) {
	roleName := proxyRoleResourceName(r.Name)
	spec := core.MappingNodeFields(
		"roleName", core.MappingNodeFromString(roleName),
		"assumeRolePolicyDocument", core.MappingNodeFields(
			"version", core.MappingNodeFromString(policyDocVersion),
			"statement", core.MappingNodeItems(core.MappingNodeFields(
				"effect", core.MappingNodeFromString("Allow"),
				"principal", core.MappingNodeFields(
					"service", core.MappingNodeFromString("rds.amazonaws.com"),
				),
				"action", core.MappingNodeFromString("sts:AssumeRole"),
			)),
		),
	)

	if !iamMode {
		secretArnRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.masterUserSecret.secretArn}", instanceResourceName(r.Name)))
		if err != nil {
			return nil, err
		}
		spec.Fields["policies"] = core.MappingNodeItems(
			core.MappingNodeFields(
				"policyName", core.MappingNodeFromString(fmt.Sprintf("%s-secret-access", name)),
				"policyDocument", core.MappingNodeFields(
					"version", core.MappingNodeFromString(policyDocVersion),
					"statement", core.MappingNodeItems(core.MappingNodeFields(
						"effect", core.MappingNodeFromString("Allow"),
						"action", core.MappingNodeFromString("secretsmanager:GetSecretValue"),
						"resource", secretArnRef,
					)),
				),
			),
		)
	}

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "aws/iam/role"},
		Spec:     spec,
		Metadata: infraMeta(r.Name),
	}, nil
}

func applyInstanceAuth(spec *core.MappingNode, r *ResolvedSQLDatabase) {
	if sqlAuthMode(r) == "iam" {
		spec.Fields["enableIAMDatabaseAuthentication"] = core.MappingNodeFromBool(true)
	} else {
		spec.Fields["manageMasterUserPassword"] = core.MappingNodeFromBool(true)
	}
}

func auroraConfig(run *transformutils.Run, name, suffix string) *core.MappingNode {
	if run == nil {
		return nil
	}
	node, _ := sharedaws.ResolveDeployConfigNode(run.TransformContext, "aws.aurora", name, suffix)
	return node
}

func auroraCapacity(run *transformutils.Run, name, suffix string, fallback float64) *core.MappingNode {
	if node := auroraConfig(run, name, suffix); node != nil {
		return node
	}
	return core.MappingNodeFromFloat(fallback)
}

func dbLabels(r *ResolvedSQLDatabase) *schema.StringMap {
	if r.Resource.Metadata != nil {
		return r.Resource.Metadata.Labels
	}
	return nil
}

func clusterRef(name, field string) *core.MappingNode {
	node, _ := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.%s}", clusterResourceName(name), field))
	return node
}

func instanceRef(name, field string) *core.MappingNode {
	node, _ := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.%s}", instanceResourceName(name), field))
	return node
}

func engineFamily(engine string) string {
	switch engine {
	case "mysql", "mariadb", "aurora-mysql":
		return "MYSQL"
	default:
		return "POSTGRESQL"
	}
}

func auroraEngine(engine string) string {
	if engineFamily(engine) == "MYSQL" {
		return "aurora-mysql"
	}
	return "aurora-postgresql"
}

func sqlAuthMode(r *ResolvedSQLDatabase) string {
	mode := core.StringValue(specGet(r, "$.authMode"))
	if mode == "" {
		return "password"
	}
	return mode
}

func infraMeta(abstractName string) *schema.Metadata {
	return &schema.Metadata{
		Annotations: transformutils.TransformerBaseAnnotations(
			&transformutils.TransformerBaseAnnotationsInput{
				AbstractResourceName: abstractName,
				AbstractResourceType: "celerity/sqlDatabase",
				ResourceCategory:     transformutils.ResourceCategoryInfrastructure,
			},
		),
	}
}

func specGet(r *ResolvedSQLDatabase, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, r.Resource.Spec)
	return node
}

func instanceResourceName(name string) string {
	return fmt.Sprintf("%s_rds_instance", name)
}

func subnetGroupResourceName(name string) string {
	return fmt.Sprintf("%s_rds_subnet_group", name)
}

func clusterResourceName(name string) string {
	return fmt.Sprintf("%s_rds_cluster", name)
}

func readerResourceName(name string) string {
	return fmt.Sprintf("%s_rds_reader", name)
}

func proxyResourceName(name string) string {
	return fmt.Sprintf("%s_rds_proxy", name)
}

func proxyRoleResourceName(name string) string {
	return fmt.Sprintf("%s_rds_proxy_role", name)
}

func proxyTargetGroupResourceName(name string) string {
	return fmt.Sprintf("%s_rds_proxy_target_group", name)
}
