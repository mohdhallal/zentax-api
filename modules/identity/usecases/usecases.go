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

const (
	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute
)

// dummyHash is verified against when a user is not found, so login timing does
// not reveal whether an email exists. Computed once at load.
var dummyHash, _ = crypto.HashPassword("timing-equalizer-not-a-real-password")

type UseCases struct {
	users    domain.UserRepository
	sessions domain.SessionRepository
	tokens   domain.TokenRepository
	grants   domain.GrantWriter
	settings Settings
	now      func() time.Time

	// Member administration (optional wiring; nil-safe for the auth-only tests).
	members domain.MemberRepository
	invites domain.InviteTokenRepository
	tx      database.ExecerPgTx // opens a tenant-bound tx for the public accept-invite step
	audit   *audit.Recorder      // nil = no-op
}

// WithMembers injects the member-administration repositories.
func (uc *UseCases) WithMembers(members domain.MemberRepository, invites domain.InviteTokenRepository) *UseCases {
	uc.members = members
	uc.invites = invites
	return uc
}

// WithTx injects the transaction seam used by AcceptInvite, which runs on a
// public route (no session, no tenant) and must open its own transaction bound
// to the tenant taken from the token row.
func (uc *UseCases) WithTx(tx database.ExecerPgTx) *UseCases {
	uc.tx = tx
	return uc
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
		users: users, sessions: sessions, tokens: tokens, grants: grants,
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
