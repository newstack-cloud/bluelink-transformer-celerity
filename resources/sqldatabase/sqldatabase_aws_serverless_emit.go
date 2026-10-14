package sqldatabase

import (
	"context"
	"fmt"
	"maps"

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
	// resourceTypeDBInstance is the concrete RDS instance resource type emitted for
	// standalone databases, Aurora writers and read replicas.
	resourceTypeDBInstance = "aws/rds/dbInstance"

	// proxyTargetLabelKey marks the instance a proxy fronts, so the emitted proxy can
	// select it and the aws/rds/dbProxy::aws/rds/dbInstance link can open the network
	// path between them.
	//
	// A distinct key rather than the database's own labels: those are carried by every
	// resource this database emits, so selecting on them would have the proxy link to
	// its own subnet group and role as well.
	proxyTargetLabelKey = "celerity.internal.sqldatabase.proxyTarget"

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

	// maxSQLPhysicalNameLen caps the generated physical base name so every
	// identifier derived from it fits its AWS limit: "-reader" within the
	// 63-char RDS instance identifier, and "-proxy-role" within the 64-char
	// IAM role name.
	maxSQLPhysicalNameLen = 52
)

// presetsUnsuitableForSQL are managed VPC presets that cannot host an RDS
// database: public/light-public have no private subnets, and light is single-AZ
// (RDS subnet groups require at least two availability zones).
var presetsUnsuitableForSQL = map[string]struct{}{
	"public":       {},
	"light-public": {},
	"light":        {},
}

// presetSuitabilityError rejects placement on a managed VPC preset that cannot
// host an RDS database (no private subnets, or single-AZ). Returns nil when the
// placement is a referenced VPC or a suitable preset.
func presetSuitabilityError(r *ResolvedSQLDatabase, name string) *core.Diagnostic {
	if r.VPCName == "" || r.VPCReferenced {
		return nil
	}
	if _, unsuitable := presetsUnsuitableForSQL[r.VPCPreset]; !unsuitable {
		return nil
	}
	return &core.Diagnostic{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/sqlDatabase %q requires private subnets across at least two "+
				"availability zones, but its placement VPC preset %q does not provide them; "+
				"use \"standard\" or \"isolated\"",
			name, r.VPCPreset,
		),
		Range: core.DiagnosticRangeFromSourceMeta(r.Resource.SourceMeta, nil),
	}
}

type sqlDatabasePlan struct {
	name          string
	physicalName  *core.MappingNode
	engine        string
	labels        *schema.StringMap
	vpcRefs       *vpcReferences
	auroraEnabled bool
	readReplicas  bool
	proxyEmitted  bool
}

func emitSQLDatabase(
	_ context.Context,
	run *transformutils.Run,
	r *ResolvedSQLDatabase,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	if diagnostics := deferredStructuralErrors(r); len(diagnostics) > 0 {
		return &transformutils.EmitResult{Diagnostics: diagnostics}, nil
	}

	plan, blocked, err := planSQLDatabase(r, run)
	if err != nil {
		return nil, err
	}
	if len(blocked) > 0 {
		return &transformutils.EmitResult{Diagnostics: blocked}, nil
	}

	resources, err := buildDatabaseResources(r, run, plan)
	if err != nil {
		return nil, err
	}

	diagnostics, err := addMaintenanceRunner(r, run, plan, resources)
	if err != nil {
		return nil, err
	}

	rewriteResourceRefs(resources, rw)

	derivedValues, err := dbDerivedValues(
		r, plan.name, plan.auroraEnabled, plan.proxyEmitted, plan.readReplicas,
	)
	if err != nil {
		return nil, err
	}

	sharedaws.ApplyTags(resources, r.Resource.Metadata, sharedaws.Identity{
		AppName:      shared.ResolveAppName(run),
		ResourceName: r.Name,
		ResourceType: "celerity/sqlDatabase",
	})

	return &transformutils.EmitResult{
		Resources:     resources,
		DerivedValues: derivedValues,
		Diagnostics:   diagnostics,
	}, nil
}

// engine, authMode and readReplicas each decide which concrete resources are
// emitted, so none can be deferred to a reference the deploy engine resolves
// after the graph is built.
func deferredStructuralErrors(r *ResolvedSQLDatabase) []*core.Diagnostic {
	return shared.DeferredValueErrors(
		shared.AWSServerless,
		"sqlDatabase", r.Name, r.Resource,
		shared.StructuralField{Name: "engine", Value: specGet(r, "$.engine")},
		shared.StructuralField{Name: "authMode", Value: specGet(r, "$.authMode")},
		shared.StructuralField{Name: "readReplicas", Value: specGet(r, "$.readReplicas")},
	)
}

func rewriteResourceRefs(
	resources map[string]*schema.Resource,
	rw transformutils.ResourcePropertyRewriter,
) {
	for _, res := range resources {
		res.Spec = subwalk.WalkMappingNode(res.Spec, transformutils.RewriteResourcePropertyRefs(rw))
	}
}

// Resolves the names, topology and VPC references every emitted resource
// shares. A non-empty diagnostic slice means nothing is emitted at all, so the
// plan is nil.
func planSQLDatabase(
	r *ResolvedSQLDatabase,
	run *transformutils.Run,
) (*sqlDatabasePlan, []*core.Diagnostic, error) {
	name, physicalName := sqlDatabaseNames(r, run)

	if diag := presetSuitabilityError(r, name); diag != nil {
		return nil, []*core.Diagnostic{diag}, nil
	}

	// Resolved once: every emitted resource (subnet group, instance, cluster,
	// proxy) shares these references.
	vpcRefs, err := resolveVPCRefs(r, name, physicalName)
	if err != nil {
		return nil, nil, err
	}
	if vpcRefs == nil {
		return nil, []*core.Diagnostic{missingVPCError(r, name)}, nil
	}

	return &sqlDatabasePlan{
		name:          name,
		physicalName:  physicalName,
		engine:        core.StringValue(specGet(r, "$.engine")),
		labels:        dbLabels(r),
		vpcRefs:       vpcRefs,
		auroraEnabled: core.BoolValue(auroraConfig(run, name, "enabled")),
		readReplicas:  core.BoolValue(specGet(r, "$.readReplicas")),
	}, nil, nil
}

// The logical name keys deploy-config lookups (aws.aurora.<name>.*), the
// engine-internal dbName/databaseName and the diagnostics; the physical name is
// the base every deployed identifier derives from. An author-provided spec.name
// is kept as-is for the latter (the author owns uniqueness, matching
// queue/bucket), otherwise it is app-scoped so two apps in one account never
// collide on generated names.
//
// A referenced spec.name reaches the deployed identifiers but cannot serve as
// the logical name, since that is read before references are resolved. The
// resource's own name stands in, as it does when spec.name is unset.
func sqlDatabaseNames(
	r *ResolvedSQLDatabase,
	run *transformutils.Run,
) (string, *core.MappingNode) {
	nameNode := specGet(r, "$.name")
	if !shared.HasStringValue(nameNode) {
		return r.Name, core.MappingNodeFromString(shared.AppScopedPhysicalName(
			shared.ResolveAppName(run), r.Name, maxSQLPhysicalNameLen,
		))
	}

	name := core.StringValue(nameNode)
	if name == "" {
		name = r.Name
	}

	return name, nameNode
}

// Databases require VPC placement on aws-serverless: without it the instance,
// subnet group and proxy cannot be wired, so nothing is emitted rather than a
// set of incomplete resources.
func missingVPCError(r *ResolvedSQLDatabase, name string) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"celerity/sqlDatabase %q is not linked to a celerity/vpc; databases require VPC "+
				"placement on aws-serverless, so no database resources have been emitted",
			name,
		),
		Range: core.DiagnosticRangeFromSourceMeta(r.Resource.SourceMeta, nil),
	}
}

func buildDatabaseResources(
	r *ResolvedSQLDatabase,
	run *transformutils.Run,
	plan *sqlDatabasePlan,
) (map[string]*schema.Resource, error) {
	resources := map[string]*schema.Resource{
		subnetGroupResourceName(r.Name): plan.vpcRefs.subnetGroup,
	}

	if err := addTopologyResources(r, run, plan, resources); err != nil {
		return nil, err
	}

	// On Aurora an additional db.serverless reader in the cluster; on standalone
	// RDS a read replica of the primary instance.
	if plan.readReplicas {
		resources[readerResourceName(r.Name)] = buildReaderInstance(
			r, plan.engine, plan.physicalName, plan.auroraEnabled, plan.labels,
		)
	}

	// The application authenticates as its own database user rather than the
	// master, so password-mode databases carry a secret holding that user's
	// generated credentials. IAM mode distributes no credential at all.
	if AppUserAuthEnabled(r) {
		appUserSecret, err := buildAppUserSecret(r, plan.name, plan.physicalName, plan.labels)
		if err != nil {
			return nil, err
		}
		resources[AppUserSecretResourceName(r.Name)] = appUserSecret
	}

	return resources, nil
}

// Aurora Serverless v2 is a cluster plus a db.serverless writer instance, and
// the cluster is what handlers link to: aws/lambda/function::aws/rds/dbCluster
// uses Aurora's built-in connection pooling, so no RDS Proxy is emitted on that
// path. Standalone RDS pools through a proxy instead
// (aws/lambda/function::aws/rds/dbProxy).
func addTopologyResources(
	r *ResolvedSQLDatabase,
	run *transformutils.Run,
	plan *sqlDatabasePlan,
	resources map[string]*schema.Resource,
) error {
	if plan.auroraEnabled {
		resources[clusterResourceName(r.Name)] = buildAuroraCluster(
			r, run, plan.name, plan.engine, plan.physicalName, plan.vpcRefs, plan.labels,
		)
		resources[instanceResourceName(r.Name)] = buildAuroraInstance(
			r, plan.engine, plan.physicalName, plan.labels,
		)
		return nil
	}

	resources[instanceResourceName(r.Name)] = buildStandaloneInstance(
		r, plan.name, plan.engine, plan.physicalName, plan.vpcRefs, plan.labels,
	)
	proxyResources, err := buildProxyResources(
		r, plan.name, plan.engine, plan.physicalName, plan.vpcRefs, plan.labels,
	)
	if err != nil {
		return err
	}
	maps.Copy(resources, proxyResources)
	plan.proxyEmitted = true

	return nil
}

// The application user's credential authenticates as a role that does not exist
// until something inside the VPC creates it, and nothing outside can reach the
// database to do so. The runner is what closes that gap, for provisioning now
// and migrations next.
func addMaintenanceRunner(
	r *ResolvedSQLDatabase,
	run *transformutils.Run,
	plan *sqlDatabasePlan,
	resources map[string]*schema.Resource,
) ([]*core.Diagnostic, error) {
	maintenanceResources, diagnostics, err := buildMaintenanceRunner(r, maintenanceRunnerInput{
		name:          plan.name,
		physicalName:  plan.physicalName,
		auroraEnabled: plan.auroraEnabled,
		manifest:      buildManifest(run),
	})
	if err != nil {
		return nil, err
	}
	maps.Copy(resources, maintenanceResources)

	// The runner needs a network path to whatever the application connects
	// through: the proxy on the standalone path, the cluster on Aurora, which
	// pools for itself.
	target := proxyResourceName(r.Name)
	if plan.auroraEnabled {
		target = clusterResourceName(r.Name)
	}
	markMaintenanceTarget(resources[target], r.Name)

	return diagnostics, nil
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
	subnetGroupNameRef *core.MappingNode
	subnetIdsRef       *core.MappingNode
	// instanceSecurityGroups and proxySecurityGroups are separate groups minted by
	// the VPC. The proxy reaches the instance through the link between them, which
	// pairs one group with the other; a single shared group would need a
	// self-referencing rule and would also open that path to anything else placed
	// in it.
	instanceSecurityGroups *core.MappingNode
	proxySecurityGroups    *core.MappingNode
	subnetGroup            *schema.Resource
}

func resolveVPCRefs(
	r *ResolvedSQLDatabase,
	name string,
	physicalName *core.MappingNode,
) (*vpcReferences, error) {
	if r.VPCName == "" {
		return nil, nil
	}
	vpcConcrete := vpc.ConcreteResourceName(r.VPCName)
	subnetGroupName := shared.DerivedName(physicalName, "-db-subnets")

	subnetIdsRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.privateSubnetIds}", vpcConcrete))
	if err != nil {
		return nil, err
	}
	// Each side references the group the VPC minted for it, not the VPC's whole
	// list. A link opening a path to the database pairs the caller's group with a
	// group minted for that resource alone; given the whole list it cannot tell
	// which group belongs to what, and pairing against a shared one would open a
	// path to everything else in it.
	instanceSGRef, err := shared.SubstitutionMappingNode(
		shared.VPCSecurityGroupIDRef(vpcConcrete, r.Name, shared.SecurityGroupRoleDatabase))
	if err != nil {
		return nil, err
	}
	proxySGRef, err := shared.SubstitutionMappingNode(
		shared.VPCSecurityGroupIDRef(vpcConcrete, r.Name, shared.SecurityGroupRoleProxy))
	if err != nil {
		return nil, err
	}

	subnetGroup := &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/rds/dbSubnetGroup"},
		Spec: core.MappingNodeFields(
			"dbSubnetGroupName", subnetGroupName,
			"dbSubnetGroupDescription", core.MappingNodeFromString(
				fmt.Sprintf("Subnets for Celerity database %s", name)),
			"subnetIds", subnetIdsRef,
		),
		Metadata: infraMeta(r.Name),
	}

	// Reference the emitted subnet group's name so a dependency edge is created,
	// rather than repeating the plain string (which would leave no ordering edge).
	subnetGroupNameRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.dbSubnetGroupName}", subnetGroupResourceName(r.Name)))
	if err != nil {
		return nil, err
	}

	return &vpcReferences{
		subnetGroupNameRef:     subnetGroupNameRef,
		subnetIdsRef:           subnetIdsRef,
		instanceSecurityGroups: core.MappingNodeItems(instanceSGRef),
		proxySecurityGroups:    core.MappingNodeItems(proxySGRef),
		subnetGroup:            subnetGroup,
	}, nil
}

// Password mode -> RDS manages the master-user secret itself (exposing the
// computed masterUserSecret); iam mode -> IAM database authentication.
func buildStandaloneInstance(
	r *ResolvedSQLDatabase,
	name, engine string,
	physicalName *core.MappingNode,
	vpcRefs *vpcReferences,
	labels *schema.StringMap,
) *schema.Resource {
	spec := core.MappingNodeFields(
		"dbInstanceIdentifier", physicalName,
		"dbName", core.MappingNodeFromString(name),
		"engine", core.MappingNodeFromString(engine),
		"dbInstanceClass", core.MappingNodeFromString(defaultInstanceClass),
		"allocatedStorage", core.MappingNodeFromString(fmt.Sprintf("%d", defaultAllocatedStorage)),
		"masterUsername", core.MappingNodeFromString(defaultMasterUsername),
	)
	applyInstanceAuth(spec, r)
	if vpcRefs != nil {
		spec.Fields["dbSubnetGroupName"] = vpcRefs.subnetGroupNameRef
		spec.Fields["vpcSecurityGroups"] = vpcRefs.instanceSecurityGroups
	}

	meta := infraMeta(r.Name)
	// The proxy selects on this label to open the network path to the instance it
	// fronts. Carried by the primary only: the proxy's target group registers the
	// primary, so a read replica is not on the far end of that connection.
	meta.Labels = labelsWith(labels, proxyTargetLabelKey, name)
	return &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: resourceTypeDBInstance},
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
	physicalName *core.MappingNode,
	vpcRefs *vpcReferences,
	labels *schema.StringMap,
) *schema.Resource {
	spec := core.MappingNodeFields(
		"dbClusterIdentifier", physicalName,
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
		spec.Fields["dbSubnetGroupName"] = vpcRefs.subnetGroupNameRef
		spec.Fields["vpcSecurityGroupIds"] = vpcRefs.instanceSecurityGroups
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
	engine string,
	physicalName *core.MappingNode,
	labels *schema.StringMap,
) *schema.Resource {
	spec := core.MappingNodeFields(
		"dbInstanceIdentifier", physicalName,
		"dbClusterIdentifier", clusterRef(r.Name, "dbClusterIdentifier"),
		"engine", core.MappingNodeFromString(auroraEngine(engine)),
		"dbInstanceClass", core.MappingNodeFromString(serverlessInstanceClass),
	)
	meta := infraMeta(r.Name)
	meta.Labels = labels
	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: resourceTypeDBInstance},
		Spec:     spec,
		Metadata: meta,
	}
}

// For Aurora the reader is a db.serverless reader in the cluster; for standalone
// RDS it is a read replica of the primary instance.
func buildReaderInstance(
	r *ResolvedSQLDatabase,
	engine string,
	physicalName *core.MappingNode,
	auroraEnabled bool,
	labels *schema.StringMap,
) *schema.Resource {
	readerID := shared.DerivedName(physicalName, "-reader")
	var spec *core.MappingNode
	if auroraEnabled {
		spec = core.MappingNodeFields(
			"dbInstanceIdentifier", readerID,
			"dbClusterIdentifier", clusterRef(r.Name, "dbClusterIdentifier"),
			"engine", core.MappingNodeFromString(auroraEngine(engine)),
			"dbInstanceClass", core.MappingNodeFromString(serverlessInstanceClass),
		)
	} else {
		spec = core.MappingNodeFields(
			"dbInstanceIdentifier", readerID,
			"sourceDBInstanceIdentifier", instanceRef(r.Name, "dbInstanceIdentifier"),
			"dbInstanceClass", core.MappingNodeFromString(defaultInstanceClass),
		)
	}
	meta := infraMeta(r.Name)
	meta.Labels = labels
	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: resourceTypeDBInstance},
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
	physicalName *core.MappingNode,
	vpcRefs *vpcReferences,
	labels *schema.StringMap,
) (map[string]*schema.Resource, error) {
	roleArnRef, err := shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.arn}", proxyRoleResourceName(r.Name)))
	if err != nil {
		return nil, err
	}

	proxySpec := core.MappingNodeFields(
		"dbProxyName", physicalName,
		"engineFamily", core.MappingNodeFromString(engineFamily(engine)),
		"roleArn", roleArnRef,
		"vpcSubnetIds", vpcRefs.subnetIdsRef,
		"vpcSecurityGroupIds", vpcRefs.proxySecurityGroups,
	)

	iamMode := sqlAuthMode(r) == "iam"
	if iamMode {
		// IAM_AUTH lets the proxy authenticate to the database with IAM as well, so
		// no Secrets Manager secret is needed for the proxy-to-database connection.
		proxySpec.Fields["defaultAuthScheme"] = core.MappingNodeFromString("IAM_AUTH")
		proxySpec.Fields["auth"] = core.MappingNodeItems(
			core.MappingNodeFields("iamAuth", core.MappingNodeFromString("REQUIRED")),
		)
	} else {
		secretArnRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.masterUserSecret.secretArn}", instanceResourceName(r.Name)))
		if err != nil {
			return nil, err
		}
		// The proxy authenticates to the database as the master, and authenticates
		// the application to itself as the application user. Both need a
		// registered secret: a proxy rejects a connection for any role it holds no
		// credentials for, with "This RDS proxy has no credentials for the role",
		// which reads as a missing database role rather than a missing
		// registration.
		//
		// The role itself does not exist until provisioning has run, which is the
		// contract buildAppUserSecret already documents, so registering a secret
		// ahead of the role it names is expected rather than premature.
		authEntries := []*core.MappingNode{
			core.MappingNodeFields(
				"authScheme", core.MappingNodeFromString("SECRETS"),
				"secretArn", secretArnRef,
			),
		}
		if AppUserAuthEnabled(r) {
			appUserSecretRef, err := appUserSecretARNRef(r)
			if err != nil {
				return nil, err
			}
			authEntries = append(authEntries, core.MappingNodeFields(
				"authScheme", core.MappingNodeFromString("SECRETS"),
				"secretArn", appUserSecretRef,
			))
		}
		proxySpec.Fields["auth"] = &core.MappingNode{Items: authEntries}
	}

	proxyMeta := infraMeta(r.Name)
	proxyMeta.Labels = labels

	roleResource, err := buildProxyRole(r, name, physicalName, iamMode)
	if err != nil {
		return nil, err
	}

	return map[string]*schema.Resource{
		proxyResourceName(r.Name): {
			Type:     &schema.ResourceTypeWrapper{Value: "aws/rds/dbProxy"},
			Spec:     proxySpec,
			Metadata: proxyMeta,
			// Selects only the instance this proxy fronts. Registering the instance as a
			// target is not enough on its own: the target stays unavailable until a
			// security group rule permits the connection, and the target group's create
			// then waits on a health it never sees.
			LinkSelector: &schema.LinkSelector{
				ByLabel: &schema.StringMap{
					Values: map[string]string{proxyTargetLabelKey: name},
				},
			},
		},
		proxyRoleResourceName(r.Name): roleResource,
	}, nil
}

// The IAM role trusts rds.amazonaws.com. In password mode it grants
// secretsmanager:GetSecretValue on the instance's RDS-managed master secret so
// the proxy can read credentials; in iam mode it instead grants rds-db:connect
// so the proxy can authenticate to the database with IAM (defaultAuthScheme=IAM_AUTH).
func buildProxyRole(
	r *ResolvedSQLDatabase,
	name string,
	physicalName *core.MappingNode,
	iamMode bool,
) (*schema.Resource, error) {
	// The deployed role name derives from the physical base (not the blueprint
	// resource name) so it is app-scoped whenever the base is; "-proxy-role"
	// keeps a capped generated base within IAM's 64-char role name limit.
	roleName := shared.DerivedName(physicalName, "-proxy-role")
	spec := core.MappingNodeFields(
		"roleName", roleName,
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

	if iamMode {
		// rds-db:connect scoped to the instance's DB user. The dbiResourceId is
		// globally unique, so wildcard region/account still scope the grant to this
		// exact instance and the master user the transformer configures.
		dbConnectResource, err := shared.SubstitutionMappingNode(fmt.Sprintf(
			"arn:aws:rds-db:*:*:dbuser:${resources.%s.spec.dbiResourceId}/%s",
			instanceResourceName(r.Name), defaultMasterUsername))
		if err != nil {
			return nil, err
		}
		spec.Fields["policies"] = core.MappingNodeItems(
			core.MappingNodeFields(
				"policyName", core.MappingNodeFromString(fmt.Sprintf("%s-rds-connect", name)),
				"policyDocument", core.MappingNodeFields(
					"version", core.MappingNodeFromString(policyDocVersion),
					"statement", core.MappingNodeItems(core.MappingNodeFields(
						"effect", core.MappingNodeFromString("Allow"),
						"action", core.MappingNodeFromString("rds-db:connect"),
						"resource", dbConnectResource,
					)),
				),
			),
		)
	} else {
		secretArnRef, err := shared.SubstitutionMappingNode(
			fmt.Sprintf("${resources.%s.spec.masterUserSecret.secretArn}", instanceResourceName(r.Name)))
		if err != nil {
			return nil, err
		}
		// Every secret the proxy is registered with has to be readable by it.
		// A registered secret the role cannot read fails at connection time just
		// as completely as one that was never registered.
		secretResources := []*core.MappingNode{secretArnRef}
		if AppUserAuthEnabled(r) {
			appUserSecretRef, err := appUserSecretARNRef(r)
			if err != nil {
				return nil, err
			}
			secretResources = append(secretResources, appUserSecretRef)
		}
		spec.Fields["policies"] = core.MappingNodeItems(
			core.MappingNodeFields(
				"policyName", core.MappingNodeFromString(fmt.Sprintf("%s-secret-access", name)),
				"policyDocument", core.MappingNodeFields(
					"version", core.MappingNodeFromString(policyDocVersion),
					"statement", core.MappingNodeItems(core.MappingNodeFields(
						"effect", core.MappingNodeFromString("Allow"),
						"action", core.MappingNodeFromString("secretsmanager:GetSecretValue"),
						"resource", &core.MappingNode{Items: secretResources},
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

// AuthTargetForHandler returns the concrete resource a handler links to for the
// given celerity/sqlDatabase — the Aurora cluster when the Aurora path is enabled,
// otherwise the RDS Proxy — together with the database's auth mode ("iam" or
// "password"). The handler emit uses this to stamp the
// aws.lambda.rds.<target>.authMode link annotation the provider consumes to grant
// the function's execution role rds-db:connect in iam mode.
func AuthTargetForHandler(
	run *transformutils.Run,
	dbAbstractName string,
	dbSpec *core.MappingNode,
) (target, mode string) {
	mode = "password"
	if node, ok := pluginutils.GetValueByPath("$.authMode", dbSpec); ok {
		if value := core.StringValue(node); value != "" {
			mode = value
		}
	}

	// Aurora deploy config is keyed by the database's spec name (falling back to the
	// abstract resource name), matching emitSQLDatabase.
	configName := core.StringValue(mappingNodeAtPath(dbSpec, "$.name"))
	if configName == "" {
		configName = dbAbstractName
	}
	if core.BoolValue(auroraConfig(run, configName, "enabled")) {
		return clusterResourceName(dbAbstractName), mode
	}
	return proxyResourceName(dbAbstractName), mode
}

func mappingNodeAtPath(spec *core.MappingNode, path string) *core.MappingNode {
	node, _ := pluginutils.GetValueByPath(path, spec)
	return node
}

// Returns the given labels plus one more, without mutating the input.
// The label map is shared across every resource this database emits, so writing
// into it would put the new label on all of them.
func labelsWith(labels *schema.StringMap, key, value string) *schema.StringMap {
	values := map[string]string{}
	if labels != nil {
		maps.Copy(values, labels.Values)
	}
	// Assigned last so a user label reusing this key cannot displace it.
	values[key] = value
	return &schema.StringMap{Values: values}
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

// References the application user's secret by the field that
// carries its ARN.
//
// spec.id, not spec.arn: a Secrets Manager secret has no arn field, and a
// reference to one stages cleanly and then fails at deploy time. The maintenance
// runner references the same secret the same way.
func appUserSecretARNRef(r *ResolvedSQLDatabase) (*core.MappingNode, error) {
	return shared.SubstitutionMappingNode(
		fmt.Sprintf("${resources.%s.spec.id}", AppUserSecretResourceName(r.Name)),
	)
}
