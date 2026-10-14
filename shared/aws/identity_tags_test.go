package aws

import (
	"testing"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

func labels(values map[string]string) *schema.Metadata {
	return &schema.Metadata{Labels: &schema.StringMap{Values: values}}
}

func specTagMap(node *core.MappingNode) map[string]string {
	out := map[string]string{}
	if node == nil {
		return out
	}
	for _, item := range node.Items {
		out[core.StringValue(item.Fields["key"])] = core.StringValue(item.Fields["value"])
	}
	return out
}

var testIdentity = Identity{
	AppName:      "celerity-node-test-app",
	ResourceName: "auditDatabase",
	ResourceType: "celerity/sqlDatabase",
}

// A resource the author never labelled must still be attributable to its app and
// abstract resource, which is the whole reason identity is not derived from labels.
func Test_identity_is_stamped_without_any_labels(t *testing.T) {
	got := specTagMap(SpecTagsFor(nil, testIdentity))
	for key, want := range map[string]string{
		TagKeyApp:          "celerity-node-test-app",
		TagKeyResource:     "auditDatabase",
		TagKeyResourceType: "celerity/sqlDatabase",
	} {
		if got[key] != want {
			t.Errorf("%s: got %q want %q", key, got[key], want)
		}
	}
}

func Test_author_labels_are_carried_alongside_identity(t *testing.T) {
	got := specTagMap(SpecTagsFor(labels(map[string]string{"network": "app-network"}), testIdentity))
	if got["network"] != "app-network" {
		t.Errorf("author label lost: %v", got)
	}
	if got[TagKeyApp] != "celerity-node-test-app" {
		t.Errorf("identity lost: %v", got)
	}
}

// A resource that lies about which application owns it is worse than one that
// drops a label, so identity wins a collision.
func Test_identity_wins_a_collision_with_an_author_label(t *testing.T) {
	got := specTagMap(SpecTagsFor(labels(map[string]string{TagKeyApp: "not-this-app"}), testIdentity))
	if got[TagKeyApp] != "celerity-node-test-app" {
		t.Errorf("author label overrode identity: %v", got)
	}
}

// Validation runs have no app name; an empty component must not become an empty tag.
func Test_empty_identity_components_are_omitted(t *testing.T) {
	got := specTagMap(SpecTagsFor(nil, Identity{ResourceName: "appCache"}))
	if _, present := got[TagKeyApp]; present {
		t.Errorf("empty app name should not be tagged: %v", got)
	}
	if got[TagKeyResource] != "appCache" {
		t.Errorf("resource name should still be tagged: %v", got)
	}
}

func Test_map_shape_carries_the_same_tags(t *testing.T) {
	node := MapTagsFor(labels(map[string]string{"network": "app-network"}), testIdentity)
	if core.StringValue(node.Fields[TagKeyApp]) != "celerity-node-test-app" {
		t.Errorf("identity missing from map shape: %v", node.Fields)
	}
	if core.StringValue(node.Fields["network"]) != "app-network" {
		t.Errorf("author label missing from map shape: %v", node.Fields)
	}
}

func Test_nothing_at_all_yields_no_tags(t *testing.T) {
	if SpecTagsFor(nil, Identity{}) != nil {
		t.Error("expected nil when there is nothing to say")
	}
	if MapTagsFor(nil, Identity{}) != nil {
		t.Error("expected nil when there is nothing to say")
	}
}
