package pg

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.UserRepository = (*UserRepo)(nil)

const userColumns = `id, tenant_id, email, name, kind, password_hash, status, totp_secret_enc, ` +
	`totp_enabled, failed_login_attempts, locked_until, created_at, updated_at`

// UserRepo persists users. Users are NOT RLS-scoped (login queries by email
// before any tenant context), so callers must scope by tenant_id explicitly.
type UserRepo struct {
	db database.ExecerPg
}

func NewUserRepo(db database.ExecerPg) *UserRepo {
	return &UserRepo{db: db}
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return r.getOne(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1 LIMIT 1`, email)
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return r.getOne(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1 LIMIT 1`, id)
}

func (r *UserRepo) getOne(ctx context.Context, query string, arg any) (*domain.User, error) {
	var u domain.User
	if err := r.db.GetContext(ctx, &u, query, arg); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // nil,nil means not found
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, input domain.CreateUserInput) (*domain.User, error) {
	status := input.Status
	if status == "" {
		status = "active"
	}
	kind := input.Kind
	if kind == "" {
		kind = domain.KindHuman
	}
	var u domain.User
	err := r.db.GetContext(ctx, &u,
		`INSERT INTO users (tenant_id, email, name, kind, password_hash, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+userColumns,
		input.TenantID, input.Email, input.Name, kind, input.PasswordHash, status)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) ListByKind(ctx context.Context, tenantID, kind string) ([]domain.User, error) {
	var users []domain.User
	err := r.db.SelectContext(ctx, &users,
		`SELECT `+userColumns+` FROM users WHERE tenant_id = $1 AND kind = $2 ORDER BY created_at DESC`,
		tenantID, kind)
	return users, err
}

// ConsumeLoginAttempt spends one attempt from the account's budget and reports
// whether this attempt was inside it. It applies the whole lockout policy
// (stated in modules/identity/usecases/login.go) in ONE statement that both
// updates the row and returns the decision, so the counter, the lock and the
// answer can never disagree and concurrent attempts cannot lose a count.
//
// ONE STATEMENT IS THE POINT. The caller must not read the row, decide, and
// write afterwards: between the read and the write, every other request in
// flight reads the same pre-threshold row and is admitted too, so the budget
// grows with the attacker's concurrency instead of bounding it. Here the inner
// SELECT takes the row lock (FOR UPDATE), which serialises concurrent attempts
// on the same account — under READ COMMITTED each waiter re-reads the version
// the one before it committed — and the decision is computed from that locked
// version, in the same statement that updates it.
//
// The inner SELECT also reduces the row to what the policy needs, with an
// EXPIRED lock already erased:
//
//	live_lock = false, attempts = 0, locked_until = NULL   once the window has elapsed
//	live_lock,         attempts, locked_until as stored    otherwise
//
// Erasing the expired stamp is what makes an elapsed window clear the debt: the
// next attempt after the window starts a fresh count of 1, instead of a
// saturated counter leaving "attempts + 1 >= threshold" true forever — which
// let one wrong password per window re-lock a known address indefinitely.
//
// The outer UPDATE then:
//
//   - LIVE LOCK — leaves every column exactly as it found it (counter, stamp
//     and updated_at alike; users has no updated_at trigger, so preserving it
//     here is what preserves it) and returns allowed = false. A lock is never
//     slid by the attempts it is refusing, so guessing cannot lengthen its own
//     refusal. The row IS still rewritten, which is deliberate: skipping the
//     write would make a refused attempt measurably cheaper than a charged one
//     and hand back a locked/unlocked timing signal. The cost is one dead tuple
//     per refused attempt on `users` — negligible at the policy's own rate, and
//     ordinary autovacuum work under a sustained attack on one address.
//   - OTHERWISE — counts the attempt, saturating at the threshold (past it the
//     number means nothing, and the column is a SMALLINT), stamps lockUntil on
//     the attempt that REACHES the threshold, and returns allowed = true. The
//     threshold-th attempt is still inside the budget: it is the one that arms
//     the lock, not the first one refused by it.
//
// "now" is the caller's clock — the one that also produced lockUntil — so
// expiry is judged against the clock that wrote the stamp.
//
// It runs on its own connection in autocommit (the login route deliberately has
// no ambient request transaction), which is exactly the "short transaction" this
// needs: one round trip, committed before the caller spends ~70ms verifying a
// password, so no row lock is ever held across the verification. That connection
// carries no app.tenant_id GUC, which is fine: users is deliberately not
// RLS-scoped (login resolves a user before any tenant context exists). If that
// ever changes, this statement needs a tenant-bound transaction of its own.
func (r *UserRepo) ConsumeLoginAttempt(
	ctx context.Context, id string, lockThreshold int, now, lockUntil time.Time,
) (bool, error) {
	var allowed bool
	err := r.db.GetContext(ctx, &allowed,
		`UPDATE users
		    SET failed_login_attempts = CASE
		            WHEN base.live_lock THEN base.attempts
		            ELSE LEAST(base.attempts + 1, $2)
		        END,
		        locked_until = CASE
		            WHEN base.live_lock THEN base.locked_until
		            WHEN base.attempts + 1 >= $2 THEN $4
		            ELSE NULL
		        END,
		        updated_at = CASE WHEN base.live_lock THEN users.updated_at ELSE NOW() END
		   FROM (
		        SELECT id,
		               (locked_until IS NOT NULL AND locked_until > $3) AS live_lock,
		               CASE WHEN locked_until IS NOT NULL AND locked_until <= $3
		                    THEN 0 ELSE failed_login_attempts END AS attempts,
		               CASE WHEN locked_until IS NOT NULL AND locked_until <= $3
		                    THEN NULL ELSE locked_until END AS locked_until
		          FROM users WHERE id = $1 FOR UPDATE
		   ) AS base
		  WHERE users.id = base.id
		  RETURNING NOT base.live_lock AS allowed`,
		id, lockThreshold, now, lockUntil)
	if errors.Is(err, sql.ErrNoRows) {
		// The row went away between the lookup and here (a deleted member).
		// No account, no budget to spend: refuse, without an error the caller
		// would have to log.
		return false, nil
	}
	return allowed, err
}

func (r *UserRepo) ResetFailedLogin(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET failed_login_attempts = 0, locked_until = NULL, updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *UserRepo) SetTOTP(ctx context.Context, id string, secretEnc *string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET totp_secret_enc = $2, totp_enabled = $3, updated_at = NOW() WHERE id = $1`,
		id, secretEnc, enabled)
	return err
}
