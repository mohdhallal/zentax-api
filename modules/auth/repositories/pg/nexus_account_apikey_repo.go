package pg

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.NexusAccountAPIKeyRepo = (*NexusAccountAPIKeyRepo)(nil)

type NexusAccountAPIKeyRepo struct {
	db database.ExecerPg
}

func NewNexusAccountAPIKeyRepo(db database.ExecerPg) *NexusAccountAPIKeyRepo {
	return &NexusAccountAPIKeyRepo{db: db}
}

func (r *NexusAccountAPIKeyRepo) Store(ctx context.Context, input domain.StoreNexusAPIKeyInput) (*domain.NexusAccountAPIKey, error) {
	var record domain.NexusAccountAPIKey
	err := r.db.GetContext(ctx, &record, nexusAccountAPIKeySQL.Store, input.NexusAccountID, input.APIKey, input.APISecret)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *NexusAccountAPIKeyRepo) GetByNexusAccountID(ctx context.Context, nexusAccountID int) (*domain.NexusAccountAPIKey, error) {
	var record domain.NexusAccountAPIKey
	err := r.db.GetContext(ctx, &record, nexusAccountAPIKeySQL.GetByNexusAccountID, nexusAccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // nil,nil means not found
		}
		return nil, err
	}
	return &record, nil
}
