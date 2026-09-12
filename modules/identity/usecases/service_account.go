package usecases

import (
	"context"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// Machine identity in the trail (ADR-0008). A service account reaches exactly
// the data a member reaches, and its credentials are the standing route into a
// tenant, so provisioning and credential lifecycle are recorded with the same
// whitelist treatment as `member.*`:
//
//   - service_account.created — the grant (role + scope entity id), the kind
//     and the status of the principal. Not the name: it is `max=200` free text
//     (ADR-0007/0008 keep free text out of the envelope).
//   - api_token.issued — which account the credential speaks for, the token by
//     id (it is the entry's resource) and its scheme prefix, and the window it
//     is valid for.
//   - api_token.revoked — the transition that ends the credential's life.
//
// Never the secret and never its hash: the cleartext exists for one response
// and the hash is the verifier, and the trail is append-only — a credential put
// there could never be withdrawn from it.
//
// Every one of the three runs on the request transaction (all three routes
// declare Tenant, which implies Tx), so the provisioning and its evidence
// commit or roll back together; and the actor is always a HUMAN tenant admin,
// because member:manage is human-only (authz.HumanOnly) — which is what makes
// the recorder's "acting user" requirement safe at these call sites.

// auditServiceAccountGrant projects the grant a service account is created
// with onto the member envelope's shape (role + scope entity id, never the
// scope entity's name), so a machine grant and a human grant compare directly
// in the trail — the asymmetry that let a machine be given a scoped reviewer
// role with no record while the same grant to a person wrote one.
func auditServiceAccountGrant(role string, scopeEntityID *string) []map[string]any {
	return auditMemberGrants([]domain.MemberGrant{{Role: role, ScopeEntityID: scopeEntityID}})
}

// auditTokenDetails attaches the credential scheme to a token entry's change
// set. The prefix recorded is the SCHEME marker ("ztx_"), which is what tells
// an API credential apart from an invite token ("zti_") in the trail — never a
// fragment of the secret itself, which would leave part of a live credential
// in a ledger nothing can redact.
func auditTokenDetails(changes map[string]any) map[string]any {
	if changes == nil {
		changes = map[string]any{}
	}
	changes["tokenPrefix"] = domain.TokenPrefix
	return changes
}

// CreateServiceAccount provisions a machine principal (agentic-AI readiness
// B1): a users row with kind=service plus one RBAC grant. Service accounts
// authenticate only via API tokens — they get a synthetic internal email and
// no password, and the login path rejects them outright. The grant insert runs
// on the request transaction, so RLS pins it to the caller's tenant.
//
// Any matrix role may be granted (a machine legitimately needs manager-level
// setup writes), but the approval verb is still blocked at authorization time:
// service principals are denied task:approve regardless of role.
func (uc *UseCases) CreateServiceAccount(ctx context.Context, input domain.CreateServiceAccountInput) (*domain.User, error) {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	if !authz.KnownRole(authz.Role(input.Role)) {
		return nil, apperrors.NewValidation("unknown role: " + input.Role)
	}
	// No self-replication: a machine never holds member:manage.
	if authz.Role(input.Role) == authz.RoleTenantAdmin {
		return nil, apperrors.NewValidation(domain.MsgServiceCannotBeAdmin)
	}

	user, err := uc.users.Create(ctx, domain.CreateUserInput{
		TenantID: tenantID,
		Email:    "svc-" + uuid.NewString() + "@service.zentax.internal",
		Name:     input.Name,
		Kind:     domain.KindService,
		Status:   "active",
	})
	if err != nil {
		return nil, err
	}

	// Cross-tenant scope ids fail the composite FK on user_grants (→ 400).
	if err := uc.grants.Insert(ctx, user.ID, input.Role, input.ScopeEntityID); err != nil {
		return nil, err
	}

	// user_grants is hard-deleted on change and carries no created_by, so the
	// provisioning of a machine principal survives HERE or nowhere.
	if err := uc.audit.Record(ctx, "service_account.created", "service_account", user.ID,
		audit.Changes(nil, audit.Values{
			"kind":   user.Kind,
			"status": user.Status,
			"grants": auditServiceAccountGrant(input.Role, input.ScopeEntityID),
		})); err != nil {
		return nil, err
	}
	return user, nil
}

// ListServiceAccounts returns the tenant's machine principals.
func (uc *UseCases) ListServiceAccounts(ctx context.Context) ([]domain.User, error) {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	return uc.users.ListByKind(ctx, tenantID, domain.KindService)
}

// IssueToken mints a bearer credential for a service account and returns the
// cleartext exactly once (only the SHA-256 hash is stored).
func (uc *UseCases) IssueToken(ctx context.Context, serviceAccountID, label string, ttlDays int) (*domain.IssueTokenResult, error) {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return nil, apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}

	sa, err := uc.users.GetByID(ctx, serviceAccountID)
	if err != nil {
		return nil, err
	}
	// users is not RLS'd: enforce the tenant boundary explicitly, and only
	// service accounts get tokens (humans use sessions + MFA).
	if sa == nil || sa.TenantID != tenantID || !sa.IsService() || !sa.IsActive() {
		return nil, apperrors.NewNotFound(domain.MsgServiceAccountNotFound)
	}

	if ttlDays < 1 || ttlDays > 365 {
		ttlDays = 90
	}
	secret, err := crypto.NewSessionToken()
	if err != nil {
		return nil, err
	}
	raw := domain.TokenPrefix + secret

	var createdBy *string
	if req := app.GetRequester(ctx); req != nil && req.ID != "" {
		id := req.ID
		createdBy = &id
	}

	token, err := uc.tokens.Create(ctx, domain.CreateAPITokenInput{
		TokenHash: crypto.HashToken(raw),
		UserID:    sa.ID,
		TenantID:  tenantID,
		Label:     label,
		CreatedBy: createdBy,
		ExpiresAt: uc.now().Add(time24h(ttlDays)),
	})
	if err != nil {
		return nil, err
	}

	// api_tokens.created_by holds the issuer too, but that is mutable
	// operational state outside the chain and invisible in the Audit Trail —
	// and revocation stamps a time with no actor at all. This is the entry an
	// auditor asked "who issued the token that read the tenant last month"
	// reads. The label is left out: free text, like every other name.
	if err := uc.audit.Record(ctx, "api_token.issued", "api_token", token.ID,
		auditTokenDetails(audit.Changes(nil, audit.Values{
			"serviceAccountId": sa.ID,
			"expiresAt":        token.ExpiresAt,
			"ttlDays":          ttlDays,
		}))); err != nil {
		return nil, err
	}
	return &domain.IssueTokenResult{Token: token, RawToken: raw}, nil
}

// RevokeToken tombstones a token within the caller's tenant.
func (uc *UseCases) RevokeToken(ctx context.Context, tokenID string) error {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	ok, err := uc.tokens.Revoke(ctx, tokenID, tenantID)
	if err != nil {
		return err
	}
	if !ok {
		return apperrors.NewNotFound(domain.MsgTokenNotFound)
	}

	// The transition IS the event: the moment a live credential stops being
	// one. Which account it spoke for is on the api_token.issued entry for the
	// same resource id — Revoke tombstones by id without reading the row back,
	// and the two entries join on the token rather than paying for that read.
	// Only a real revocation is recorded: a second attempt, or one from
	// another tenant, is a 404 above and writes nothing.
	if err := uc.audit.Record(ctx, "api_token.revoked", "api_token", tokenID,
		auditTokenDetails(audit.Changes(
			audit.Values{"revoked": false},
			audit.Values{"revoked": true},
		))); err != nil {
		return err
	}
	return nil
}
