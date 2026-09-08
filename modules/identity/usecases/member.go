package usecases

import (
	"context"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Member administration (ADR-0011/0012). Every method here runs on a tenant
// route: the tenant comes from the requester's session, never from the URL,
// and — users not being RLS-scoped — is passed explicitly to every users
// statement. A member of another tenant is a 404, indistinguishable from an
// unknown id.

const (
	defaultMemberPageSize = 100
	maxMemberPageSize     = 500
)

func (uc *UseCases) requireTenant(ctx context.Context) (string, error) {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return "", apperrors.NewUnauthorized(domain.MsgSessionInvalid)
	}
	return tenantID, nil
}

func (uc *UseCases) ListMembers(ctx context.Context, kind, status, search string, limit, offset int) ([]domain.Member, int, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = defaultMemberPageSize
	}
	if limit > maxMemberPageSize {
		limit = maxMemberPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return uc.members.List(ctx, domain.ListMembersArgs{
		TenantID: tenantID, Kind: kind, Status: status, Search: strings.TrimSpace(search), Limit: limit, Offset: offset,
	})
}

func (uc *UseCases) GetMember(ctx context.Context, id string) (*domain.Member, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	return uc.mustMember(ctx, tenantID, id)
}

// mustMember loads a member of the tenant or returns the 404.
func (uc *UseCases) mustMember(ctx context.Context, tenantID, id string) (*domain.Member, error) {
	m, err := uc.members.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, apperrors.NewNotFound(domain.MsgMemberNotFound)
	}
	return m, nil
}

// Invite creates an invited human user (no password), its first grant, and a
// one-time invite token whose cleartext is returned exactly once.
func (uc *UseCases) Invite(ctx context.Context, input domain.CreateMemberInput) (*domain.InviteResult, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateGrant(false, domain.GrantInput{Role: input.Role, ScopeEntityID: input.ScopeEntityID}); err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))

	user, err := uc.users.Create(ctx, domain.CreateUserInput{
		TenantID: tenantID,
		Email:    email,
		Name:     input.Name,
		Kind:     domain.KindHuman,
		Status:   domain.StatusInvited,
	})
	if err != nil {
		if database.IsUniqueViolation(err) {
			return nil, apperrors.NewConflict(domain.MsgMemberEmailTaken)
		}
		return nil, err
	}

	if _, err := uc.members.InsertGrant(ctx, user.ID, input.Role, input.ScopeEntityID); err != nil {
		return nil, err
	}

	raw, expiresAt, err := uc.issueInvite(ctx, user.ID, tenantID)
	if err != nil {
		return nil, err
	}

	if err := uc.audit.Record(ctx, "member.invited", "user", user.ID,
		map[string]any{"role": input.Role, "scoped": input.ScopeEntityID != nil}); err != nil {
		return nil, err
	}

	member, err := uc.mustMember(ctx, tenantID, user.ID)
	if err != nil {
		return nil, err
	}
	return &domain.InviteResult{Member: member, RawToken: raw, ExpiresAt: expiresAt}, nil
}

// ReissueInvite mints a fresh token for a still-invited member and revokes
// every earlier unused one (exactly one live invite at a time).
func (uc *UseCases) ReissueInvite(ctx context.Context, memberID string) (*domain.InviteResult, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	member, err := uc.mustMember(ctx, tenantID, memberID)
	if err != nil {
		return nil, err
	}
	if !member.IsInvited() || member.IsService() {
		return nil, apperrors.NewConflict(domain.MsgMemberNotInvited)
	}
	if err := uc.invites.RevokeUnusedForUser(ctx, member.ID); err != nil {
		return nil, err
	}
	raw, expiresAt, err := uc.issueInvite(ctx, member.ID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "member.invite_reissued", "user", member.ID, nil); err != nil {
		return nil, err
	}
	return &domain.InviteResult{Member: member, RawToken: raw, ExpiresAt: expiresAt}, nil
}

// issueInvite mints "zti_" + 32 random bytes (base64url), stores its SHA-256
// and returns the cleartext + expiry.
func (uc *UseCases) issueInvite(ctx context.Context, userID, tenantID string) (string, time.Time, error) {
	secret, err := crypto.NewSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	raw := domain.InviteTokenPrefix + secret

	var createdBy *string
	if req := app.GetRequester(ctx); req != nil && req.ID != "" {
		id := req.ID
		createdBy = &id
	}
	expiresAt := uc.now().Add(domain.InviteTTL).UTC()
	if _, err := uc.invites.Create(ctx, domain.CreateInviteTokenInput{
		TokenHash: crypto.HashToken(raw),
		UserID:    userID,
		TenantID:  tenantID,
		CreatedBy: createdBy,
		ExpiresAt: expiresAt,
	}); err != nil {
		return "", time.Time{}, err
	}
	return raw, expiresAt, nil
}

// UpdateMember renames and/or enables/disables a member. Disabling revokes
// every session, API token and outstanding invite of that user; the caller can
// never disable itself, and an invited member is activated only by accepting
// its invite (never through this route).
func (uc *UseCases) UpdateMember(ctx context.Context, memberID string, input domain.UpdateMemberInput) (*domain.Member, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	member, err := uc.mustMember(ctx, tenantID, memberID)
	if err != nil {
		return nil, err
	}

	status := input.Status
	switch status {
	case domain.StatusDisabled:
		if req := app.GetRequester(ctx); req != nil && req.ID == member.ID {
			return nil, apperrors.NewValidation(domain.MsgCannotDisableSelf)
		}
		if err := uc.members.LockAdminGuard(ctx, tenantID); err != nil {
			return nil, err
		}
	case domain.StatusActive:
		if member.IsInvited() {
			return nil, apperrors.NewValidation(domain.MsgInvitedNotActivatable)
		}
		// A human disabled before it ever accepted its invite has no password:
		// re-enabling returns it to invited (re-issue the invite to onboard it)
		// instead of minting an active account nobody can sign in to.
		if !member.IsService() && !member.HasPassword {
			status = domain.StatusInvited
		}
	default:
		return nil, apperrors.NewValidation("status must be active or disabled")
	}

	if _, err := uc.members.UpdateNameStatus(ctx, tenantID, member.ID, input.Name, status); err != nil {
		return nil, err
	}

	if status == domain.StatusDisabled {
		if err := uc.sessions.RevokeAllForUser(ctx, member.ID); err != nil {
			return nil, err
		}
		if err := uc.tokens.RevokeAllForUser(ctx, member.ID); err != nil {
			return nil, err
		}
		if err := uc.invites.RevokeUnusedForUser(ctx, member.ID); err != nil {
			return nil, err
		}
		// Disabling the last tenant admin would orphan the tenant.
		if err := uc.ensureTenantAdminRemains(ctx, tenantID); err != nil {
			return nil, err
		}
	}

	if err := uc.audit.Record(ctx, "member.updated", "user", member.ID,
		map[string]any{"status": status}); err != nil {
		return nil, err
	}
	return uc.mustMember(ctx, tenantID, member.ID)
}

// SetRole REPLACES every grant of the member with the given one.
func (uc *UseCases) SetRole(ctx context.Context, memberID string, input domain.GrantInput) (*domain.Member, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	member, err := uc.mustMember(ctx, tenantID, memberID)
	if err != nil {
		return nil, err
	}
	if err := validateGrant(member.IsService(), input); err != nil {
		return nil, err
	}
	if err := uc.members.LockAdminGuard(ctx, tenantID); err != nil {
		return nil, err
	}
	if err := uc.members.DeleteGrantsForUser(ctx, member.ID); err != nil {
		return nil, err
	}
	if _, err := uc.members.InsertGrant(ctx, member.ID, input.Role, input.ScopeEntityID); err != nil {
		return nil, err
	}
	return uc.finishGrantChange(ctx, tenantID, member.ID, input.Role, input.ScopeEntityID != nil)
}

// AddGrant ADDS one grant to the member's set.
func (uc *UseCases) AddGrant(ctx context.Context, memberID string, input domain.GrantInput) (*domain.Member, error) {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	member, err := uc.mustMember(ctx, tenantID, memberID)
	if err != nil {
		return nil, err
	}
	if err := validateGrant(member.IsService(), input); err != nil {
		return nil, err
	}
	if err := uc.members.LockAdminGuard(ctx, tenantID); err != nil {
		return nil, err
	}
	if _, err := uc.members.InsertGrant(ctx, member.ID, input.Role, input.ScopeEntityID); err != nil {
		return nil, err
	}
	return uc.finishGrantChange(ctx, tenantID, member.ID, input.Role, input.ScopeEntityID != nil)
}

// RemoveGrant deletes one grant of the member.
func (uc *UseCases) RemoveGrant(ctx context.Context, memberID, grantID string) error {
	tenantID, err := uc.requireTenant(ctx)
	if err != nil {
		return err
	}
	member, err := uc.mustMember(ctx, tenantID, memberID)
	if err != nil {
		return err
	}
	var removed *domain.MemberGrant
	for i := range member.Grants {
		if member.Grants[i].ID == grantID {
			removed = &member.Grants[i]
			break
		}
	}
	if removed == nil {
		return apperrors.NewNotFound(domain.MsgGrantNotFound)
	}
	if err := uc.members.LockAdminGuard(ctx, tenantID); err != nil {
		return err
	}
	ok, err := uc.members.DeleteGrant(ctx, member.ID, grantID)
	if err != nil {
		return err
	}
	if !ok {
		return apperrors.NewNotFound(domain.MsgGrantNotFound)
	}
	_, err = uc.finishGrantChange(ctx, tenantID, member.ID, removed.Role, !removed.IsTenantWide())
	return err
}

// validateGrant checks the role is known; that tenant_admin is only ever
// tenant-wide (capability checks on tenant-level routes such as /members are
// scope-agnostic, so a "scoped admin" would administer the whole tenant); and
// that a machine never receives member:manage — no self-replication (ADR-0012).
func validateGrant(isService bool, input domain.GrantInput) error {
	if !authz.KnownRole(authz.Role(input.Role)) {
		return apperrors.NewValidation("unknown role: " + input.Role)
	}
	if authz.Role(input.Role) == authz.RoleTenantAdmin {
		if isService {
			return apperrors.NewValidation(domain.MsgServiceCannotBeAdmin)
		}
		if input.ScopeEntityID != nil {
			return apperrors.NewValidation(domain.MsgAdminMustBeTenantWide)
		}
	}
	return nil
}

// finishGrantChange applies the last-admin guard (on the request tx, so it
// sees the uncommitted change — a violation rolls the whole request back),
// records the audit entry and returns the fresh member view.
func (uc *UseCases) finishGrantChange(ctx context.Context, tenantID, memberID, role string, scoped bool) (*domain.Member, error) {
	if err := uc.ensureTenantAdminRemains(ctx, tenantID); err != nil {
		return nil, err
	}
	member, err := uc.mustMember(ctx, tenantID, memberID)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "member.role_changed", "user", member.ID,
		map[string]any{"role": role, "scoped": scoped, "grants": len(member.Grants)}); err != nil {
		return nil, err
	}
	return member, nil
}

func (uc *UseCases) ensureTenantAdminRemains(ctx context.Context, tenantID string) error {
	n, err := uc.members.CountActiveTenantAdmins(ctx, tenantID)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperrors.NewConflict(domain.MsgLastTenantAdmin)
	}
	return nil
}

// AcceptInvite is PUBLIC (no session, no tenant): it resolves the token by
// hash, then performs the activation + audit inside a transaction bound to the
// tenant and user TAKEN FROM THE TOKEN ROW — the same Tx seam every tenant
// route uses, so RLS-scoped writes (audit_log) land in the right tenant chain.
// Every failure is the same generic 400: the response never reveals whether a
// token or email exists, nor why it was refused.
func (uc *UseCases) AcceptInvite(ctx context.Context, input domain.AcceptInviteInput) (*domain.AcceptInviteResult, error) {
	invalid := apperrors.NewValidation(domain.MsgInviteInvalid)
	raw := strings.TrimSpace(input.Token)
	if !strings.HasPrefix(raw, domain.InviteTokenPrefix) {
		return nil, invalid
	}
	now := uc.now()

	token, err := uc.invites.GetByTokenHash(ctx, crypto.HashToken(raw))
	if err != nil {
		return nil, err
	}
	if token == nil || !token.IsUsable(now) {
		return nil, invalid
	}
	user, err := uc.users.GetByID(ctx, token.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.TenantID != token.TenantID || user.IsService() || user.Status != domain.StatusInvited {
		return nil, invalid
	}

	hash, err := crypto.HashPassword(input.Password)
	if err != nil {
		return nil, err
	}

	// Bind the token's tenant + the activating user as the actor: audit is
	// attributed to the member who accepted, in their tenant's chain.
	txCtx := app.WithTenantID(ctx, token.TenantID)
	txCtx = app.WithRequester(txCtx, &app.Requester{Kind: app.RequesterUser, ID: user.ID})

	err = uc.withinTx(txCtx, func(ctx context.Context) error {
		ok, err := uc.invites.MarkAccepted(ctx, token.ID, now)
		if err != nil {
			return err
		}
		if !ok {
			return invalid // lost a race with a concurrent accept / revoke
		}
		ok, err = uc.members.Activate(ctx, user.ID, hash, input.Name)
		if err != nil {
			return err
		}
		if !ok {
			return invalid
		}
		// Any sibling tokens are spent too — one invite activates at most once.
		if err := uc.invites.RevokeUnusedForUser(ctx, user.ID); err != nil {
			return err
		}
		return uc.audit.Record(ctx, "member.activated", "user", user.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return &domain.AcceptInviteResult{Email: user.Email}, nil
}
