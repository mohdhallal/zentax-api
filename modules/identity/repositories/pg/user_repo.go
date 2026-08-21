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

func (r *UserRepo) RecordFailedLogin(ctx context.Context, id string, attempts int, lockedUntil *time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET failed_login_attempts = $2, locked_until = $3, updated_at = NOW() WHERE id = $1`,
		id, attempts, lockedUntil)
	return err
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
