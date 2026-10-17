package sqldatabase

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

const (
	// AppUserName is the database role the application connects as.
	//
	// Not the master user. The master is effectively a superuser, able to drop
	// any table, create roles and read every database on the server, and the
	// connection pooler already holds its credentials to authenticate to the
	// database on the application's behalf. Handing the same credentials to
	// every handler would undo the per-handler role work and give a compromised
	// handler the whole server.
	AppUserName = "celerity_app"

	// MasterUsername is the owner role every database is created with.
	//
	// Not authored. The role has to be known before the database exists, because
	// the maintenance runner authenticates as it to create the application role,
	// and both sides have to name the same one. Making it authorable means
	// declaring it in the schema and reading it in both places at once.
	MasterUsername = "celerity"
)

// Engine reports the database engine, defaulting to postgres.
//
// One of the connection details a deployed handler needs. These are read from
// the abstract spec so that every deploy target agrees on them, and they are
// read by the transformer's resources store as well as by each target's emit.
func Engine(resource *schema.Resource) string {
	if engine := specStringField(resource, "$.engine"); engine != "" {
		return engine
	}

	return "postgres"
}

// Port is the engine's port. A literal rather than a reference to an emitted
// spec: a lone substitution keeps the type of what it points at, and the
// parameter tree holds strings only.
func Port(resource *schema.Resource) int {
	if Engine(resource) == "mysql" {
		return 3306
	}

	return 5432
}

// AuthMode is "password" or "iam"; it decides whether the application is given a
// credential at all.
func AuthMode(resource *schema.Resource) string {
	if mode := specStringField(resource, "$.authMode"); mode != "" {
		return mode
	}

	return "password"
}

// ReadReplicasEnabled reports whether a reader endpoint exists to publish.
func ReadReplicasEnabled(resource *schema.Resource) bool {
	if resource == nil {
		return false
	}

	return specBoolField(resource, "$.readReplicas")
}

// AppUserAuthEnabled reports whether the database hands the application a
// generated user and password.
//
// IAM authentication needs no secret: the handler is granted a short-lived
// token in exchange for its own identity. The role still has to exist in the
// database and hold the engine's IAM privilege, so provisioning runs either
// way. The difference is only whether a credential is distributed.
func AppUserAuthEnabled(resource *schema.Resource) bool {
	return AuthMode(resource) != "iam"
}

func specStringField(resource *schema.Resource, path string) string {
	if resource == nil {
		return ""
	}
	node, ok := pluginutils.GetValueByPath(path, resource.Spec)
	if !ok {
		return ""
	}

	return core.StringValue(node)
}

func specBoolField(resource *schema.Resource, path string) bool {
	if resource == nil {
		return false
	}
	node, ok := pluginutils.GetValueByPath(path, resource.Spec)
	if !ok {
		return false
	}

	return core.BoolValue(node)
}
