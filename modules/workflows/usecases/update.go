package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Update rewrites the workflow — and records what the rewrite replaced.
//
// The prior row is read on the same transaction as the write (ADR-0008: the
// mutation and its evidence commit or roll back together). Nothing else keeps
// it: no history table stands behind workflows, so the status a workflow held
// and the due-date rule that dated its task instances are recoverable only
// from this envelope.
func (uc *UseCases) Update(ctx context.Context, id domain.WorkflowID, input domain.UpdateWorkflowInput) (*domain.Workflow, error) {
	if err := uc.authorizer.EnsureWorkflow(ctx, id, authz.WorkflowWrite); err != nil {
		return nil, err
	}
	if input.WorkflowCategory == "" {
		input.WorkflowCategory = "recurring"
	}
	if input.Status == "" {
		input.Status = "draft"
	}

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}

	wf, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}
	if err := uc.audit.Record(ctx, "workflow.updated", "workflow", id,
		audit.Changes(auditValues(before), auditValues(wf))); err != nil {
		return nil, err
	}
	return wf, nil
}
