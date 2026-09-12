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

const sessionColumns = `id, token_hash, user_id, tenant_id, mfa_pending, mfa_attempts, created_at, ` +
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

// CompleteMFA also zeroes mfa_attempts: a code that verifies clears the budget
// its wrong predecessors spent, in the same statement that completes the
// session, so the two can never disagree.
func (r *SessionRepo) CompleteMFA(ctx context.Context, id, newTokenHash string, idleExpiresAt, absoluteExpiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET mfa_pending = false, mfa_attempts = 0, token_hash = $2,
		        idle_expires_at = $3, absolute_expires_at = $4
		  WHERE id = $1`,
		id, newTokenHash, idleExpiresAt, absoluteExpiresAt)
	return err
}

// ConsumeMFAAttempt spends one attempt from the session's second-factor budget
// and reports whether this attempt was inside it, plus the count after the
// charge. It applies the whole policy (stated in
// modules/identity/usecases/mfa.go) in ONE statement that both updates the row
// and returns the decision, so the counter, the answer and the destruction that
// follows can never disagree.
//
// ONE STATEMENT IS THE POINT, exactly as in ConsumeLoginAttempt. The caller must
// not read the session, decide, and write afterwards: between the read and the
// write every other request in flight reads the same pre-threshold row and is
// admitted too, so the budget would grow with the attacker's concurrency instead
// of bounding it — and a six-digit code is worth brute-forcing precisely because
// it is cheap to try many at once. The inner SELECT takes the row lock
// (FOR UPDATE), which serialises concurrent attempts on the same session, and
// the decision is computed from that locked version in the same statement that
// updates it.
//
// The outer UPDATE then:
//
//   - BELOW THE THRESHOLD — counts the attempt and returns allowed = true. The
//     attempt that REACHES the threshold is still inside the budget: it is the
//     last try, not the first refusal.
//   - AT THE THRESHOLD — leaves the (already saturated) counter alone and
//     returns allowed = false. The row is still rewritten so that a refused
//     attempt costs the same write a charged one does, rather than answering
//     measurably faster and handing back a "budget spent" timing signal. The
//     cost is one dead tuple per refused attempt, which is bounded anyway: the
//     use case destroys the session the moment the budget is spent, so these
//     writes only happen to a session that is already dead.
//
// A session row that has gone (partition pruned, user deleted) returns
// allowed = false: no session, no budget to spend.
func (r *SessionRepo) ConsumeMFAAttempt(
	ctx context.Context, id string, threshold int,
) (bool, int, error) {
	var out struct {
		Allowed bool `db:"allowed"`
		Spent   int  `db:"spent"`
	}
	err := r.db.GetContext(ctx, &out,
		`UPDATE sessions
		    SET mfa_attempts = LEAST(base.mfa_attempts + 1, $2)
		   FROM (
		        SELECT id, created_at, mfa_attempts FROM sessions WHERE id = $1 FOR UPDATE
		   ) AS base
		  WHERE sessions.id = base.id AND sessions.created_at = base.created_at
		  RETURNING base.mfa_attempts < $2 AS allowed, LEAST(base.mfa_attempts + 1, $2) AS spent`,
		id, threshold)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return out.Allowed, out.Spent, nil
}

func (r *SessionRepo) ClearMFAAttempts(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sessions SET mfa_attempts = 0 WHERE id = $1`, id)
	return err
}

// Revoke stamps revoked_at once and never moves it: a session destroyed by a
// spent MFA budget must not have its timestamp slid by the attempts that keep
// arriving after it.

func (r *SessionRepo) Revoke(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

func (r *SessionRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}
