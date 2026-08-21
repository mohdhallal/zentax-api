package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
)

func (uc *UseCases) Update(ctx context.Context, id domain.TaskInstanceID, input domain.UpdateTaskInstanceInput) (*domain.TaskInstance, error) {
	if input.TaxDataStatus == "" {
		input.TaxDataStatus = "draft"
	}

	ti, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if ti == nil {
		return nil, apperrors.NewNotFound(domain.ErrTaskInstanceNotFound(id))
	}
	return ti, nil
}
