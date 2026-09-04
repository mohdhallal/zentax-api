package pg

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.TokenRepository = (*TokenRepo)(nil)

const tokenColumns = `id, token_hash, user_id, tenant_id, label, created_by, created_at, ` +
	`expires_at, last_used_at, revoked_at`

// TokenRepo persists API tokens (machine credentials). NOT RLS-scoped — looked
// up by hash before tenant context exists — so tenant-sensitive statements
// scope by tenant_id explicitly.
type TokenRepo struct {
	db database.ExecerPg
}

func NewTokenRepo(db database.ExecerPg) *TokenRepo {
	return &TokenRepo{db: db}
}

func (r *TokenRepo) Create(ctx context.Context, input domain.CreateAPITokenInput) (*domain.APIToken, error) {
	var t domain.APIToken
	err := r.db.GetContext(ctx, &t,
		`INSERT INTO api_tokens (token_hash, user_id, tenant_id, label, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+tokenColumns,
		input.TokenHash, input.UserID, input.TenantID, input.Label, input.CreatedBy, input.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TokenRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.APIToken, error) {
	var t domain.APIToken
	err := r.db.GetContext(ctx, &t,
		`SELECT `+tokenColumns+` FROM api_tokens WHERE token_hash = $1 LIMIT 1`, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil means not found
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TokenRepo) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = $2 WHERE id = $1`, id, at)
	return err
}

func (r *TokenRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

func (r *TokenRepo) Revoke(ctx context.Context, id, tenantID string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = NOW() WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL`,
		id, tenantID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
