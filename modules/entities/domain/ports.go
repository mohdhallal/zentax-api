package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type EntityRepository interface {
	Create(ctx context.Context, input CreateEntityInput) (*Entity, error)
	GetById(ctx context.Context, id EntityID) (*Entity, error)
	Update(ctx context.Context, id EntityID, input UpdateEntityInput) (*Entity, error)
	Delete(ctx context.Context, id EntityID) (bool, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]Entity, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
}

type EntityUseCases interface {
	Create(ctx context.Context, input CreateEntityInput) (*Entity, error)
	GetById(ctx context.Context, id EntityID) (*Entity, error)
	Update(ctx context.Context, id EntityID, input UpdateEntityInput) (*Entity, error)
	Delete(ctx context.Context, id EntityID) error
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[Entity], error)
}
