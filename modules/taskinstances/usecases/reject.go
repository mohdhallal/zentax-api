package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Reject sends a submitted task instance back to in_progress with a reason, so
// the preparer can revise and resubmit. Requires task:approve within scope. (No
// self-submission bar: rejecting your own submission to fix it is fine — only
// approval is the attestation that segregation of duties protects.)
func (uc *UseCases) Reject(ctx context.Context, id domain.TaskInstanceID, reason *string) (*domain.TaskInstance, error) {
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

	rejected, err := uc.repo.Reject(ctx, id, reason)
	if err != nil {
		return nil, err
	}
	// reasonProvided only — the reason text is free text and stays out of the
	// PII-free audit envelope (it lives on the row itself).
	if err := uc.audit.Record(ctx, "task_instance.rejected", "task_instance", id,
		map[string]any{"from": domain.StatusPendingApproval, "to": domain.StatusInProgress,
			"reasonProvided": reason != nil}); err != nil {
		return nil, err
	}
	return rejected, nil
}
