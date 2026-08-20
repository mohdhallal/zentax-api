package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type EntityObligationRepository interface {
	Create(ctx context.Context, input CreateEntityObligationInput) (*EntityObligation, error)
	GetById(ctx context.Context, id EntityObligationID) (*EntityObligation, error)
	Update(ctx context.Context, id EntityObligationID, input UpdateEntityObligationInput) (*EntityObligation, error)
	Delete(ctx context.Context, id EntityObligationID) (bool, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]EntityObligation, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
}

type EntityObligationUseCases interface {
	Create(ctx context.Context, input CreateEntityObligationInput) (*EntityObligation, error)
	GetById(ctx context.Context, id EntityObligationID) (*EntityObligation, error)
	Update(ctx context.Context, id EntityObligationID, input UpdateEntityObligationInput) (*EntityObligation, error)
	Delete(ctx context.Context, id EntityObligationID) error
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[EntityObligation], error)
}
