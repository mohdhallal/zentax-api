package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// SubmitForApproval moves a task instance to pending_approval and records the
// submitter (ADR-0018 attestation). Requires task:submit within the instance's
// entity scope. Only tasks that require approval, and are not already pending or
// approved, can be submitted.
func (uc *UseCases) SubmitForApproval(ctx context.Context, id domain.TaskInstanceID, actorID string) (*domain.TaskInstance, error) {
	if err := uc.authorizer.EnsureTaskInstance(ctx, id, authz.TaskSubmit); err != nil {
		return nil, err
	}

	ti, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if ti == nil {
		return nil, apperrors.NewNotFound(domain.ErrTaskInstanceNotFound(id))
	}
	if !ti.ApprovalRequired {
		return nil, apperrors.NewValidation(domain.MsgApprovalNotRequired)
	}
	if ti.IsApproved() {
		return nil, apperrors.NewConflict(domain.MsgApprovedImmutable)
	}
	if ti.IsPendingApproval() {
		return nil, apperrors.NewConflict(domain.MsgAlreadyPending)
	}

	return uc.repo.SubmitForApproval(ctx, id, actorID)
}
