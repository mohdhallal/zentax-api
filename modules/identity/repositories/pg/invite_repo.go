package pg

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.InviteTokenRepository = (*InviteRepo)(nil)

const inviteColumns = `id, token_hash, user_id, tenant_id, created_by, created_at, expires_at, accepted_at, revoked_at`

// InviteRepo persists invite tokens. NOT RLS-scoped — a token is looked up by
// hash before any tenant context exists (like api_tokens / sessions); the
// tenant is carried on the row and bound explicitly by the accept flow.
type InviteRepo struct {
	db database.ExecerPg
}

func NewInviteRepo(db database.ExecerPg) *InviteRepo {
	return &InviteRepo{db: db}
}

func (r *InviteRepo) Create(ctx context.Context, input domain.CreateInviteTokenInput) (*domain.InviteToken, error) {
	var t domain.InviteToken
	err := r.db.GetContext(ctx, &t,
		`INSERT INTO invite_tokens (token_hash, user_id, tenant_id, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+inviteColumns,
		input.TokenHash, input.UserID, input.TenantID, input.CreatedBy, input.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *InviteRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.InviteToken, error) {
	var t domain.InviteToken
	err := r.db.GetContext(ctx, &t,
		`SELECT `+inviteColumns+` FROM invite_tokens WHERE token_hash = $1 LIMIT 1`, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil means not found
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *InviteRepo) MarkAccepted(ctx context.Context, id string, at time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE invite_tokens SET accepted_at = $2
		 WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2`, id, at)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *InviteRepo) RevokeUnusedForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE invite_tokens SET revoked_at = NOW()
		 WHERE user_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL`, userID)
	return err
}
