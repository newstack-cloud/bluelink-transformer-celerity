package sqldatabaseaws

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/sqldatabase"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

const (
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
	r *sqldatabase.ResolvedSQLDatabase,
	name string,
	physicalName *core.MappingNode,
	labels *schema.StringMap,
) (*schema.Resource, error) {
	template, err := json.Marshal(map[string]string{"username": sqldatabase.AppUserName})
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

	meta := InfraMetadata(r.Name)
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
