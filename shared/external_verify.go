package shared

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
	"github.com/newstack-cloud/bluelink/libs/blueprint/transform"
)

// ExternalVerifier looks an external identifier up so a wrong or stale one is
// reported while the author is still at their terminal.
//
// Without this an external resource's identifier is first exercised inside a
// running handler, which fails as a permission or not-found error from the
// service with nothing pointing at the blueprint that named it. A resource
// recreated under a new name six months ago fails on the next request rather
// than on the next deploy.
type ExternalVerifier interface {
	Verify(
		ctx context.Context,
		transformCtx transform.Context,
		celerityType string,
		external *External,
	) (ExternalVerification, error)
}

// ExternalVerification is the outcome of looking one identifier up.
type ExternalVerification struct {
	// Found reports that the resource was located.
	Found bool
	// Invisible reports that the lookup was refused rather than answered. This is
	// the expected outcome for a resource in another account, where the deploy
	// principal has no visibility, and is a different condition from the resource
	// not existing.
	Invisible bool
	// Detail carries the service's own message, so a diagnostic can say what the
	// lookup actually returned.
	Detail string
}

// ExternalVerificationDiagnostics turns a verification outcome into what the
// author should be told.
//
// A refused lookup is a warning rather than an error: where the identifier names
// another account, the deploy principal legitimately cannot see into it, so
// existence is unverifiable rather than disproved. Treating it as an error would
// make every cross-account deployment fail, which is the case external mode
// exists for.
func ExternalVerificationDiagnostics(
	resourceType string,
	name string,
	resource *schema.Resource,
	external *External,
	verification ExternalVerification,
) []*core.Diagnostic {
	if verification.Found {
		return nil
	}

	if verification.Invisible {
		return []*core.Diagnostic{{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"%s %q names %q, which this deploy principal cannot see. That is expected "+
					"for a resource in another account, so its existence has not been "+
					"verified: a wrong or stale identifier will surface at runtime instead. %s",
				resourceType, name, external.ID, verification.Detail,
			),
			Range: core.DiagnosticRangeFromSourceMeta(resource.SourceMeta, nil),
		}}
	}

	return []*core.Diagnostic{{
		Level: core.DiagnosticLevelError,
		Message: fmt.Sprintf(
			"%s %q names %q, which does not exist. %s",
			resourceType, name, external.ID, verification.Detail,
		),
		Range: core.DiagnosticRangeFromSourceMeta(resource.SourceMeta, nil),
	}}
}

// VerifyExternal runs the verifier and renders its outcome, returning no
// diagnostics when no verifier is configured.
//
// A verifier is absent in a validation context and in tests, where the transform
// has no credentials to look anything up with. A lookup that fails for any other
// reason, such as a throttled or unreachable API, is reported as a warning rather
// than failing the transform: the deploy would otherwise be blocked by a
// transient condition unrelated to the blueprint.
func VerifyExternal(
	ctx context.Context,
	run *RunContext,
	resourceType string,
	name string,
	resource *schema.Resource,
	external *External,
) []*core.Diagnostic {
	if run == nil || run.Verifier == nil || external == nil {
		return nil
	}

	verification, err := run.Verifier.Verify(
		ctx, run.TransformContext, resourceType, external,
	)
	if err != nil {
		return []*core.Diagnostic{{
			Level: core.DiagnosticLevelWarning,
			Message: fmt.Sprintf(
				"%s %q names %q, which could not be checked: %s. The identifier has not "+
					"been verified.",
				resourceType, name, external.ID, err.Error(),
			),
			Range: core.DiagnosticRangeFromSourceMeta(resource.SourceMeta, nil),
		}}
	}

	return ExternalVerificationDiagnostics(
		resourceType, name, resource, external, verification,
	)
}

// RunContext is the slice of a transform run external verification needs: the
// transformer context to build a client from, and the verifier itself.
type RunContext struct {
	TransformContext transform.Context
	Verifier         ExternalVerifier
}

// ExternalKeyModel is an external data store's key model as the service reports it.
type ExternalKeyModel struct {
	PartitionKey string
	SortKey      string
	// Found and Invisible carry the same meanings as on ExternalVerification.
	Found     bool
	Invisible bool
	Detail    string
}

// ExternalKeyModelVerifier reads an external data store's key model. Implemented
// alongside ExternalVerifier, and used instead of it for a data store: reading
// the key model also proves the table exists, so one lookup answers both.
type ExternalKeyModelVerifier interface {
	ExternalKeyModel(
		ctx context.Context,
		transformCtx transform.Context,
		external *External,
	) (ExternalKeyModel, error)
}

// VerifyExternalKeyModel checks an external data store exists and that the key
// model the blueprint declares is the one the store actually has.
//
// A mismatch is an error because the SDK's condition builder is written against
// the declared keys, so it would otherwise surface as a validation failure from
// the data store at runtime, with nothing pointing at the cause.
func VerifyExternalKeyModel(
	ctx context.Context,
	run *RunContext,
	resourceType string,
	name string,
	resource *schema.Resource,
	external *External,
	declaredPartitionKey string,
	declaredSortKey string,
) []*core.Diagnostic {
	if run == nil || external == nil {
		return nil
	}

	verifier, supported := run.Verifier.(ExternalKeyModelVerifier)
	if !supported {
		return VerifyExternal(ctx, run, resourceType, name, resource, external)
	}

	model, err := verifier.ExternalKeyModel(ctx, run.TransformContext, external)
	if err != nil {
		return []*core.Diagnostic{externalWarning(resource, fmt.Sprintf(
			"%s %q names %q, whose key model could not be read: %s. Neither the data "+
				"store's existence nor its keys have been verified.",
			resourceType, name, external.ID, err.Error(),
		))}
	}

	if !model.Found {
		return ExternalVerificationDiagnostics(
			resourceType, name, resource, external,
			ExternalVerification{Invisible: model.Invisible, Detail: model.Detail},
		)
	}

	return keyModelDiagnostics(
		resourceType, name, resource, external, model, declaredPartitionKey, declaredSortKey,
	)
}

func keyModelDiagnostics(
	resourceType string,
	name string,
	resource *schema.Resource,
	external *External,
	model ExternalKeyModel,
	declaredPartitionKey string,
	declaredSortKey string,
) []*core.Diagnostic {
	diagnostics := []*core.Diagnostic{}

	if declaredPartitionKey != model.PartitionKey {
		diagnostics = append(diagnostics, ExternalDiagnostic(resource, fmt.Sprintf(
			"%s %q declares partition key %q but %q uses %q.",
			resourceType, name, declaredPartitionKey, external.ID, model.PartitionKey,
		)))
	}

	if declaredSortKey != model.SortKey {
		diagnostics = append(diagnostics, ExternalDiagnostic(resource, fmt.Sprintf(
			"%s %q declares sort key %q but %q uses %q.",
			resourceType, name, describeSortKey(declaredSortKey), external.ID,
			describeSortKey(model.SortKey),
		)))
	}

	return diagnostics
}

func describeSortKey(key string) string {
	if key == "" {
		return "none"
	}

	return key
}

func externalWarning(resource *schema.Resource, message string) *core.Diagnostic {
	return &core.Diagnostic{
		Level:   core.DiagnosticLevelWarning,
		Message: message,
		Range:   core.DiagnosticRangeFromSourceMeta(resource.SourceMeta, nil),
	}
}
