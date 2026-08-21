package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func (uc *UseCases) List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[domain.Workflow], error) {
	items, err := uc.repo.List(ctx, args)
	if err != nil {
		return nil, err
	}

	total, err := uc.repo.GetTotal(ctx, args.Filters)
	if err != nil {
		return nil, err
	}

	return &sharedtypes.ListResult[domain.Workflow]{
		Items: items,
		Total: total,
	}, nil
}
