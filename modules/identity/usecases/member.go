package usecases

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
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

	issued, err := uc.issueInvite(ctx, user.ID, tenantID)
	if err != nil {
		return nil, err
	}

	// The link now goes to the invited address, queued on THIS transaction (see
	// deliverInvite) rather than sent from here. The cleartext token STAYS in
	// the response for the moment, because the seeding tool and the demo oracle
	// read it from there; taking it out is the follow-up once delivery is proven
	// in a deployment, and it is what turns the invite into a credential the
	// administrator never sees.
	if err := uc.deliverInvite(ctx, domain.InviteMail{
		TokenID:  issued.TokenID,
		RawToken: issued.Raw,
		// The PERSISTED address and name, not the request's: the mail must
		// greet the account that now exists, and the row is the only thing that
		// says what that is.
		Email:     user.Email,
		Name:      user.Name,
		ExpiresAt: issued.ExpiresAt,
	}); err != nil {
		return nil, err
	}

	member, err := uc.mustMember(ctx, tenantID, user.ID)
	if err != nil {
		return nil, err
	}

	// Creating a human principal is an access grant, and it used to be the one
	// access event recorded as a summary rather than a state: {"role": …,
	// "scoped": true} named the role and said only that a scope EXISTED, so the
	// entity a scoped person was confined to was in no row anywhere —
	// user_grants is hard-deleted, so once the grant is replaced the original
	// is unreconstructable. The same grant to a MACHINE has been recorded in
	// full since service_account.created, which made an invite the only door
	// out of that record. Both doors now write the same envelope, field for
	// field, off the member this call already had to load for its response —
	// plus the window of the credential the invite just minted, so an invite
	// and a re-issue (member.invite_reissued) also compare directly.
	if err := uc.audit.Record(ctx, "member.invited", "user", member.ID,
		audit.Changes(nil, auditInvitedValues(member, issued.ExpiresAt))); err != nil {
		return nil, err
	}
	return &domain.InviteResult{Member: member, RawToken: issued.Raw, ExpiresAt: issued.ExpiresAt}, nil
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
	if _, err := uc.invites.RevokeUnusedForUser(ctx, member.ID); err != nil {
		return nil, err
	}
	issued, err := uc.issueInvite(ctx, member.ID, tenantID)
	if err != nil {
		return nil, err
	}
	// A re-issue is the "resend the link" action, so it delivers too — and
	// because the dedupe key is the TOKEN's id and this is a new token, it is a
	// new message rather than one the outbox suppresses as already owed.
	if err := uc.deliverInvite(ctx, domain.InviteMail{
		TokenID:   issued.TokenID,
		RawToken:  issued.Raw,
		Email:     member.Email,
		Name:      member.Name,
		ExpiresAt: issued.ExpiresAt,
	}); err != nil {
		return nil, err
	}
	// A re-issue is one-sided: nothing on the member row moves, a credential is
	// minted. So the envelope records what the new credential IS — the window
	// it is valid for, and the access it will unlock on acceptance — in the
	// same "to"-only form audit.Changes gives a create. Without it the trail
	// said only that somebody re-invited somebody.
	if err := uc.audit.Record(ctx, "member.invite_reissued", "user", member.ID,
		audit.Changes(nil, audit.Values{
			"status":          member.Status,
			"inviteExpiresAt": issued.ExpiresAt,
			"grants":          auditMemberGrants(member.Grants),
		})); err != nil {
		return nil, err
	}
	return &domain.InviteResult{Member: member, RawToken: issued.Raw, ExpiresAt: issued.ExpiresAt}, nil
}

// issuedInvite is a freshly minted invite credential: the row's id (which
// identifies the ONE mail it is worth — see the outbox dedupe key), the
// cleartext the recipient needs, and the window it is valid for.
type issuedInvite struct {
	TokenID   string
	Raw       string
	ExpiresAt time.Time
}

// issueInvite mints "zti_" + 32 random bytes (base64url), stores its SHA-256
// and returns the cleartext + expiry.
func (uc *UseCases) issueInvite(ctx context.Context, userID, tenantID string) (issuedInvite, error) {
	secret, err := crypto.NewSessionToken()
	if err != nil {
		return issuedInvite{}, err
	}
	raw := domain.InviteTokenPrefix + secret

	var createdBy *string
	if req := app.GetRequester(ctx); req != nil && req.ID != "" {
		id := req.ID
		createdBy = &id
	}
	expiresAt := uc.now().Add(domain.InviteTTL).UTC()
	token, err := uc.invites.Create(ctx, domain.CreateInviteTokenInput{
		TokenHash: crypto.HashToken(raw),
		UserID:    userID,
		TenantID:  tenantID,
		CreatedBy: createdBy,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return issuedInvite{}, err
	}
	// The row's id, not a second identifier: it is what makes "this invite has
	// already queued its mail" a fact Postgres can enforce.
	var tokenID string
	if token != nil {
		tokenID = token.ID
	}
	return issuedInvite{TokenID: tokenID, Raw: raw, ExpiresAt: expiresAt}, nil
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

	var revoked *domain.CredentialRevocation
	if status == domain.StatusDisabled {
		sessions, err := uc.sessions.RevokeAllForUser(ctx, member.ID)
		if err != nil {
			return nil, err
		}
		tokens, err := uc.tokens.RevokeAllForUser(ctx, member.ID)
		if err != nil {
			return nil, err
		}
		invites, err := uc.invites.RevokeUnusedForUser(ctx, member.ID)
		if err != nil {
			return nil, err
		}
		revoked = &domain.CredentialRevocation{
			Sessions: sessions, APITokens: tokens, Invites: invites,
		}
		// Disabling the last tenant admin would orphan the tenant.
		if err := uc.ensureTenantAdminRemains(ctx, tenantID); err != nil {
			return nil, err
		}
	}

	// Both sides are rows this request already read — `member` from the 404
	// check above, `updated` from the response it is about to return — so the
	// envelope records the rename and the enable/disable it actually performed
	// (including the invited-not-active landing above) at no extra cost.
	updated, err := uc.mustMember(ctx, tenantID, member.ID)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "member.updated", "user", member.ID,
		audit.Changes(auditMemberValues(member), auditMemberValues(updated))); err != nil {
		return nil, err
	}
	if err := uc.recordCredentialRevocation(ctx, updated, revoked); err != nil {
		return nil, err
	}
	return updated, nil
}

// recordCredentialRevocation writes the second half of a disable: the security
// event, as its own entry, immediately after the status change that caused it.
//
// Why it is a separate entry rather than three more fields on member.updated.
// A status change and a credential revocation are different facts with
// different lifetimes and different readers. "status: active -> disabled" is a
// change to the member ROW, and it is reversible — re-enabling restores it. The
// revocation is not a change to any row this envelope describes: it is an
// irreversible event on OTHER rows (sessions, api_tokens, invite_tokens), and
// re-enabling the member does not bring a single killed credential back. Folding
// counts into the member's change set would also break the one invariant
// audit.Changes rests on — that `fields` is a before/after map of the resource's
// whitelist — and would make the event unfindable: an access review that asks
// "when did this token stop working?" can select on the action, which is
// exactly how the explicit route's api_token.revoked is already found. Giving
// the same fact two shapes depending on which door ended the credential is the
// asymmetry the sweep exists to remove, and one entry per revocation, keyed by
// its own action, is the shape the explicit door already uses.
//
// What it records is counts by credential KIND, not identifiers. Naming every
// token would mean either a read-back of the rows the UPDATE just tombstoned or
// a RETURNING clause threaded through three ports, and it would put a list of
// credential ids in an append-only ledger for no additional answer: api_tokens
// keeps revoked_at, so an auditor who needs the ids has the live table, joined
// to the api_token.issued entries that already name each token as resource_id.
// What the table cannot say is WHY they died, or that they died together, as
// one act, by one actor, at one instant — which is precisely what this entry
// carries. principalKind is on it because a machine losing its standing
// credential and a person losing their sessions are different security events
// and only the kind separates them in a trail keyed by user id.
//
// It is written even when every count is zero. "The revocation ran and nothing
// was live" is an answer; silence is not, and silence is what a reader would
// otherwise have to distinguish from "the revocation never ran".
func (uc *UseCases) recordCredentialRevocation(ctx context.Context, member *domain.Member, revoked *domain.CredentialRevocation) error {
	if revoked == nil {
		return nil
	}
	if err := uc.audit.Record(ctx, "member.credentials_revoked", "user", member.ID, map[string]any{
		"credentialsRevoked": map[string]any{
			"sessions":  revoked.Sessions,
			"apiTokens": revoked.APITokens,
			"invites":   revoked.Invites,
		},
		"principalKind": member.Kind,
		"trigger":       domain.RevocationTriggerDisabled,
	}); err != nil {
		return err
	}

	// The same act, in the other stream (ADR-0008 stream 2), and not a
	// duplicate: the trail entry above is the ACCESS REVIEW record — an
	// administrator ended these credentials, and here are the counts by kind —
	// while this one is the INCIDENT TIMELINE record: every live session of this
	// principal stopped working at this instant. The two streams are read by
	// different people asking different questions, and a breach timeline that
	// had to be reconstructed by joining a tenant's business trail would be
	// missing exactly the tenant-less rows that stream is for.
	//
	// ActorID is the administrator, PrincipalID the member whose access ended:
	// the one event in the stream where they are different people, which is why
	// the table carries both.
	var actorID string
	if req := app.GetRequester(ctx); req != nil && req.IsUser() {
		actorID = req.ID
	}
	uc.note(ctx, securityevent.Event{
		Event:       securityevent.EventSessionsRevoked,
		Outcome:     securityevent.OutcomeSuccess,
		Method:      securityevent.MethodSession,
		Reason:      securityevent.ReasonMemberDisabled,
		TenantID:    member.TenantID,
		PrincipalID: member.ID,
		ActorID:     actorID,
	})
	return nil
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
	return uc.finishGrantChange(ctx, tenantID, member)
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
	return uc.finishGrantChange(ctx, tenantID, member)
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
	var found bool
	for i := range member.Grants {
		if member.Grants[i].ID == grantID {
			found = true
			break
		}
	}
	if !found {
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
	_, err = uc.finishGrantChange(ctx, tenantID, member)
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
//
// `before` is the member as its caller already loaded it, on this same
// transaction, BEFORE touching user_grants — the grant set that is about to be
// replaced. It costs no extra read and is the only surviving copy: user_grants
// is hard-deleted. All three grant paths (replace-all, add one, remove one)
// therefore record the same thing — the grant set on both sides — rather than
// the one role that happened to be named in the request, which said nothing
// about what the member could do before, and on an add or a remove was not even
// a change of role.
func (uc *UseCases) finishGrantChange(ctx context.Context, tenantID string, before *domain.Member) (*domain.Member, error) {
	if err := uc.ensureTenantAdminRemains(ctx, tenantID); err != nil {
		return nil, err
	}
	member, err := uc.mustMember(ctx, tenantID, before.ID)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "member.role_changed", "user", member.ID,
		audit.Changes(auditMemberValues(before), auditMemberValues(member))); err != nil {
		return nil, err
	}
	return member, nil
}

// auditMemberValues is the ADR-0008 whitelist for a member — the access-review
// envelope, and the one place in the system where prior state exists nowhere
// else: user_grants is hard-deleted (no revoked_at, no history table), so a
// grant that is replaced or removed survives only here. It feeds audit.Changes,
// which records the before and after of whatever moved.
//
// Quoted verbatim (enums, a bool, uuids):
//
//   - grants: the whole grant set, as role + scope entity id per grant — see
//     auditMemberGrants. This is the segregation-of-duties record: a demotion
//     from reviewer to preparer, or an escalation from viewer to tenant_admin,
//     is reconstructable from the two sides. Both halves are constrained — the
//     role by authz.KnownRole, the scope by a `uuid` and a composite FK.
//   - status: invited / active / disabled.
//   - kind: human / service. A machine principal can never approve (ADR-0012),
//     so what kind of principal held a grant is part of the control question.
//   - mfaEnabled: whether the credential behind those grants was second-factor
//     protected.
//
// Redacted — the change is dated, the value withheld: name and email. Both are
// personal data under ADR-0007, and the audit log is outside the erasure
// boundary, so the trail must never duplicate them. The actor is already
// carried as actor_id, and the member is resource_id.
func auditMemberValues(m *domain.Member) audit.Values {
	if m == nil {
		return nil
	}
	return audit.Values{
		"name":       audit.Redact(m.Name),
		"email":      audit.Redact(m.Email),
		"kind":       m.Kind,
		"status":     m.Status,
		"mfaEnabled": m.MFAEnabled,
		"grants":     auditMemberGrants(m.Grants),
	}
}

// auditInvitedValues is that whitelist plus the invite window: the same fields
// member.role_changed and service_account.created record — so the three
// access-creation events compare field for field — and the expiry of the
// credential that will activate the account, which is what member.invite_reissued
// records for the second and later ones.
func auditInvitedValues(m *domain.Member, inviteExpiresAt time.Time) audit.Values {
	values := auditMemberValues(m)
	if values == nil {
		return nil
	}
	values["inviteExpiresAt"] = inviteExpiresAt
	return values
}

// auditMemberGrants projects the grant set onto what an access review asks of
// it: which role, and over which entity subtree (nothing for a tenant-wide
// grant). The scope entity's NAME is deliberately absent — it is the entity's
// free text, and a scope name is exactly what a reader outside that subtree
// must not learn from the trail.
//
// Grant ids are left out and the list is sorted, so the two sides compare by
// MEANING: replacing a grant with an identical one (SetRole to the same role
// mints a new row id) records no change, which is the honest answer.
func auditMemberGrants(grants []domain.MemberGrant) []map[string]any {
	if len(grants) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(grants))
	for i := range grants {
		grant := &grants[i]
		entry := map[string]any{"role": grant.Role}
		if !grant.IsTenantWide() {
			entry["scopeEntityId"] = *grant.ScopeEntityID
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := auditGrantKey(out[i]), auditGrantKey(out[j])
		return left < right
	})
	return out
}

// auditGrantKey orders a projected grant: by role, then by scope (tenant-wide
// first). Only the sort depends on it — it never reaches the envelope.
func auditGrantKey(g map[string]any) string {
	role, _ := g["role"].(string)
	scope, _ := g["scopeEntityId"].(string)
	return role + "\x00" + scope
}

// activatedName mirrors the repository's `name = COALESCE($3, name)`: an
// accept-invite that sends no display name keeps the invited one, so the trail
// must not claim a rename that did not happen.
func activatedName(current string, override *string) string {
	if override == nil {
		return current
	}
	return *override
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
//
// IT IS THE ONE PUBLIC ROUTE THAT ESTABLISHES A CREDENTIAL, which is why every
// outcome reaches the security stream (ADR-0008 stream 2). A redemption is the
// birth certificate of a human principal — the moment an account that could not
// sign in can — and the refusals are the only trace somebody working through
// invite tokens leaves anywhere: they are answered with one uninformative 400,
// they mint no session, and they write no audit entry, so without these events
// the whole surface is invisible in both stores. The reason class is what the
// response withholds and the operator is owed: unknown_credential is somebody
// guessing, credential_expired is a real invitation that ran out its clock or
// was spent, principal_not_usable is a token presented for an account that can
// no longer redeem it.
//
// WHAT IS NOT RECORDED: anything derived from the token. Not the value, not its
// hash, not a keyed digest of it — this ledger is append-only and cannot
// unpublish a verifier for a live credential later, which is the same rule
// AuthenticateToken follows. An unidentified refusal therefore correlates by
// address alone, and that is exactly why the address has to be the caller's
// rather than the load balancer's.
func (uc *UseCases) AcceptInvite(ctx context.Context, input domain.AcceptInviteInput) (*domain.AcceptInviteResult, error) {
	invalid := apperrors.NewValidation(domain.MsgInviteInvalid)
	raw := strings.TrimSpace(input.Token)
	if !strings.HasPrefix(raw, domain.InviteTokenPrefix) {
		// Recorded, unlike the malformed Authorization header AuthenticateToken
		// ignores: that arrives on every route from every client that guessed the
		// scheme wrong, while a POST to /auth/accept-invite is an attempt at this
		// specific credential door and nothing else. The volume is bounded by the
		// anonymous budget and the verification gate the route already sits
		// behind.
		uc.noteInviteRejected(ctx, securityevent.ReasonUnknownCredential, "", "")
		return nil, invalid
	}
	now := uc.now()

	token, err := uc.invites.GetByTokenHash(ctx, crypto.HashToken(raw))
	if err != nil {
		return nil, err
	}
	if token == nil {
		// Well-formed and matches no row: probing, or a link from another
		// deployment. Nothing is known about who this is.
		uc.noteInviteRejected(ctx, securityevent.ReasonUnknownCredential, "", "")
		return nil, invalid
	}
	if !token.IsUsable(now) {
		// A real invitation, past its life or already spent. Told apart from the
		// unknown case on purpose: one is a person who needs a new invitation,
		// the other is somebody guessing.
		uc.noteInviteRejected(ctx, securityevent.ReasonCredentialExpired, token.TenantID, token.UserID)
		return nil, invalid
	}
	user, err := uc.users.GetByID(ctx, token.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.TenantID != token.TenantID || user.IsService() || user.Status != domain.StatusInvited {
		// The credential resolved; the account behind it cannot redeem it —
		// already active, disabled, gone, or a machine principal. This is the row
		// that says a revoked person's invitation link is still being clicked.
		uc.noteInviteRejected(ctx, securityevent.ReasonPrincipalNotUsable, token.TenantID, token.UserID)
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

	// A refusal decided INSIDE the transaction is classified here and recorded
	// after it returns, never within it: the refusal rolls the transaction back,
	// and an event written on it would roll back with it — the same reason the
	// public /auth routes declare no transaction at all (securityevent.Record).
	// Only the two lost-race branches set this; a repository error is a failure,
	// not a refusal, and must not be filed as one.
	refused := ""
	err = uc.withinTx(txCtx, func(ctx context.Context) error {
		ok, err := uc.invites.MarkAccepted(ctx, token.ID, now)
		if err != nil {
			return err
		}
		if !ok {
			// Lost a race with a concurrent accept or a revocation: the token was
			// live when it was read and is spent now.
			refused = securityevent.ReasonCredentialExpired
			return invalid
		}
		ok, err = uc.members.Activate(ctx, user.ID, hash, input.Name)
		if err != nil {
			return err
		}
		if !ok {
			// The account stopped being invited between the read and the update.
			refused = securityevent.ReasonPrincipalNotUsable
			return invalid
		}
		// Any sibling tokens are spent too — one invite activates at most once.
		if _, err := uc.invites.RevokeUnusedForUser(ctx, user.ID); err != nil {
			return err
		}
		// The activation is a real transition and the user row that preceded it
		// is already loaded: invited → active, and no credential → a credential.
		// That second half is the one an access review cares about, because it
		// is the moment the account becomes usable.
		return uc.audit.Record(ctx, "member.activated", "user", user.ID,
			audit.Changes(
				audit.Values{
					"status":      user.Status,
					"hasPassword": user.PasswordHash != nil,
					"name":        audit.Redact(user.Name),
				},
				audit.Values{
					"status":      domain.StatusActive,
					"hasPassword": true,
					"name":        audit.Redact(activatedName(user.Name, input.Name)),
				},
			))
	})
	if err != nil {
		if refused != "" {
			uc.noteInviteRejected(ctx, refused, token.TenantID, user.ID)
		}
		return nil, err
	}

	// The redemption happened: recorded after the commit, on the caller's
	// context rather than the transaction's, so a hiccup in the stream can
	// neither undo an activation that has already succeeded nor abort the
	// transaction that performed it. The hole that leaves — committed, then the
	// process dies before the event lands — is logged loudly by Note, and is the
	// trade every refusal on this stream already makes.
	uc.note(ctx, securityevent.Event{
		Event:       securityevent.EventInviteAccepted,
		Outcome:     securityevent.OutcomeSuccess,
		Method:      securityevent.MethodInviteToken,
		TenantID:    token.TenantID,
		PrincipalID: user.ID,
	})
	return &domain.AcceptInviteResult{Email: user.Email}, nil
}

// noteInviteRejected records a refused redemption of an invitation.
//
// Neither id is required: the token that matched nothing names no tenant and no
// principal, which is the same shape as a failed login for an address nobody
// recognises — and the reason audit_log could never hold these rows.
func (uc *UseCases) noteInviteRejected(ctx context.Context, reason, tenantID, principalID string) {
	uc.note(ctx, securityevent.Event{
		Event:       securityevent.EventInviteRejected,
		Outcome:     securityevent.OutcomeFailure,
		Method:      securityevent.MethodInviteToken,
		Reason:      reason,
		TenantID:    tenantID,
		PrincipalID: principalID,
	})
}
