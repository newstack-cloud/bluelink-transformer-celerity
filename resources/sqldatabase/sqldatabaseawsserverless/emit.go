package sqldatabaseawsserverless

import (
	"context"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase/sqldatabaseaws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformutils"
)

// Emit builds the database and adds the maintenance runner to it.
func Emit(
	ctx context.Context,
	run *transformutils.Run,
	r *sqldatabase.ResolvedSQLDatabase,
	rw transformutils.ResourcePropertyRewriter,
) (*transformutils.EmitResult, error) {
	return sqldatabaseaws.EmitDatabase(
		ctx, run, shared.AWSServerless, r, rw,
		func(emitted *sqldatabaseaws.EmittedDatabase) (
			map[string]*schema.Resource, []*core.Diagnostic, error,
		) {
			return maintenanceRunnerFor(r, run, emitted)
		},
	)
}

// The application user's credential authenticates as a role that does not exist
// until something inside the VPC creates it, and nothing outside can reach the
// database to do so. The runner is what closes that gap, for provisioning now
// and migrations next.
func maintenanceRunnerFor(
	r *sqldatabase.ResolvedSQLDatabase,
	run *transformutils.Run,
	emitted *sqldatabaseaws.EmittedDatabase,
) (map[string]*schema.Resource, []*core.Diagnostic, error) {
	resources, diagnostics, err := buildMaintenanceRunner(r, maintenanceRunnerInput{
		name:          emitted.Name,
		physicalName:  emitted.PhysicalName,
		auroraEnabled: emitted.AuroraEnabled,
		manifest:      buildManifest(run),
	})
	if err != nil {
		return nil, nil, err
	}

	// The runner needs a network path to whatever the application connects
	// through, which is what this label opens.
	markMaintenanceTarget(emitted.ConnectionTarget, r.Name)

	return resources, diagnostics, nil
}
