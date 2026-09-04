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

	// assigneeId must be an ACTIVE HUMAN member of the caller's tenant (the
	// column carries no FK — users is not RLS-scoped — so it is checked here).
	// Only a CHANGE of assignee is checked: clients send the current assignee
	// back on every save, and a task whose assignee was since disabled must
	// stay editable (and reassignable).
	if input.AssigneeID != nil && uc.assignees != nil &&
		(current.AssigneeID == nil || *current.AssigneeID != *input.AssigneeID) {
		ok, err := uc.assignees.IsAssignable(ctx, *input.AssigneeID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, apperrors.NewValidation(domain.MsgAssigneeNotMember)
		}
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
	if err := uc.audit.Record(ctx, "task_instance.updated", "task_instance", id,
		map[string]any{"status": ti.Status}); err != nil {
		return nil, err
	}
	return ti, nil
}
