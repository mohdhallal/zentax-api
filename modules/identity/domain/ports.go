package domain

import (
	"context"
	"time"
)

type UserRepository interface {
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	Create(ctx context.Context, input CreateUserInput) (*User, error)
	// ListByKind returns a tenant's users of one kind (users is not RLS'd, so
	// the tenant is scoped explicitly).
	ListByKind(ctx context.Context, tenantID, kind string) ([]User, error)
	// ConsumeLoginAttempt spends one attempt from the account's lockout budget
	// and reports whether this attempt was inside it. It applies the whole
	// policy atomically, in a single statement evaluated under the row lock that
	// both updates the row and returns the decision — never
	// read-then-decide-then-write, which is what let concurrency widen the
	// budget:
	//
	//   - a LIVE lock changes nothing at all — counter, stamp and updated_at —
	//     and reports NOT allowed, so a lock can never be extended by the
	//     attempts it is refusing;
	//   - an EXPIRED lock is cleared first, so this attempt starts a fresh
	//     window at 1 and IS allowed — an elapsed window clears the debt;
	//   - otherwise the counter increments, saturating at lockThreshold, the
	//     attempt that REACHES lockThreshold stamps locked_until with lockUntil,
	//     and the attempt is allowed. The threshold-th attempt is the one that
	//     arms the lock, not the first one refused by it.
	//
	// "now" is the caller's clock — the one that also produced lockUntil — so
	// expiry is judged against the clock that wrote the stamp.
	//
	// Callers MUST consume BEFORE verifying a password, and MUST NOT hold a
	// transaction open across that verification: the whole point is a short
	// statement that commits before ~70ms of argon2, so simultaneous attempts
	// serialise on the row for a millisecond instead of queueing behind each
	// other's hashing — and so that no answer can outrun the attempt it spent.
	ConsumeLoginAttempt(
		ctx context.Context, id string, lockThreshold int, now, lockUntil time.Time,
	) (allowed bool, err error)
	// ResetFailedLogin clears the counter AND any lock (including one whose
	// window has already elapsed): a successful login leaves no residue.
	ResetFailedLogin(ctx context.Context, id string) error
	SetTOTP(ctx context.Context, id string, secretEnc *string, enabled bool) error
}

type SessionRepository interface {
	Create(ctx context.Context, input CreateSessionInput) (*Session, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	Touch(ctx context.Context, id string, idleExpiresAt time.Time) error
	// CompleteMFA clears mfa_pending, rotates the token (fixation defense) AND
	// clears the second-factor attempt budget — a verification that succeeds
	// leaves no debt behind, in the same statement that completes it.
	CompleteMFA(ctx context.Context, id, newTokenHash string, idleExpiresAt, absoluteExpiresAt time.Time) error
	// ConsumeMFAAttempt spends one attempt from this session's second-factor
	// budget and reports whether THIS attempt was inside it, plus the count
	// after the charge. It is the MFA twin of ConsumeLoginAttempt and exists
	// for the same reason: ONE statement, evaluated under the row lock that
	// also updates the row, so the counter and the decision can never disagree
	// and concurrent guesses cannot each read a pre-threshold row and be
	// admitted together.
	//
	//   - below the threshold: the counter increments and the attempt is
	//     allowed. The attempt that REACHES the threshold is still allowed — it
	//     is the last one of the budget, not the first one refused by it.
	//   - at the threshold (the budget is already spent): the counter stays
	//     saturated and the attempt is refused. The row is still rewritten, so
	//     a refused attempt costs what a charged one costs.
	//
	// Callers MUST consume BEFORE validating the code, and MUST NOT be inside a
	// request transaction when they do: the tx middleware rolls a request
	// transaction back on any 4xx, which would discard the very charge the
	// refusal depends on (the failure mode that once made the login lockout
	// unreachable — see handlers.MfaVerifyHandler.DefineRoute).
	ConsumeMFAAttempt(ctx context.Context, id string, threshold int) (allowed bool, spent int, err error)
	// ClearMFAAttempts zeroes the second-factor budget of a session that is not
	// completing MFA: enrolment confirmation's success, and the path that
	// destroys a pending enrolment — both leave the session itself alive.
	ClearMFAAttempts(ctx context.Context, id string) error
	Revoke(ctx context.Context, id string) error
	RevokeAllForUser(ctx context.Context, userID string) error
}

// SessionAuthenticator resolves a session-cookie token to its session + user.
// Consumed by the RequireAuth middleware (kept narrow so the middleware does
// not depend on the whole use-case surface).
type SessionAuthenticator interface {
	Authenticate(ctx context.Context, token string) (*Session, *User, error)
}

// TokenAuthenticator resolves a bearer API token ("ztx_...") to its token +
// service-account user (machine identity, agentic-AI readiness B1).
type TokenAuthenticator interface {
	AuthenticateToken(ctx context.Context, raw string) (*APIToken, *User, error)
}

// RequestAuthenticator is what the auth middleware needs: both credential
// forms. Implemented by the identity use cases.
type RequestAuthenticator interface {
	SessionAuthenticator
	TokenAuthenticator
}

// TokenRepository persists API tokens. Not RLS-scoped — tenant-sensitive
// queries take the tenant explicitly and must scope by it.
type TokenRepository interface {
	Create(ctx context.Context, input CreateAPITokenInput) (*APIToken, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*APIToken, error)
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
	// Revoke tombstones a token within a tenant; returns false if no live
	// token matched (wrong tenant, unknown id, or already revoked).
	Revoke(ctx context.Context, id, tenantID string) (bool, error)
	// RevokeAllForUser tombstones every live token of a principal (used when a
	// member is disabled).
	RevokeAllForUser(ctx context.Context, userID string) error
}

// MemberRepository is the tenant-directory persistence: users (NOT RLS-scoped,
// every statement takes the tenant explicitly) + their grants (RLS-scoped,
// on the request transaction).
type MemberRepository interface {
	// List returns one page of the tenant's members with grants embedded (two
	// statements, never N+1) and the total count for the same filters.
	List(ctx context.Context, args ListMembersArgs) ([]Member, int, error)
	// GetByID returns a member of the given tenant, or nil (unknown id OR a
	// user of another tenant — indistinguishable by design).
	GetByID(ctx context.Context, tenantID, id string) (*Member, error)
	// UpdateNameStatus updates a member of the tenant; false if none matched.
	UpdateNameStatus(ctx context.Context, tenantID, id, name, status string) (bool, error)
	// Activate sets the password, flips status to active and optionally
	// overrides the name; only an invited user matches.
	Activate(ctx context.Context, id, passwordHash string, name *string) (bool, error)
	// InsertGrant writes one grant and returns its id. A scope entity of
	// another tenant fails the composite FK.
	InsertGrant(ctx context.Context, userID, role string, scopeEntityID *string) (string, error)
	// DeleteGrant removes one grant of the user; false if none matched.
	DeleteGrant(ctx context.Context, userID, grantID string) (bool, error)
	DeleteGrantsForUser(ctx context.Context, userID string) error
	// CountActiveTenantAdmins counts ACTIVE HUMAN users of the tenant holding a
	// tenant-wide tenant_admin grant (the last-admin guard).
	CountActiveTenantAdmins(ctx context.Context, tenantID string) (int, error)
	// LockAdminGuard serialises the tenant's admin-guard mutations for the
	// rest of the request transaction, so the read-then-commit last-admin
	// check cannot be raced by a concurrent request.
	LockAdminGuard(ctx context.Context, tenantID string) error
}

// InviteTokenRepository persists invite tokens. Not RLS-scoped (looked up by
// hash before any tenant context).
type InviteTokenRepository interface {
	Create(ctx context.Context, input CreateInviteTokenInput) (*InviteToken, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*InviteToken, error)
	// MarkAccepted stamps accepted_at on a still-unused token; false if the
	// token was already accepted/revoked (single use, race-safe).
	MarkAccepted(ctx context.Context, id string, at time.Time) (bool, error)
	// RevokeUnusedForUser tombstones every outstanding token of a user.
	RevokeUnusedForUser(ctx context.Context, userID string) error
}

// MemberUseCases is the tenant-admin member surface + the public accept-invite
// step. Mutations are gated by member:manage, reads by member:read, at the
// route layer.
type MemberUseCases interface {
	ListMembers(ctx context.Context, kind, status, search string, limit, offset int) ([]Member, int, error)
	GetMember(ctx context.Context, id string) (*Member, error)
	Invite(ctx context.Context, input CreateMemberInput) (*InviteResult, error)
	ReissueInvite(ctx context.Context, memberID string) (*InviteResult, error)
	UpdateMember(ctx context.Context, memberID string, input UpdateMemberInput) (*Member, error)
	SetRole(ctx context.Context, memberID string, input GrantInput) (*Member, error)
	AddGrant(ctx context.Context, memberID string, input GrantInput) (*Member, error)
	RemoveGrant(ctx context.Context, memberID, grantID string) error
	AcceptInvite(ctx context.Context, input AcceptInviteInput) (*AcceptInviteResult, error)
}

// GrantWriter writes one RBAC grant (user_grants is RLS-scoped; runs on the
// request transaction). Implemented by the identity GrantRepo.
type GrantWriter interface {
	Insert(ctx context.Context, userID, role string, scopeEntityID *string) error
}

// ServiceAccountUseCases is the tenant-admin surface for machine identities.
// All of it is gated by the member:manage capability at the route layer.
type ServiceAccountUseCases interface {
	CreateServiceAccount(ctx context.Context, input CreateServiceAccountInput) (*User, error)
	ListServiceAccounts(ctx context.Context) ([]User, error)
	IssueToken(ctx context.Context, serviceAccountID, label string, ttlDays int) (*IssueTokenResult, error)
	RevokeToken(ctx context.Context, tokenID string) error
}

// AuthUseCases is the handler-facing auth surface.
type AuthUseCases interface {
	Login(ctx context.Context, input LoginInput) (*LoginResult, error)
	MfaEnroll(ctx context.Context, userID string) (*MfaEnrollResult, error)
	// MfaEnable confirms an enrolment. It takes the session TOKEN rather than a
	// user id because its route carries no ambient transaction (a budget charged
	// inside one would be rolled back with the 401 it answers), so the session
	// is resolved here instead of by the auth middleware.
	MfaEnable(ctx context.Context, sessionToken, code string) error
	MfaVerify(ctx context.Context, sessionToken, code string) (*LoginResult, error)
	Logout(ctx context.Context, sessionToken string) error
	LogoutAll(ctx context.Context, userID string) error
	Me(ctx context.Context, userID string) (*User, error)
}

// --- use-case contract types ---

type LoginInput struct {
	Email     string
	Password  string
	IP        *string
	UserAgent *string
}

// LoginResult carries the raw session token (for the Set-Cookie) and whether an
// MFA step is still required before the session is usable.
type LoginResult struct {
	SessionToken string
	MFARequired  bool
	User         *User
}

type MfaEnrollResult struct {
	Secret     string // base32 TOTP secret (manual entry)
	OtpauthURL string // otpauth:// URI (QR)
}
