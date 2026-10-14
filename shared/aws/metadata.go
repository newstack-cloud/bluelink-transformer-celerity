package aws

import (
	"maps"
	"sort"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// SpecTagsFromResourceMetadata converts the labels from the resource metadata
// into a list of {key, value} tag objects for inclusion in an emitted AWS
// resource spec (the shape used by most AWS resources). Returns nil when there
// are no labels.
func SpecTagsFromResourceMetadata(metadata *schema.Metadata) *core.MappingNode {
	if metadata == nil ||
		metadata.Labels == nil ||
		len(metadata.Labels.Values) == 0 {
		return nil
	}

	keys := make([]string, 0, len(metadata.Labels.Values))
	for key := range metadata.Labels.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	tags := core.MappingNodeItems()
	for _, key := range keys {
		tags.Items = append(
			tags.Items,
			core.MappingNodeFields(
				"key", core.MappingNodeFromString(key),
				"value", core.MappingNodeFromString(metadata.Labels.Values[key]),
			),
		)
	}

	return tags
}

// MapTagsFromResourceMetadata converts the labels from the resource metadata
// into a map-shaped tags mapping node (key -> value) for AWS resources whose
// tags field is a string map rather than a list of {key, value} objects
// (for example aws/ssm/parameter). Returns nil when there are no labels.
func MapTagsFromResourceMetadata(metadata *schema.Metadata) *core.MappingNode {
	if metadata == nil ||
		metadata.Labels == nil ||
		len(metadata.Labels.Values) == 0 {
		return nil
	}

	tags := core.MappingNodeFields()
	for key, value := range metadata.Labels.Values {
		tags.Fields[key] = core.MappingNodeFromString(value)
	}

	return tags
}

// Identity is what Celerity stamps on every concrete resource it emits, so a
// resource can be attributed to the application and the abstract resource it came
// from without depending on the author having labelled anything.
//
// Author labels alone are not enough for this: they are optional, and a resource an
// author never labelled is then untraceable in a bill, an audit or a teardown. The
// keys mirror the bluelink.transform.source.* annotations the framework already
// stamps, in a tag-safe namespace.
type Identity struct {
	// AppName is the Celerity application the resource belongs to.
	AppName string
	// ResourceName is the abstract resource the concrete one was emitted from.
	ResourceName string
	// ResourceType is that abstract resource's type, e.g. "celerity/handler".
	ResourceType string
}

const (
	// TagKeyApp names the Celerity application.
	TagKeyApp = "celerity:app"
	// TagKeyResource names the abstract resource a concrete resource came from.
	TagKeyResource = "celerity:resource"
	// TagKeyResourceType names that abstract resource's type.
	TagKeyResourceType = "celerity:resource-type"
)

// Merges the author's labels with the identity tags.
//
// Identity wins on a collision. The keys are namespaced so a collision means the
// author wrote a celerity: key themselves, and a resource that lies about which
// application owns it is worse than one that drops a label.
func tagsWithIdentity(metadata *schema.Metadata, identity Identity) map[string]string {
	tags := map[string]string{}
	if metadata != nil && metadata.Labels != nil {
		maps.Copy(tags, metadata.Labels.Values)
	}

	for key, value := range map[string]string{
		TagKeyApp:          identity.AppName,
		TagKeyResource:     identity.ResourceName,
		TagKeyResourceType: identity.ResourceType,
	} {
		if value != "" {
			tags[key] = value
		}
	}

	return tags
}

// SpecTagsFor renders the author's labels plus Celerity's identity tags as the
// list-of-{key,value} shape most AWS resources take. Returns nil only when there is
// nothing at all to say, which in practice means a validation run with no app name.
func SpecTagsFor(metadata *schema.Metadata, identity Identity) *core.MappingNode {
	return specTagsFromMap(tagsWithIdentity(metadata, identity))
}

// MapTagsFor is SpecTagsFor for AWS resources whose tags field is a string map
// rather than a list of {key, value} objects.
func MapTagsFor(metadata *schema.Metadata, identity Identity) *core.MappingNode {
	tags := tagsWithIdentity(metadata, identity)
	if len(tags) == 0 {
		return nil
	}

	node := core.MappingNodeFields()
	for _, key := range sortedKeys(tags) {
		node.Fields[key] = core.MappingNodeFromString(tags[key])
	}

	return node
}

func specTagsFromMap(tags map[string]string) *core.MappingNode {
	if len(tags) == 0 {
		return nil
	}

	node := core.MappingNodeItems()
	for _, key := range sortedKeys(tags) {
		node.Items = append(node.Items, core.MappingNodeFields(
			"key", core.MappingNodeFromString(key),
			"value", core.MappingNodeFromString(tags[key]),
		))
	}

	return node
}

func sortedKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Tag shapes for the concrete AWS types Celerity emits. A type absent from both
// sets takes the list-of-{key,value} shape, which is what most AWS resources use.
var (
	// Types AWS gives no way to tag. Setting tags on these is not a no-op: Cloud
	// Control rejects the request, so they must be skipped rather than attempted.
	untaggableTypes = map[string]bool{
		"aws/lambda/layerVersion":     true,
		"aws/apigatewayv2/authorizer": true,
		"aws/sns/subscription":        true,
	}
	// Types whose tags field is a {key: value} map rather than a list.
	mapShapedTagTypes = map[string]bool{
		"aws/apigatewayv2/stage": true,
		"aws/ssm/parameterTree":  true,
		"aws/flex/vpc":           true,
	}
)

// ApplyTags stamps the author's labels and Celerity's identity onto every resource
// a family emitted, in whatever shape each concrete type takes.
//
// Applied to the whole emitted set rather than at each construction site, so a
// family that grows a resource later gets it tagged without anyone remembering to.
// A resource that already carries tags is left alone: the emitter said something
// more specific than this can.
func ApplyTags(
	resources map[string]*schema.Resource,
	metadata *schema.Metadata,
	identity Identity,
) {
	for _, resource := range resources {
		if resource == nil || resource.Type == nil || resource.Spec == nil {
			continue
		}
		if untaggableTypes[resource.Type.Value] {
			continue
		}
		if existing, has := resource.Spec.Fields["tags"]; has && existing != nil {
			continue
		}

		var tags *core.MappingNode
		if mapShapedTagTypes[resource.Type.Value] {
			tags = MapTagsFor(metadata, identity)
		} else {
			tags = SpecTagsFor(metadata, identity)
		}
		if tags != nil {
			resource.Spec.Fields["tags"] = tags
		}
	}
}
