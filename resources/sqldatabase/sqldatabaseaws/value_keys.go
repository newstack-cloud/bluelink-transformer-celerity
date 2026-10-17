package sqldatabaseaws

// HostValueKey is the endpoint the application connects to: the proxy when one
// is emitted, the Aurora writer endpoint on the Aurora path, otherwise the
// instance endpoint.
//
// This and its siblings name blueprint values this package emits, so that the
// transformer's resources store and each AWS target reference them rather than
// spelling them out. Exported from the package that emits them so the two
// cannot drift: a store entry pointing at a value this package never produced
// resolves to nothing, and the handler fails at cold start on a missing config
// key rather than at deploy time.
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
