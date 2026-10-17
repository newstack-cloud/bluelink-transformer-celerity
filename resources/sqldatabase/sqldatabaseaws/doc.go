// Package sqldatabaseaws is the AWS implementation of celerity/sqlDatabase: the
// RDS resources a database maps to, standalone or Aurora, with the subnet
// group, proxy and application-user secret that go with them.
//
// It declares no Implementation of its own. Every AWS target needs more than the
// database, because something has to reach inside the VPC to create the
// application role, so EmitDatabase takes an Extender and the target composes
// the two. See sqldatabaseawsserverless.
package sqldatabaseaws
