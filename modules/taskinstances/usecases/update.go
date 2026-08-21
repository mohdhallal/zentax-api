package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Update(ctx context.Context, id domain.TaskInstanceID, input domain.UpdateTaskInstanceInput) (*domain.TaskInstance, error) {
	if err := uc.authorizer.EnsureTaskInstance(ctx, id, authz.TaskWrite); err != nil {
		return nil, err
	}

	// ADR-0018: a submitted (pending) or approved instance is locked against
	// in-place edits — a change must go through reject/amend, not overwrite.
	current, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, apperrors.NewNotFound(domain.ErrTaskInstanceNotFound(id))
	}
	if current.IsApproved() {
		return nil, apperrors.NewConflict(domain.MsgApprovedImmutable)
	}
	if current.IsPendingApproval() {
		return nil, apperrors.NewConflict(domain.MsgAlreadyPending)
	}

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
