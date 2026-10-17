package handlerawsserverless

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/newstack-cloud/bluelink-transformer-celerity/resources/handler"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awsexternal"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/awslambda"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

// accountPlaceholder stands in for the account being deployed into, which is not
// known while the blueprint is transformed.
//
// Resolving it would mean an STS GetCallerIdentity call in the transform step
// for one string in one diagnostic, and the transform would then fail or stall
// on a credentials problem that has nothing to do with the blueprint. The author
// knows which account they are deploying into; the statement is still
// copy-and-editable with one substitution.
const accountPlaceholder = "<account-being-deployed-into>"

// externalResourcePolicyDiagnostics explains the half of a cross-account grant
// Celerity cannot make.
//
// The grant seeded onto the execution role is necessary and not sufficient:
// reaching a resource another account owns needs that account's permission too,
// expressed as a statement on the resource itself. Without being told, an author
// sees a correct-looking deploy and an AccessDenied at the first call, and the
// policy they are missing is in a different account from anything they are
// looking at.
func externalResourcePolicyDiagnostics(
	r *handler.ResolvedHandler,
	appName string,
) []*core.Diagnostic {
	grants := externalRoleTargets(r)
	if len(grants) == 0 {
		return nil
	}

	plan := RolePlanFor(r)
	roleARN := fmt.Sprintf(
		"arn:aws:iam::%s:role/%s",
		accountPlaceholder,
		physicalRoleName(appName, plan.Fingerprint()),
	)

	diagnostics := []*core.Diagnostic{}
	for _, grant := range grants {
		statement, err := resourcePolicyStatement(roleARN, grant)
		if err != nil {
			continue
		}

		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelInfo,
			Message: fmt.Sprintf(
				"celerity/handler %q is granted access to %s, which Celerity does not own.%s "+
					"Where it is owned by another account, that account must also allow this "+
					"principal, by adding this statement to the resource's own policy:\n\n%s\n\n"+
					"If the resource is encrypted with a customer-managed key, the key's policy "+
					"has to allow the same principal and the role needs kms:Decrypt and "+
					"kms:GenerateDataKey on the key. Celerity cannot read either policy from "+
					"here, so neither is checked.",
				r.Name, grant.ARN, ownerClause(r, grant), statement,
			),
			Range: shared.DiagnosticRange(shared.ResourceMeta(r.Resource)),
		})
	}

	return diagnostics
}

// Names the owning account when it is known: an identifier that carries one says
// so itself, and an author declares it where the identifier does not.
func ownerClause(r *handler.ResolvedHandler, grant awslambda.ExternalGrant) string {
	if account := grantOwnerAccount(r, grant); account != "" {
		return fmt.Sprintf(" It is owned by account %s.", account)
	}

	return ""
}

func grantOwnerAccount(r *handler.ResolvedHandler, grant awslambda.ExternalGrant) string {
	if identifier := awsexternal.ParseOrNil(&shared.External{ID: grant.ARN}); identifier != nil {
		if identifier.ARN.AccountID != "" {
			return identifier.ARN.AccountID
		}
	}

	// An S3 ARN names no account, so the author declares it if they want the
	// diagnostic to name the owner.
	for _, linked := range r.Buckets {
		external, _ := shared.ResolvedExternal("celerity/bucket", linked.Name, linked.Resource)
		if external != nil && external.ID == grant.ARN {
			return external.Account
		}
	}

	return ""
}

// The statement the owning account adds, rendered as JSON so it can be pasted
// into a bucket, queue, topic or key policy with only the account substituted.
func resourcePolicyStatement(roleARN string, grant awslambda.ExternalGrant) (string, error) {
	resources := awslambda.ExternalTargetResourceARNs(grant)
	statement := map[string]any{
		"Effect":    "Allow",
		"Principal": map[string]any{"AWS": roleARN},
		"Action":    awslambda.ExternalTargetActions(grant.Service, grant.Access),
		"Resource":  resources,
	}
	if len(resources) == 1 {
		statement["Resource"] = resources[0]
	}

	// Encoded rather than marshalled so the account placeholder's angle brackets
	// survive. json.Marshal escapes them to \u003c and \u003e, which the author
	// would have to undo before the statement could be used.
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(statement); err != nil {
		return "", err
	}

	return strings.TrimRight(encoded.String(), "\n"), nil
}
