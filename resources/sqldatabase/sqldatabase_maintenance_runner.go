package sqldatabase

import (
	"encoding/json"
	"fmt"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

const (
	// AnnotationKeyMaintenanceRunner marks the function as the maintenance
	// runner, so the CLI can find it in deployed state without depending on a
	// naming convention the two sides would have to keep in step.
	AnnotationKeyMaintenanceRunner = "celerity.maintenance.runner"

	// AnnotationValueMaintenanceRunner is the value that annotation carries.
	AnnotationValueMaintenanceRunner = "true"

	// maintenanceTargetLabelKey marks the resources the runner needs a network
	// path to.
	//
	// A distinct key rather than the database's own labels, for the same reason
	// proxyTargetLabelKey is: those labels are on every resource this database
	// emits, so selecting on them would link the runner to its subnet group and
	// role as well.
	maintenanceTargetLabelKey = "celerity.internal.sqldatabase.maintenanceTarget"

	// maintenanceRunnerRuntime is Lambda's provided runtime family, which
	// executes a file named bootstrap. The runner is a compiled Go binary, not
	// application code.
	maintenanceRunnerRuntime = "provided.al2023"

	// maintenanceRunnerHandler is ignored by the provided runtimes but is a
	// required field.
	maintenanceRunnerHandler = "bootstrap"

	// maintenanceRunnerArchitecture must match what the CLI cross-compiles the
	// runner binary for, which is arm64.
	//
	// Declaring it matters because Lambda defaults to x86_64 when the field is
	// absent, and the mismatch is only reported at invocation, as
	// "Runtime exited with error: exit status 126" with nothing naming the
	// architecture. A binary the platform cannot execute is indistinguishable
	// from one that is not executable at all.
	maintenanceRunnerArchitecture = "arm64"

	// maintenanceRunnerMemory is generous for what the work needs, because
	// Lambda scales CPU with it and the operator is waiting on the result.
	maintenanceRunnerMemory = 512

	// maintenanceRunnerTimeout is Lambda's ceiling. Provisioning takes seconds;
	// migrations, which this will also carry, are what can approach it.
	maintenanceRunnerTimeout = 900

	// The environment variable and host contract, mirroring
	// internal/maintenancerunner in the CLI.
	maintenanceTargetsEnvVar = "CELERITY_MAINTENANCE_TARGETS"

	// maintenanceRunnerSubnetType places the runner where the database is.
	// Nothing invokes it over the network — invocation is a control-plane call
	// — so it never needs a public subnet.
	maintenanceRunnerSubnetType = "private"

	// annotationVPCSubnetType is the placement annotation the
	// aws/flex/vpc::aws/lambda/function link reads to build the function's
	// vpcConfig. The transformer never writes vpcConfig itself.
	annotationVPCSubnetType = "aws.flexvpc.lambda.subnetType"
)

// buildManifest returns the build manifest for this run, or nil when there is
// none. A missing manifest is normal during validation and before the first
// build, and is handled by emitting a placeholder rather than failing.
func buildManifest(run *transformutils.Run) *build.Manifest {
	manifest, has := transformutils.Use[*build.Manifest](run)
	if !has {
		return nil
	}
	return manifest
}

// MaintenanceRunnerResourceName is the runner's function resource.
func MaintenanceRunnerResourceName(abstractName string) string {
	return fmt.Sprintf("%s_sqldb_maintenance_runner", abstractName)
}

// MaintenanceRunnerRoleResourceName is the runner's execution role.
func MaintenanceRunnerRoleResourceName(abstractName string) string {
	return fmt.Sprintf("%s_sqldb_maintenance_runner_role", abstractName)
}

// maintenanceRunnerInput is everything the runner emission needs that the
// caller has already worked out.
type maintenanceRunnerInput struct {
	name          string
	physicalName  *core.MappingNode
	auroraEnabled bool
	manifest      *build.Manifest
}

// buildMaintenanceRunner emits the function and role that let the CLI run
// privileged SQL against a database it cannot reach.
//
// A Celerity database is in private subnets, so `celerity schema apply` and the
// application-user provisioning it depends on cannot connect from an operator's
// machine. Invoking a function is a control-plane call and reaches one from
// anywhere; a function inside the VPC has a data-plane path to the database.
//
// The runner carries only the labels its VPC selects members by, so it is
// placed alongside the database without being caught by any other selector, and
// it selects the database's concrete resources on a dedicated internal label so
// it links to those and nothing else.
func buildMaintenanceRunner(
	r *ResolvedSQLDatabase,
	in maintenanceRunnerInput,
) (map[string]*schema.Resource, []*core.Diagnostic, error) {
	code, diagnostics := maintenanceRunnerCode(r, in)

	targets, err := maintenanceTargetsEnv(r, in)
	if err != nil {
		return nil, nil, err
	}

	roleRef, err := shared.SubstitutionMappingNode(fmt.Sprintf(
		"${resources.%s.spec.arn}", MaintenanceRunnerRoleResourceName(r.Name)))
	if err != nil {
		return nil, nil, err
	}

	spec := core.MappingNodeFields(
		"functionName", shared.DerivedName(in.physicalName, "-maintenance"),
		"handler", core.MappingNodeFromString(maintenanceRunnerHandler),
		"runtime", core.MappingNodeFromString(maintenanceRunnerRuntime),
		"architectures", core.MappingNodeFromStringSlice(
			[]string{maintenanceRunnerArchitecture},
		),
		"code", code,
		"memorySize", core.MappingNodeFromInt(maintenanceRunnerMemory),
		"timeout", core.MappingNodeFromInt(maintenanceRunnerTimeout),
		"role", roleRef,
		"environment", core.MappingNodeFields(
			"variables", core.MappingNodeFields(maintenanceTargetsEnvVar, targets),
		),
	)

	meta := infraMeta(r.Name)
	meta.Annotations.Values[AnnotationKeyMaintenanceRunner] =
		pluginutils.StringToSubstitutions(AnnotationValueMaintenanceRunner)
	meta.Annotations.Values[annotationVPCSubnetType] =
		pluginutils.StringToSubstitutions(maintenanceRunnerSubnetType)
	// Exactly the labels the VPC selects members by, and no others.
	//
	// The database's own label set is the tempting thing to copy, and it is
	// wrong: it carries the generated celerity.handler.uses.<db> label that
	// every handler using the database selects on. A runner wearing that gets
	// selected by all of them, which forms a link from each handler to the
	// runner and grants them invoke permission on it, and this is the one function that
	// can read the master credential.
	if len(r.VPCSelectorLabels) > 0 {
		meta.Labels = &schema.StringMap{Values: r.VPCSelectorLabels}
	}

	runner := &schema.Resource{
		Type:         &schema.ResourceTypeWrapper{Value: "aws/lambda/function"},
		Spec:         spec,
		Metadata:     meta,
		LinkSelector: maintenanceTargetSelector(r.Name),
	}

	role, err := buildMaintenanceRunnerRole(r, in)
	if err != nil {
		return nil, nil, err
	}

	return map[string]*schema.Resource{
		MaintenanceRunnerResourceName(r.Name):     runner,
		MaintenanceRunnerRoleResourceName(r.Name): role,
	}, diagnostics, nil
}

// maintenanceTargetSelector selects the concrete resources the runner needs a
// network path to.
func maintenanceTargetSelector(abstractName string) *schema.LinkSelector {
	return &schema.LinkSelector{
		ByLabel: &schema.StringMap{
			Values: map[string]string{maintenanceTargetLabelKey: abstractName},
		},
	}
}

// markMaintenanceTarget labels a resource as something the runner connects to.
func markMaintenanceTarget(resource *schema.Resource, abstractName string) {
	if resource == nil || resource.Metadata == nil {
		return
	}
	if resource.Metadata.Labels == nil {
		resource.Metadata.Labels = &schema.StringMap{Values: map[string]string{}}
	}
	resource.Metadata.Labels.Values[maintenanceTargetLabelKey] = abstractName
}

// maintenanceRunnerCode resolves the runner's code asset from the build
// manifest.
//
// A missing artefact follows the same contract handlers use for a missing
// manifest: emit a placeholder that stages and validates but cannot deploy,
// with a warning naming what to run. Failing the transform here would break
// `celerity validate` for anyone who has not built yet.
func maintenanceRunnerCode(
	r *ResolvedSQLDatabase,
	in maintenanceRunnerInput,
) (*core.MappingNode, []*core.Diagnostic) {
	if in.manifest != nil && in.manifest.Lambda != nil && in.manifest.Lambda.MaintenanceRunner != nil {
		artifact := in.manifest.Lambda.MaintenanceRunner
		return core.MappingNodeFields(
			"s3Bucket", core.MappingNodeFromString(artifact.S3Bucket),
			"s3Key", core.MappingNodeFromString(artifact.S3Key),
		), nil
	}

	return core.MappingNodeFields(
			"s3Bucket", core.MappingNodeFromString("placeholder-bucket"),
			"s3Key", core.MappingNodeFromString("placeholder-key"),
		), []*core.Diagnostic{{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"no maintenance runner artefact in the build manifest for celerity/sqlDatabase %q; "+
					"the runner is emitted with a placeholder code location that cannot deploy, and "+
					"the application database user cannot be provisioned without it. "+
					"Run \"celerity build\" (validation and a dry run/plan are still valid)",
				in.name),
			Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
		}}
}

// maintenanceTargetsEnv builds the runner's target configuration.
//
// A JSON object keyed by the abstract database name, so a request names a
// database and carries nothing else: whoever can invoke the runner cannot point
// it at a database of their choosing. Endpoints and secret ARNs are only known
// at deploy, so they interpolate — inside a larger string, which coerces to
// text, unlike a lone substitution that would keep the type of what it points
// at.
func maintenanceTargetsEnv(
	r *ResolvedSQLDatabase,
	in maintenanceRunnerInput,
) (*core.MappingNode, error) {
	iamMode := sqlAuthMode(r) == "iam"

	target := map[string]string{
		"engine":   Engine(r.Resource),
		"host":     fmt.Sprintf("${values.%s}", HostValueKey(r.Name)),
		"port":     fmt.Sprintf("%d", Port(r.Resource)),
		"database": fmt.Sprintf("${values.%s}", DatabaseNameValueKey(r.Name)),
		"appUser":  AppUserName,
	}

	if iamMode {
		// Both credentials are platform-issued, the runner exchanges its own
		// execution role for the owner, and the application user holds the
		// engine's IAM role rather than a password.
		target["ownerUser"] = masterUsername(r)
		target["appUserTokenAuth"] = "true"
	} else {
		target["ownerCredentialRef"] = fmt.Sprintf(
			"${resources.%s.spec.masterUserSecret.secretArn}", masterSecretHolder(r, in.auroraEnabled))
		target["appUserCredentialRef"] = fmt.Sprintf(
			"${resources.%s.spec.id}", AppUserSecretResourceName(r.Name))
	}

	encoded, err := json.Marshal(map[string]any{r.Name: targetJSON(target, iamMode)})
	if err != nil {
		return nil, err
	}
	return shared.SubstitutionMappingNode(string(encoded))
}

// targetJSON converts the flat string map into the runner's Target shape,
// giving the one boolean field its real type.
func targetJSON(target map[string]string, iamMode bool) map[string]any {
	out := map[string]any{}
	for key, value := range target {
		if key == "appUserTokenAuth" {
			continue
		}
		out[key] = value
	}
	if iamMode {
		out["appUserTokenAuth"] = true
	}
	return out
}

// masterSecretHolder is the resource carrying the RDS-managed master secret:
// the cluster on Aurora, the instance otherwise.
func masterSecretHolder(r *ResolvedSQLDatabase, auroraEnabled bool) string {
	if auroraEnabled {
		return clusterResourceName(r.Name)
	}
	return instanceResourceName(r.Name)
}

func masterUsername(r *ResolvedSQLDatabase) string {
	if user := core.StringValue(specGet(r, "$.masterUsername")); user != "" {
		return user
	}
	return defaultMasterUsername
}
