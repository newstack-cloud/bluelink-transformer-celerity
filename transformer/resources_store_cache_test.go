//go:build unit

package transformer

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type ResourcesStoreCacheTestSuite struct {
	suite.Suite
}

func TestResourcesStoreCacheTestSuite(t *testing.T) {
	suite.Run(t, new(ResourcesStoreCacheTestSuite))
}

// The exact set of keys the SDK's CacheLayer reads out of the resources
// namespace, pinned here so this side and the CLI's local seeder cannot drift
// apart silently. Adding a key the SDK does not read is harmless; failing to
// write one it does read breaks every handler using a cache, at cold start, on
// deployed infrastructure only.
var wantCacheStoreKeys = []string{
	"_authMode",
	"_authTokenSecretId",
	"_clusterMode",
	"_host",
	"_port",
	"_tls",
}

func cacheBackedTransform(s *suite.Suite, cacheSpec *core.MappingNode) map[string]*schema.Resource {
	handlerRes := handlerLinkedTo(map[string]string{"app": "sessions"})
	cacheRes := &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "celerity/cache"},
		Spec:     cacheSpec,
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"app": "sessions"}}},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"saveOrder": handlerRes, "appCache": cacheRes,
	}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          edges(edge("saveOrder", "appCache", "celerity/handler", "celerity/cache")),
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	return out.TransformedBlueprint.Resources.Values
}

// A cache needs a host, a port and how to authenticate — one identifier does not
// describe a connection — so it contributes several keys rather than the single
// entry the other backing types need.
func (s *ResourcesStoreCacheTestSuite) Test_cache_contributes_its_full_key_set() {
	resources := cacheBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("sessions"),
	))

	store := resources["celerityResourcesConfigStore"]
	s.Require().NotNil(store, "a handler linked to a cache needs the resources store")
	values := store.Spec.Fields["values"]
	s.Require().NotNil(values)

	got := []string{}
	for key := range values.Fields {
		if suffix, found := strings.CutPrefix(key, "sessions"); found {
			got = append(got, suffix)
		}
	}
	sort.Strings(got)

	s.Equal(wantCacheStoreKeys, got)
}

// The regression this whole change exists for. A handler whose only backing
// resource is a cache was left out of hasResourceLinks, so it got no
// CELERITY_CONFIG_RESOURCES_STORE_ID and threw `Config namespace "resources" not
// registered` on every invocation — after deploying perfectly cleanly.
func (s *ResourcesStoreCacheTestSuite) Test_cache_only_handler_can_find_the_store() {
	resources := cacheBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("sessions"),
	))

	lambda := resources["saveOrder_lambda_func"]
	s.Require().NotNil(lambda)
	env := lambda.Spec.Fields["environment"].Fields["variables"].Fields

	s.Equal(
		"/celerity/placeholder-app/resources",
		core.StringValue(env["CELERITY_CONFIG_RESOURCES_STORE_ID"]),
		"a cache-only handler must still be told where the resources store is",
	)
	s.Equal("parameter-store", core.StringValue(env["CELERITY_CONFIG_RESOURCES_STORE_KIND"]))
}

// The AUTH token is generated into Secrets Manager and can rotate. The store
// carries a reference to it, never the value: a copy in the parameter store is a
// second copy of a credential that nothing updates, and it would start failing at
// some later rotation for no visible reason.
func (s *ResourcesStoreCacheTestSuite) Test_auth_token_is_referenced_not_copied() {
	resources := cacheBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("sessions"),
	))

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"]
	authToken := values.Fields["sessions_authTokenSecretId"]
	s.Require().NotNil(authToken)

	// The attribute is asserted, not just the resource: an earlier version of
	// this referenced spec.arn, which a Secrets Manager secret does not have —
	// its Cloud Control primary identifier is the ARN. Checking only the name
	// let that through to a staging failure.
	name, path := resourceRefNameAndPath(authToken)
	s.Equal("appCache_cache_auth_secret", name)
	s.Equal([]string{"spec", "id"}, path)
	s.Empty(core.StringValue(authToken), "the token must not be written as a literal")
}

// The keys an iam-auth cache contributes. It carries no AUTH token secret,
// since there is no token, and it must carry the RBAC user the handler
// authenticates as: without it the cache deploys cleanly and every handler
// using it fails at cold start on a missing config key.
//
// No _region: the region a connection token is signed in is settled at deploy
// rather than at transform, and the execution environment already carries it.
var wantCacheIAMStoreKeys = []string{
	"_authMode",
	"_clusterMode",
	"_host",
	"_port",
	"_tls",
	"_user",
}

func (s *ResourcesStoreCacheTestSuite) Test_iam_cache_keys() {
	resources := cacheBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("sessions"),
		"authMode", core.MappingNodeFromString("iam"),
	))

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"]
	suffixes := []string{}
	for key := range values.Fields {
		if suffix, found := strings.CutPrefix(key, "sessions"); found {
			suffixes = append(suffixes, suffix)
		}
	}
	sort.Strings(suffixes)

	s.Equal(wantCacheIAMStoreKeys, suffixes)
	s.Equal("iam", core.StringValue(values.Fields["sessions_authMode"]))
}

// The user is a reference to the RBAC user this transform emitted, not a
// literal: the id is settled at deploy, and a store entry naming a user nothing
// emitted resolves to nothing.
func (s *ResourcesStoreCacheTestSuite) Test_iam_user_is_referenced_from_the_emitted_user() {
	resources := cacheBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("sessions"),
		"authMode", core.MappingNodeFromString("iam"),
	))

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"]
	name, path := resourceRefNameAndPath(values.Fields["sessions_user"])

	s.Equal("appCache_cache_iam_user", name)
	s.Equal([]string{"spec", "userId"}, path)
	s.Require().NotNil(resources["appCache_cache_iam_user"], "the user it names must be emitted")
}

// A password-auth cache names no user: the AUTH token is the whole credential,
// and a user key would have the SDK take the iam path.
func (s *ResourcesStoreCacheTestSuite) Test_password_cache_names_no_user() {
	resources := cacheBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("sessions"),
	))

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"]
	s.Nil(values.Fields["sessions_user"])
}

// The client has to be told it is talking to a cluster separately from the
// endpoint it connects to, so clusterMode travels as its own key.
func (s *ResourcesStoreCacheTestSuite) Test_cluster_mode_is_carried_through() {
	resources := cacheBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("sessions"),
		"clusterMode", core.MappingNodeFromBool(true),
	))

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"]
	s.Equal("true", core.StringValue(values.Fields["sessions_clusterMode"]))
}

// The SQL database's key set, pinned for the same reason as the cache's. The
// credential is a reference to the application user's secret, never the master's
// and never a copied value.
var wantSQLStoreKeys = []string{
	"_authMode",
	"_credentialsSecretId",
	"_database",
	"_engine",
	"_host",
	"_port",
	"_ssl",
	"_user",
}

func sqlBackedTransform(s *suite.Suite, dbSpec *core.MappingNode) map[string]*schema.Resource {
	handlerRes := handlerLinkedTo(map[string]string{"app": "orders"})
	dbRes := &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: "celerity/sqlDatabase"},
		Spec:     dbSpec,
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}}},
	}

	bp := &schema.Blueprint{Resources: &schema.ResourceMap{Values: map[string]*schema.Resource{
		"saveOrder": handlerRes, "ordersDb": dbRes,
	}}}
	out, err := NewTransformer(&shared.Dependencies{}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     bp,
			LinkGraph:          edges(edge("saveOrder", "ordersDb", "celerity/handler", "celerity/sqlDatabase")),
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)
	return out.TransformedBlueprint.Resources.Values
}

func (s *ResourcesStoreCacheTestSuite) Test_sql_database_contributes_its_full_key_set() {
	resources := sqlBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("orders"),
		"engine", core.MappingNodeFromString("postgres"),
	))

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"]
	s.Require().NotNil(values)

	got := []string{}
	for key := range values.Fields {
		if suffix, found := strings.CutPrefix(key, "orders"); found {
			got = append(got, suffix)
		}
	}
	sort.Strings(got)

	s.Equal(wantSQLStoreKeys, got)
}

// The application connects as its own role. The master user can drop any table
// and read every database on the instance; the proxy already holds it to reach
// the database, and a handler has no reason to.
func (s *ResourcesStoreCacheTestSuite) Test_sql_user_is_the_app_user_not_the_master() {
	resources := sqlBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("orders"),
	))

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"]
	s.Equal("celerity_app", core.StringValue(values.Fields["orders_user"]))

	name, path := resourceRefNameAndPath(values.Fields["orders_credentialsSecretId"])
	s.Equal("ordersDb_sqldb_app_user_secret", name)
	s.Equal([]string{"spec", "id"}, path)
}

// A database-backed handler needs the namespace as much as a cache-backed one;
// leaving it out is what broke every cache handler.
func (s *ResourcesStoreCacheTestSuite) Test_sql_only_handler_can_find_the_store() {
	resources := sqlBackedTransform(&s.Suite, core.MappingNodeFields(
		"name", core.MappingNodeFromString("orders"),
	))

	lambda := resources["saveOrder_lambda_func"]
	s.Require().NotNil(lambda)
	env := lambda.Spec.Fields["environment"].Fields["variables"].Fields
	s.Equal(
		"/celerity/placeholder-app/resources",
		core.StringValue(env["CELERITY_CONFIG_RESOURCES_STORE_ID"]),
	)
}
