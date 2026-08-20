package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type ObligationTypeRepository interface {
	Create(ctx context.Context, input CreateObligationTypeInput) (*ObligationType, error)
	GetById(ctx context.Context, id ObligationTypeID) (*ObligationType, error)
	Update(ctx context.Context, id ObligationTypeID, input UpdateObligationTypeInput) (*ObligationType, error)
	Delete(ctx context.Context, id ObligationTypeID) (bool, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]ObligationType, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
}

type ObligationTypeUseCases interface {
	Create(ctx context.Context, input CreateObligationTypeInput) (*ObligationType, error)
	GetById(ctx context.Context, id ObligationTypeID) (*ObligationType, error)
	Update(ctx context.Context, id ObligationTypeID, input UpdateObligationTypeInput) (*ObligationType, error)
	Delete(ctx context.Context, id ObligationTypeID) error
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[ObligationType], error)
}
