package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

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

	wf, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}
	if err := uc.audit.Record(ctx, "workflow.updated", "workflow", id, nil); err != nil {
		return nil, err
	}
	return wf, nil
}
