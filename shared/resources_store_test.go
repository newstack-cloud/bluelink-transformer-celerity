//go:build unit

package shared

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/stretchr/testify/suite"
)

type ResourcesStoreTestSuite struct {
	suite.Suite
}

func TestResourcesStoreTestSuite(t *testing.T) {
	suite.Run(t, new(ResourcesStoreTestSuite))
}

// A target with nothing AWS about it, standing in for a second backend. If the
// store's assembly has leaked a target assumption, this cannot satisfy it.
type fakeStoreTarget struct{}

func (fakeStoreTarget) StoreResourceType() string {
	return "fake/config/store"
}

func (fakeStoreTarget) StoreSeedSpec(
	appName, storePath string,
	values *core.MappingNode,
) *core.MappingNode {
	return core.MappingNodeFields(
		"location", core.MappingNodeFromString(storePath),
		"owner", core.MappingNodeFromString(appName),
		"entries", values,
	)
}

func (fakeStoreTarget) BackingIdentifier(
	celerityType string,
	abstractName string,
	_ *schema.Resource,
) (BackingIdentifier, bool) {
	if celerityType == "celerity/datastore" {
		// A target that does not resolve this type through the store at all.
		return BackingIdentifier{}, false
	}

	return BackingIdentifier{
		Value:  core.MappingNodeFromString("fake://" + abstractName),
		Region: "region-one",
	}, true
}

func (fakeStoreTarget) CacheConnection(
	abstractName string,
	_ *schema.Resource,
) (CacheConnection, bool) {
	return CacheConnection{
		Host:        core.MappingNodeFromString("cache.example"),
		Port:        1234,
		TLS:         true,
		ClusterMode: false,
		AuthMode:    "password",
		AuthTokenSecretID: core.MappingNodeFromString(
			"fake-secret://" + abstractName,
		),
	}, true
}

func (fakeStoreTarget) DatabaseConnection(
	abstractName string,
	_ *schema.Resource,
) (DatabaseConnection, bool) {
	return DatabaseConnection{
		Host:     core.MappingNodeFromString("db.example"),
		Database: core.MappingNodeFromString(abstractName),
		Port:     5555,
		Engine:   "postgres",
		User:     "app",
		SSL:      true,
		AuthMode: "iam",
	}, true
}

// The key vocabulary is the contract every SDK reads on every target, so a
// non-AWS target writing through this assembly must produce exactly these keys.
func (s *ResourcesStoreTestSuite) Test_a_non_aws_target_produces_the_portable_vocabulary() {
	entries := NewResourcesStoreEntries()
	target := fakeStoreTarget{}

	identifier, registered := target.BackingIdentifier("celerity/queue", "orders", nil)
	s.Require().True(registered)
	entries.AddBackingIdentifier("orders", identifier)

	cache, _ := target.CacheConnection("sessions", nil)
	entries.AddCacheConnection("sessions", cache)

	database, _ := target.DatabaseConnection("ledger", nil)
	entries.AddDatabaseConnection("ledger", database)

	parent := entries.SharedParent(target, "orders-app", "/fake/orders-app/resources")
	s.Require().NotNil(parent)
	s.Equal("fake/config/store", parent.ResourceType,
		"the concrete store is the target's to choose")

	values := parent.SeedSpec.Fields["entries"].Fields
	for _, key := range []string{
		"orders", "orders_region",
		"sessions_host", "sessions_port", "sessions_tls", "sessions_clusterMode",
		"sessions_authMode", "sessions_authTokenSecretId",
		"ledger_host", "ledger_database", "ledger_port", "ledger_engine",
		"ledger_user", "ledger_ssl", "ledger_authMode",
	} {
		s.Contains(values, key, "key %q is part of the resources-store contract", key)
	}

	s.Equal("fake://orders", core.StringValue(values["orders"]))
	s.Equal("1234", core.StringValue(values["sessions_port"]),
		"numbers are written as strings, since the store holds strings only")
}

// Keys a target did not supply a value for are left out rather than written
// empty, since the SDKs read an absent key as "not applicable" and an empty one
// as a value.
func (s *ResourcesStoreTestSuite) Test_unsupplied_keys_are_absent_not_empty() {
	entries := NewResourcesStoreEntries()
	target := fakeStoreTarget{}

	cache, _ := target.CacheConnection("sessions", nil)
	entries.AddCacheConnection("sessions", cache)
	database, _ := target.DatabaseConnection("ledger", nil)
	entries.AddDatabaseConnection("ledger", database)

	values := entries.SharedParent(
		target, "orders-app", "/fake/orders-app/resources",
	).SeedSpec.Fields["entries"].Fields

	s.NotContains(values, "sessions_user",
		"a password-mode cache signs no token, so it has no user")
	s.NotContains(values, "ledger_credentialsSecretId",
		"an iam-mode database distributes no credential")
	s.NotContains(values, "ledger_readHost",
		"no reader endpoint was supplied")
}

// A type the target does not resolve through the store contributes nothing, so a
// target supporting fewer backings than another needs no special case.
func (s *ResourcesStoreTestSuite) Test_an_unregistered_backing_type_is_skipped() {
	target := fakeStoreTarget{}

	_, registered := target.BackingIdentifier("celerity/datastore", "events", nil)
	s.False(registered)
}

// An empty store is no store: nothing is emitted when no handler links to a
// backing resource.
func (s *ResourcesStoreTestSuite) Test_no_entries_means_no_store_resource() {
	entries := NewResourcesStoreEntries()
	s.True(entries.Empty())
	s.Nil(entries.SharedParent(fakeStoreTarget{}, "orders-app", "/path"))
}

// The first entry for a configKey wins, which is what makes a duplicate key
// deterministic rather than dependent on traversal order.
func (s *ResourcesStoreTestSuite) Test_the_first_entry_for_a_key_wins() {
	entries := NewResourcesStoreEntries()

	entries.AddBackingIdentifier("shared", BackingIdentifier{
		Value: core.MappingNodeFromString("first"),
	})
	entries.AddBackingIdentifier("shared", BackingIdentifier{
		Value: core.MappingNodeFromString("second"),
	})

	values := entries.SharedParent(
		fakeStoreTarget{}, "orders-app", "/path",
	).SeedSpec.Fields["entries"].Fields
	s.Equal("first", core.StringValue(values["shared"]))
}
