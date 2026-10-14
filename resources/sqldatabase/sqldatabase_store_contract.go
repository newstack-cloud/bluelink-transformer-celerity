package sqldatabase

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// What a deployed handler needs in order to open a connection to this database,
// as read by the SDK's SqlDatabaseLayer from the internal resources config
// namespace.
//
// Exported from the package that emits the resources being named so the two
// cannot drift: a store entry pointing at a concrete resource or blueprint value
// this package never produced resolves to nothing, and the handler fails at cold
// start on a missing config key rather than at deploy time.

// HostValueKey is the blueprint value holding the endpoint the application
// connects to. This is the proxy when one is emitted, the Aurora writer endpoint on the
// Aurora path, otherwise the instance endpoint.
func HostValueKey(abstractName string) string {
	return dbHostKey(abstractName)
}

// ReadHostValueKey is the reader endpoint, present only when read replicas are
// enabled.
func ReadHostValueKey(abstractName string) string {
	return dbReadHostKey(abstractName)
}

// DatabaseNameValueKey is the database name inside the server.
func DatabaseNameValueKey(abstractName string) string {
	return dbDatabaseNameKey(abstractName)
}

// Engine reports the database engine, defaulting to postgres to match the
// emitted instance.
func Engine(resource *schema.Resource) string {
	if engine := specStringField(resource, "$.engine"); engine != "" {
		return engine
	}
	return "postgres"
}

// Port is the port for the engine. A literal rather than a reference to the
// emitted spec: a lone substitution keeps the type of what it points at, and the
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
