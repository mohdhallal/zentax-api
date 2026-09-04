package usecases

import (
	"context"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

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
	return nil
}
