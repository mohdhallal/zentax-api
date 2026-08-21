package pg

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.SessionRepository = (*SessionRepo)(nil)

const sessionColumns = `id, token_hash, user_id, tenant_id, mfa_pending, created_at, ` +
	`idle_expires_at, absolute_expires_at, revoked_at, ip, user_agent`

// SessionRepo persists server-side sessions. NOT RLS-scoped (looked up by token
// hash before any tenant context).
type SessionRepo struct {
	db database.ExecerPg
}

func NewSessionRepo(db database.ExecerPg) *SessionRepo {
	return &SessionRepo{db: db}
}

func (r *SessionRepo) Create(ctx context.Context, input domain.CreateSessionInput) (*domain.Session, error) {
	var s domain.Session
	err := r.db.GetContext(ctx, &s,
		`INSERT INTO sessions (token_hash, user_id, tenant_id, mfa_pending, idle_expires_at, absolute_expires_at, ip, user_agent)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+sessionColumns,
		input.TokenHash, input.UserID, input.TenantID, input.MFAPending,
		input.IdleExpiresAt, input.AbsoluteExpiresAt, input.IP, input.UserAgent)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Session, error) {
	var s domain.Session
	if err := r.db.GetContext(ctx, &s,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = $1 LIMIT 1`, tokenHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // nil,nil means not found
		}
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepo) Touch(ctx context.Context, id string, idleExpiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sessions SET idle_expires_at = $2 WHERE id = $1`, id, idleExpiresAt)
	return err
}

func (r *SessionRepo) CompleteMFA(ctx context.Context, id, newTokenHash string, idleExpiresAt, absoluteExpiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET mfa_pending = false, token_hash = $2, idle_expires_at = $3, absolute_expires_at = $4 WHERE id = $1`,
		id, newTokenHash, idleExpiresAt, absoluteExpiresAt)
	return err
}

func (r *SessionRepo) Revoke(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

func (r *SessionRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}
