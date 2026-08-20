package pg

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

type InternalAPIKeyRepo struct {
	db database.ExecerPg
}

func NewInternalAPIKeyRepo(db database.ExecerPg) *InternalAPIKeyRepo {
	return &InternalAPIKeyRepo{db: db}
}

func (r *InternalAPIKeyRepo) GetByKey(ctx context.Context, key uuid.UUID) (*domain.InternalAPIKey, error) {
	var apiKey domain.InternalAPIKey
	err := r.db.GetContext(ctx, &apiKey, internalAPIKeySQL.GetByKey, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // nil,nil means not found
		}
		return nil, err
	}
	return &apiKey, nil
}
