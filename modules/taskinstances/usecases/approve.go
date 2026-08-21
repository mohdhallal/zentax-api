package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Approve finalizes a submitted task instance — recording the approver and
// freezing the record (ADR-0018). Requires task:approve within scope, and
// enforces segregation of duties: the approver must differ from the submitter
// (ADR-0012 — the approver attests to content someone else prepared).
func (uc *UseCases) Approve(ctx context.Context, id domain.TaskInstanceID, actorID string) (*domain.TaskInstance, error) {
	if err := uc.authorizer.EnsureTaskInstance(ctx, id, authz.TaskApprove); err != nil {
		return nil, err
	}

	ti, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if ti == nil {
		return nil, apperrors.NewNotFound(domain.ErrTaskInstanceNotFound(id))
	}
	if !ti.IsPendingApproval() {
		return nil, apperrors.NewConflict(domain.MsgNotPendingApproval)
	}
	if ti.SubmittedBy != nil && *ti.SubmittedBy == actorID {
		return nil, apperrors.NewForbidden(domain.MsgCannotApproveOwn)
	}

	return uc.repo.Approve(ctx, id, actorID)
}
