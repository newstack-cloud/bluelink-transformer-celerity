package sqldatabase

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

const (
	// AppUserName is the database role the application connects as.
	//
	// Not the master user. The master is effectively a superuser — it can drop
	// any table, create roles and read every database on the instance — and the
	// RDS Proxy already holds its credentials to authenticate to the database on
	// the application's behalf. Handing the same credentials to every handler
	// would undo the per-handler role work and give a compromised handler the
	// whole instance.
	AppUserName = "celerity_app"

	// appUserPasswordLength is generous because nothing types this password; it
	// is generated into a secret and read from there.
	appUserPasswordLength = 40

	// Excluded from generated passwords because they terminate or escape values
	// in connection strings and psql arguments, and a credential that cannot be
	// pasted into a URL causes support requests years later.
	appUserExcludeCharacters = `"'@/\ `
)

const (
	// AnnotationKeySecretRole says which credential a Secrets Manager secret
	// emitted for a database holds.
	//
	// A celerity/sqlDatabase now produces more than one secret, and they carry
	// the same abstract-resource annotation, so anything reading state back has
	// to be able to tell them apart. Without this, the migration path picks
	// whichever it finds first and can end up trying to run DDL as the
	// application user — which fails, and fails in a way that looks like a
	// permissions bug in the grants rather than the wrong credential.
	AnnotationKeySecretRole = "celerity.sqlDatabase.secret.role"

	// SecretRoleAppUser marks the application user's credential.
	SecretRoleAppUser = "app-user"
)

// AppUserSecretResourceName is the Secrets Manager secret holding the
// application user's credentials.
func AppUserSecretResourceName(abstractName string) string {
	return fmt.Sprintf("%s_sqldb_app_user_secret", abstractName)
}

// buildAppUserSecret creates the secret the application authenticates with.
//
// Shaped as {"username": ..., "password": ...}, matching an AWS-managed RDS
// secret, so the same readers and any rotation tooling work against either.
// Only the password is generated; the username is fixed and travels in the
// template.
//
// The secret exists from the moment the database is deployed, but the role
// inside the database does not: creating it requires executing SQL, which needs
// something running inside the VPC. Provisioning therefore happens after the
// deploy, and until it has run the application cannot authenticate. That is the
// same contract the schema migration step already sets — handlers must tolerate
// a database that is not ready yet.
func buildAppUserSecret(
	r *ResolvedSQLDatabase,
	name string,
	physicalName *core.MappingNode,
	labels *schema.StringMap,
) (*schema.Resource, error) {
	template, err := json.Marshal(map[string]string{"username": AppUserName})
	if err != nil {
		return nil, err
	}

	// The database's own labels, so a handler that links to the database also
	// links to this secret and is granted GetSecretValue on it — the same
	// mechanism the cache's auth secret uses.
	secretLabels := map[string]string{}
	if labels != nil {
		maps.Copy(secretLabels, labels.Values)
	}

	meta := infraMeta(r.Name)
	if len(secretLabels) > 0 {
		meta.Labels = &schema.StringMap{Values: secretLabels}
	}
	meta.Annotations.Values[AnnotationKeySecretRole] =
		pluginutils.StringToSubstitutions(SecretRoleAppUser)

	return &schema.Resource{
		Type: &schema.ResourceTypeWrapper{Value: "aws/secretsmanager/secret"},
		Spec: core.MappingNodeFields(
			"name", shared.DerivedName(physicalName, "-app-user"),
			"description", core.MappingNodeFromString(
				fmt.Sprintf("Application user credentials for Celerity database %s", name)),
			"generateSecretString", core.MappingNodeFields(
				"secretStringTemplate", core.MappingNodeFromString(string(template)),
				"generateStringKey", core.MappingNodeFromString("password"),
				"passwordLength", core.MappingNodeFromInt(appUserPasswordLength),
				"excludeCharacters", core.MappingNodeFromString(appUserExcludeCharacters),
			),
		),
		Metadata: meta,
	}, nil
}

// AppUserAuthEnabled reports whether the database hands the application a
// generated user and password.
//
// IAM authentication needs no secret: the handler is granted rds-db:connect and
// exchanges its role for a short-lived token. The role still has to exist in the
// database and hold rds_iam, so provisioning runs either way — the difference is
// only whether a credential is distributed.
func AppUserAuthEnabled(r *ResolvedSQLDatabase) bool {
	return sqlAuthMode(r) != "iam"
}

// specStringField reads a plain string from an abstract resource's spec.
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

// specBoolField reads a plain bool from an abstract resource's spec.
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
