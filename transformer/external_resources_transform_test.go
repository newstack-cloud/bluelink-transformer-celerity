//go:build unit

package transformer

import (
	"context"
	"errors"
	"testing"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
	"github.com/stretchr/testify/suite"
)

type ExternalResourcesTransformTestSuite struct {
	suite.Suite
}

const (
	externalTopicARN     = "arn:aws:sns:eu-west-2:210987654321:acme-audit-events"
	externalDatastoreARN = "arn:aws:dynamodb:eu-west-2:210987654321:table/acme-ledger"
)

func externalResource(resourceType, identifier string, extra ...*core.MappingNode) *schema.Resource {
	spec := core.MappingNodeFields(
		"mode", core.MappingNodeFromString(shared.ModeExternal),
		"externalId", core.MappingNodeFromString(identifier),
	)
	for index := 0; index+1 < len(extra); index += 2 {
		spec.Fields[core.StringValue(extra[index])] = extra[index+1]
	}

	return &schema.Resource{
		Type:     &schema.ResourceTypeWrapper{Value: resourceType},
		Spec:     spec,
		Metadata: &schema.Metadata{Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}}},
	}
}

func (s *ExternalResourcesTransformTestSuite) transform(
	resources map[string]*schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
) *transform.SpecTransformerTransformOutput {
	return s.transformWithVerifier(resources, linkGraph, nil)
}

func (s *ExternalResourcesTransformTestSuite) transformWithVerifier(
	resources map[string]*schema.Resource,
	linkGraph linktypes.DeclaredLinkGraph,
	verifier shared.ExternalVerifier,
) *transform.SpecTransformerTransformOutput {
	out, err := NewTransformer(&shared.Dependencies{ExternalVerifier: verifier}).Transform(
		context.Background(),
		&transform.SpecTransformerTransformInput{
			InputBlueprint:     &schema.Blueprint{Resources: &schema.ResourceMap{Values: resources}},
			LinkGraph:          linkGraph,
			TransformerContext: validationContext(),
		},
	)
	s.Require().NoError(err)

	return out
}

func (s *ExternalResourcesTransformTestSuite) errorMessages(
	out *transform.SpecTransformerTransformOutput,
) []string {
	messages := []string{}
	for _, diagnostic := range out.Diagnostics {
		if diagnostic.Level == core.DiagnosticLevelError {
			messages = append(messages, diagnostic.Message)
		}
	}

	return messages
}

// The point of external mode: a handler reaches a topic Celerity does not own,
// and no concrete topic is emitted for it.
func (s *ExternalResourcesTransformTestSuite) Test_external_topic_emits_nothing_and_stores_its_arn() {
	out := s.transform(
		map[string]*schema.Resource{
			"publishAudit": handlerLinkedTo(map[string]string{"app": "orders"}),
			"auditTopic":   externalResource("celerity/topic", externalTopicARN),
		},
		edges(edge("publishAudit", "auditTopic", "celerity/handler", "celerity/topic")),
	)
	resources := out.TransformedBlueprint.Resources.Values

	s.Empty(s.errorMessages(out))
	s.NotContains(resources, "auditTopic_sns_topic", "an external topic emits no concrete resource")

	store := resources["celerityResourcesConfigStore"]
	s.Require().NotNil(store, "the handler still reaches the topic through the store")
	entry := store.Spec.Fields["values"].Fields["auditTopic"]
	s.Require().NotNil(entry, "expected a store entry for the external topic")
	s.Equal(
		externalTopicARN,
		core.StringValue(entry),
		"the store carries the authored ARN, not a reference to a resource that was never emitted",
	)
}

func (s *ExternalResourcesTransformTestSuite) Test_external_datastore_emits_nothing_and_stores_its_arn() {
	out := s.transform(
		map[string]*schema.Resource{
			"readLedger": handlerLinkedTo(map[string]string{"app": "orders"}),
			"ledger": externalResource(
				"celerity/datastore",
				externalDatastoreARN,
				core.MappingNodeFromString("keys"),
				core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
			),
		},
		edges(edge("readLedger", "ledger", "celerity/handler", "celerity/datastore")),
	)
	resources := out.TransformedBlueprint.Resources.Values

	s.Empty(s.errorMessages(out))
	s.NotContains(resources, "ledger_dynamodb_table")

	entry := resources["celerityResourcesConfigStore"].Spec.Fields["values"].Fields["ledger"]
	s.Require().NotNil(entry)
	s.Equal(externalDatastoreARN, core.StringValue(entry))
}

// A managed resource must be unaffected: its store entry stays a reference to the
// concrete resource rather than a literal.
func (s *ExternalResourcesTransformTestSuite) Test_managed_topic_still_stores_a_reference() {
	out := s.transform(
		map[string]*schema.Resource{
			"publishAudit": handlerLinkedTo(map[string]string{"app": "orders"}),
			"auditTopic": {
				Type: &schema.ResourceTypeWrapper{Value: "celerity/topic"},
				Spec: core.MappingNodeFields("name", core.MappingNodeFromString("audit")),
				Metadata: &schema.Metadata{
					Labels: &schema.StringMap{Values: map[string]string{"app": "orders"}},
				},
			},
		},
		edges(edge("publishAudit", "auditTopic", "celerity/handler", "celerity/topic")),
	)
	resources := out.TransformedBlueprint.Resources.Values

	s.Require().NotNil(resources["auditTopic_sns_topic"], "a managed topic still emits")
	entry := resources["celerityResourcesConfigStore"].Spec.Fields["values"].Fields["audit"]
	s.Require().NotNil(entry)
	s.Equal("auditTopic_sns_topic", resourceRefName(entry))
}

// External mode without an identifier is an error rather than an empty store
// entry, which would surface as a runtime failure inside a handler.
func (s *ExternalResourcesTransformTestSuite) Test_external_mode_without_an_arn_is_reported() {
	out := s.transform(
		map[string]*schema.Resource{
			"auditTopic": {
				Type: &schema.ResourceTypeWrapper{Value: "celerity/topic"},
				Spec: core.MappingNodeFields(
					"mode", core.MappingNodeFromString(shared.ModeExternal),
				),
			},
		},
		emptyLinkGraph{},
	)

	s.Require().Len(s.errorMessages(out), 1)
	s.Contains(s.errorMessages(out)[0], "external mode but declares no \"externalId\"")
}

func (s *ExternalResourcesTransformTestSuite) Test_external_mode_with_a_malformed_arn_is_reported() {
	out := s.transform(
		map[string]*schema.Resource{
			"auditTopic": externalResource("celerity/topic", "acme-audit-events"),
		},
		emptyLinkGraph{},
	)

	s.Require().Len(s.errorMessages(out), 1)
	s.Contains(s.errorMessages(out)[0], "not a valid ARN")
}

const (
	externalQueueARN  = "arn:aws:sqs:eu-west-2:210987654321:acme-intake"
	externalBucketARN = "arn:aws:s3:::acme-central-audit-archive"
)

// SQS addresses a queue by URL, so the store carries a URL derived from the ARN
// rather than the ARN itself.
func (s *ExternalResourcesTransformTestSuite) Test_external_queue_stores_a_derived_queue_url() {
	out := s.transform(
		map[string]*schema.Resource{
			"sendIntake": handlerLinkedTo(map[string]string{"app": "orders"}),
			"intake":     externalResource("celerity/queue", externalQueueARN),
		},
		edges(edge("sendIntake", "intake", "celerity/handler", "celerity/queue")),
	)
	resources := out.TransformedBlueprint.Resources.Values

	s.Empty(s.errorMessages(out))
	s.NotContains(resources, "intake_sqs_queue")

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"].Fields
	s.Equal(
		"https://sqs.eu-west-2.amazonaws.com/210987654321/acme-intake",
		core.StringValue(values["intake"]),
	)
	s.Equal("eu-west-2", core.StringValue(values["intake_region"]))
}

// S3 addresses a bucket by name, and an S3 ARN carries neither region nor
// account, so the region is authored and the name comes from the ARN.
func (s *ExternalResourcesTransformTestSuite) Test_external_bucket_stores_its_name_and_authored_region() {
	out := s.transform(
		map[string]*schema.Resource{
			"archive": handlerLinkedTo(map[string]string{"app": "orders"}),
			"auditArchive": externalResource(
				"celerity/bucket",
				externalBucketARN,
				core.MappingNodeFromString("region"),
				core.MappingNodeFromString("eu-west-2"),
			),
		},
		edges(edge("archive", "auditArchive", "celerity/handler", "celerity/bucket")),
	)
	resources := out.TransformedBlueprint.Resources.Values

	s.Empty(s.errorMessages(out))
	s.NotContains(resources, "auditArchive_s3_bucket")

	values := resources["celerityResourcesConfigStore"].Spec.Fields["values"].Fields
	s.Equal(
		"acme-central-audit-archive",
		core.StringValue(values["auditArchive"]),
		"S3 data-plane calls address a bucket by name, not by ARN",
	)
	s.Equal("eu-west-2", core.StringValue(values["auditArchive_region"]))
}

// Without a region an external bucket would be addressed in the application's own
// region, and S3 answers a misrouted request with a redirect rather than serving
// it, so the omission is reported instead.
func (s *ExternalResourcesTransformTestSuite) Test_external_bucket_without_a_region_is_reported() {
	out := s.transform(
		map[string]*schema.Resource{
			"auditArchive": externalResource("celerity/bucket", externalBucketARN),
		},
		emptyLinkGraph{},
	)

	s.Require().Len(s.errorMessages(out), 1)
	s.Contains(s.errorMessages(out)[0], "declares no \"region\"")
}

// A cross-partition queue URL uses a different host, so the derivation reads the
// partition from the ARN rather than assuming the commercial one.
func (s *ExternalResourcesTransformTestSuite) Test_external_queue_url_follows_the_arn_partition() {
	out := s.transform(
		map[string]*schema.Resource{
			"sendIntake": handlerLinkedTo(map[string]string{"app": "orders"}),
			"intake": externalResource(
				"celerity/queue",
				"arn:aws-cn:sqs:cn-north-1:210987654321:acme-intake",
			),
		},
		edges(edge("sendIntake", "intake", "celerity/handler", "celerity/queue")),
	)

	values := out.TransformedBlueprint.Resources.Values["celerityResourcesConfigStore"].
		Spec.Fields["values"].Fields
	s.Equal(
		"https://sqs.cn-north-1.amazonaws.com.cn/210987654321/acme-intake",
		core.StringValue(values["intake"]),
	)
}

// Stands in for a lookup against AWS. The transform makes the call per external
// resource, so the fake records what it was asked about.
type fakeVerifier struct {
	verification shared.ExternalVerification
	keyModel     shared.ExternalKeyModel
	err          error
	asked        []string
}

func (f *fakeVerifier) Verify(
	_ context.Context,
	_ transform.Context,
	celerityType string,
	external *shared.External,
) (shared.ExternalVerification, error) {
	f.asked = append(f.asked, celerityType+" "+external.ID)
	return f.verification, f.err
}

type fakeKeyModelVerifier struct {
	*fakeVerifier
}

func (f *fakeKeyModelVerifier) ExternalKeyModel(
	_ context.Context,
	_ transform.Context,
	external *shared.External,
) (shared.ExternalKeyModel, error) {
	f.asked = append(f.asked, "keyModel "+external.ID)
	return f.keyModel, f.err
}

func (s *ExternalResourcesTransformTestSuite) Test_verified_identifier_produces_no_diagnostics() {
	verifier := &fakeVerifier{verification: shared.ExternalVerification{Found: true}}
	out := s.transformWithVerifier(
		map[string]*schema.Resource{
			"auditTopic": externalResource("celerity/topic", externalTopicARN),
		},
		emptyLinkGraph{},
		verifier,
	)

	s.Empty(out.Diagnostics)
	s.Equal([]string{"celerity/topic " + externalTopicARN}, verifier.asked)
}

// The late-failure problem this exists for: a stale identifier is reported while
// the author is still at their terminal rather than inside a running handler.
func (s *ExternalResourcesTransformTestSuite) Test_identifier_that_does_not_exist_is_an_error() {
	out := s.transformWithVerifier(
		map[string]*schema.Resource{
			"auditTopic": externalResource("celerity/topic", externalTopicARN),
		},
		emptyLinkGraph{},
		&fakeVerifier{verification: shared.ExternalVerification{
			Detail: "The service reported NotFound.",
		}},
	)

	s.Require().Len(s.errorMessages(out), 1)
	s.Contains(s.errorMessages(out)[0], "which does not exist")
}

// A refused lookup is the expected answer for a resource in another account, so
// it must not fail the deploy that external mode exists to enable.
func (s *ExternalResourcesTransformTestSuite) Test_invisible_identifier_warns_rather_than_fails() {
	out := s.transformWithVerifier(
		map[string]*schema.Resource{
			"auditTopic": externalResource("celerity/topic", externalTopicARN),
		},
		emptyLinkGraph{},
		&fakeVerifier{verification: shared.ExternalVerification{
			Invisible: true,
			Detail:    "The lookup was refused with AuthorizationError.",
		}},
	)

	s.Empty(s.errorMessages(out), "a cross-account resource must still deploy")
	s.Require().Len(out.Diagnostics, 1)
	s.Equal(core.DiagnosticLevelWarning, out.Diagnostics[0].Level)
	s.Contains(out.Diagnostics[0].Message, "cannot see")
}

// A throttled or unreachable API is unrelated to the blueprint, so it leaves the
// identifier unverified rather than blocking the deploy.
func (s *ExternalResourcesTransformTestSuite) Test_lookup_failure_warns_rather_than_fails() {
	out := s.transformWithVerifier(
		map[string]*schema.Resource{
			"auditTopic": externalResource("celerity/topic", externalTopicARN),
		},
		emptyLinkGraph{},
		&fakeVerifier{err: errors.New("request throttled")},
	)

	s.Empty(s.errorMessages(out))
	s.Require().Len(out.Diagnostics, 1)
	s.Contains(out.Diagnostics[0].Message, "could not be checked")
}

// An external data store's declared keys drive the SDK's condition builder, so a
// table whose real key model differs is reported here rather than as a runtime
// ValidationException.
func (s *ExternalResourcesTransformTestSuite) Test_key_model_mismatch_is_reported() {
	out := s.transformWithVerifier(
		map[string]*schema.Resource{
			"ledger": externalResource(
				"celerity/datastore",
				externalDatastoreARN,
				core.MappingNodeFromString("keys"),
				core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
			),
		},
		emptyLinkGraph{},
		&fakeKeyModelVerifier{fakeVerifier: &fakeVerifier{
			keyModel: shared.ExternalKeyModel{
				Found: true,

				PartitionKey: "ledgerId",
				SortKey:      "recordedAt",
			},
		}},
	)

	messages := s.errorMessages(out)
	s.Require().Len(messages, 2)
	s.Contains(messages[0], "declares partition key \"id\" but")
	s.Contains(messages[0], "uses \"ledgerId\"")
	s.Contains(messages[1], "declares sort key \"none\" but")
	s.Contains(messages[1], "uses \"recordedAt\"")
}

func (s *ExternalResourcesTransformTestSuite) Test_matching_key_model_produces_no_diagnostics() {
	out := s.transformWithVerifier(
		map[string]*schema.Resource{
			"ledger": externalResource(
				"celerity/datastore",
				externalDatastoreARN,
				core.MappingNodeFromString("keys"),
				core.MappingNodeFields("partitionKey", core.MappingNodeFromString("id")),
			),
		},
		emptyLinkGraph{},
		&fakeKeyModelVerifier{fakeVerifier: &fakeVerifier{
			keyModel: shared.ExternalKeyModel{Found: true, PartitionKey: "id"},
		}},
	)

	s.Empty(out.Diagnostics)
}

// Nothing is looked up for a managed resource: there is no external identifier.
func (s *ExternalResourcesTransformTestSuite) Test_managed_resources_are_not_looked_up() {
	verifier := &fakeVerifier{verification: shared.ExternalVerification{Found: true}}
	s.transformWithVerifier(
		map[string]*schema.Resource{
			"auditTopic": {
				Type: &schema.ResourceTypeWrapper{Value: "celerity/topic"},
				Spec: core.MappingNodeFields("name", core.MappingNodeFromString("audit")),
			},
		},
		emptyLinkGraph{},
		verifier,
	)

	s.Empty(verifier.asked)
}

const (
	externalParameterARN = "arn:aws:ssm:eu-west-2:210987654321:parameter/acme/shared/config"
	externalSecretARN    = "arn:aws:secretsmanager:eu-west-2:210987654321:secret:acme/shared-AbCdEf"
)

// An external config store emits no concrete store, but spec.id still has to
// resolve: the handler's CELERITY_CONFIG_<NS>_STORE_ID carries that reference.
func (s *ExternalResourcesTransformTestSuite) Test_external_parameter_store_config_resolves_to_its_path() {
	out := s.transform(
		map[string]*schema.Resource{
			"readConfig":   handlerLinkedTo(map[string]string{"app": "orders"}),
			"sharedConfig": externalResource("celerity/config", externalParameterARN),
		},
		edges(edge("readConfig", "sharedConfig", "celerity/handler", "celerity/config")),
	)

	s.Empty(s.errorMessages(out))
	resources := out.TransformedBlueprint.Resources.Values
	s.NotContains(resources, "sharedConfig_config_param_tree")
	s.NotContains(resources, "sharedConfig_config_secret")

	values := out.TransformedBlueprint.Values.Values
	idValue := values["sharedConfig_config_store_id"]
	s.Require().NotNil(idValue, "spec.id must still resolve for an external store")
	s.Equal(
		"/acme/shared/config",
		core.StringValue(idValue.Value),
		"Parameter Store is addressed by path, so the ARN's parameter segment is used",
	)
}

// A secret is addressed by ARN rather than by path, and the ARN's service is
// what selects the backend when there are no values to infer it from.
func (s *ExternalResourcesTransformTestSuite) Test_external_secrets_manager_config_resolves_to_its_arn() {
	out := s.transform(
		map[string]*schema.Resource{
			"readConfig":   handlerLinkedTo(map[string]string{"app": "orders"}),
			"sharedConfig": externalResource("celerity/config", externalSecretARN),
		},
		edges(edge("readConfig", "sharedConfig", "celerity/handler", "celerity/config")),
	)

	s.Empty(s.errorMessages(out))
	idValue := out.TransformedBlueprint.Values.Values["sharedConfig_config_store_id"]
	s.Require().NotNil(idValue)
	s.Equal(externalSecretARN, core.StringValue(idValue.Value))
}

// An external store's contents are not Celerity's to write, so declaring values
// alongside it is a contradiction rather than something to ignore.
func (s *ExternalResourcesTransformTestSuite) Test_external_config_with_values_is_reported() {
	out := s.transform(
		map[string]*schema.Resource{
			"sharedConfig": externalResource(
				"celerity/config",
				externalParameterARN,
				core.MappingNodeFromString("values"),
				core.MappingNodeFields("apiKey", core.MappingNodeFromString("secret")),
			),
		},
		emptyLinkGraph{},
	)

	s.Require().Len(s.errorMessages(out), 1)
	s.Contains(s.errorMessages(out)[0], "external mode and declares \"values\"")
}

func TestExternalResourcesTransformTestSuite(t *testing.T) {
	suite.Run(t, new(ExternalResourcesTransformTestSuite))
}
