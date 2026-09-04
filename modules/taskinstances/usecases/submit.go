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
// approved, can be submitted. With a data template attached, every mandatory
// field must be present (ADR-0001) — the record need not be marked final.
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

	tpl, err := uc.resolveTemplate(ctx, nil, ti.DataTemplateID)
	if err != nil {
		return nil, err
	}
	if tpl != nil {
		if missing := domain.MissingMandatoryFields(tpl, ti.TaxData); len(missing) > 0 {
			return nil, apperrors.NewValidation(domain.ErrMissingMandatory(missing))
		}
	}

	submitted, err := uc.repo.SubmitForApproval(ctx, id, actorID)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "task_instance.submitted", "task_instance", id,
		map[string]any{"from": ti.Status, "to": domain.StatusPendingApproval}); err != nil {
		return nil, err
	}
	return submitted, nil
}
