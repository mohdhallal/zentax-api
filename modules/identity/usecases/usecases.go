package usecases

import (
	"context"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Settings holds the auth config the use cases need (derived from config.AuthConfig).
type Settings struct {
	EncryptionKey      []byte
	SessionIdleTTL     time.Duration
	SessionAbsoluteTTL time.Duration
	TOTPIssuer         string
}

// dummyHash is the timing equalizer: it is verified on every login path that
// must NOT verify the stored credential — an unknown address, a disabled member,
// an invited one with no password yet, a service account, and (the case that is
// easy to get wrong) an account whose lockout window is still live. Verifying a
// locked account's real hash makes the lock a password oracle; skipping the
// verification entirely forks the timing and enumerates the address. So exactly
// one hash is always checked, and this is the one checked whenever the answer
// must not depend on the result.
//
// Computed once at load, with the same parameters as a real hash. The lockout
// and enumeration policy it serves is stated in login.go.
var dummyHash, _ = crypto.HashPassword("timing-equalizer-not-a-real-password")

type UseCases struct {
	users    domain.UserRepository
	sessions domain.SessionRepository
	tokens   domain.TokenRepository
	grants   domain.GrantWriter
	settings Settings
	now      func() time.Time
	// verifyPassword is crypto.VerifyPassword, injectable like now: the login
	// path's whole enumeration defence is that it is called exactly once on
	// every outcome, which is a property about WORK done and can only be
	// asserted by counting the calls (a wall-clock assertion would be flaky).
	verifyPassword func(password, encoded string) (bool, error)

	// Member administration (optional wiring; nil-safe for the auth-only tests).
	members domain.MemberRepository
	invites domain.InviteTokenRepository
	tx      database.ExecerPgTx // the public /auth routes open their own transactions
	audit   *audit.Recorder     // nil = no-op
}

// WithMembers injects the member-administration repositories.
func (uc *UseCases) WithMembers(members domain.MemberRepository, invites domain.InviteTokenRepository) *UseCases {
	uc.members = members
	uc.invites = invites
	return uc
}

// WithTx injects the transaction seam the public /auth routes need. They carry
// no ambient request transaction, so each opens its own where it needs one:
// AcceptInvite (no session, no tenant — its transaction is bound to the tenant
// taken from the token row), and Login, whose route deliberately declares no
// transaction so that the attempt-budget statement can commit before the ~70ms
// password verification instead of holding a row lock across it.
func (uc *UseCases) WithTx(tx database.ExecerPgTx) *UseCases {
	uc.tx = tx
	return uc
}

// withinTx runs fn on the injected Tx seam; without one (unit tests) it runs
// fn directly on the given context.
func (uc *UseCases) withinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if uc.tx == nil {
		return fn(ctx)
	}
	return uc.tx.WithinTransaction(ctx, fn)
}

// WithAudit injects the audit recorder (ADR-0008). Nil-safe.
func (uc *UseCases) WithAudit(r *audit.Recorder) *UseCases {
	uc.audit = r
	return uc
}

func NewUseCases(
	users domain.UserRepository,
	sessions domain.SessionRepository,
	tokens domain.TokenRepository,
	grants domain.GrantWriter,
	settings Settings,
) *UseCases {
	if settings.TOTPIssuer == "" {
		settings.TOTPIssuer = "ZenTax"
	}
	return &UseCases{
		verifyPassword: crypto.VerifyPassword,
		users:          users, sessions: sessions, tokens: tokens, grants: grants,
		settings: settings, now: time.Now,
	}
}

// createSession mints a session for a user and returns the raw cookie token.
func (uc *UseCases) createSession(
	ctx context.Context, user *domain.User, mfaPending bool, ip, ua *string,
) (string, *domain.Session, error) {
	token, err := crypto.NewSessionToken()
	if err != nil {
		return "", nil, err
	}
	now := uc.now()
	session, err := uc.sessions.Create(ctx, domain.CreateSessionInput{
		TokenHash:         crypto.HashToken(token),
		UserID:            user.ID,
		TenantID:          user.TenantID,
		MFAPending:        mfaPending,
		IdleExpiresAt:     now.Add(uc.settings.SessionIdleTTL),
		AbsoluteExpiresAt: now.Add(uc.settings.SessionAbsoluteTTL),
		IP:                ip,
		UserAgent:         ua,
	})
	if err != nil {
		return "", nil, err
	}
	return token, session, nil
}
