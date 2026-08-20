package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
)

type ExternalAuth struct {
	repo domain.NexusAccountAPIKeyRepo
}

func NewExternalAuth(repo domain.NexusAccountAPIKeyRepo) *ExternalAuth {
	return &ExternalAuth{repo: repo}
}

func (uc *ExternalAuth) StoreExternalAPIKey(ctx context.Context, input domain.StoreNexusAPIKeyInput) (*domain.NexusAccountAPIKey, error) {
	return uc.repo.Store(ctx, input)
}

func (uc *ExternalAuth) GetByNexusAccountID(ctx context.Context, nexusAccountID int) (*domain.NexusAccountAPIKey, error) {
	record, err := uc.repo.GetByNexusAccountID(ctx, nexusAccountID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, apperrors.NewNotFound(domain.ErrAPIKeyNotFound(nexusAccountID))
	}
	return record, nil
}
