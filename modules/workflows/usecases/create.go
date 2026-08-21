package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Create(ctx context.Context, input domain.CreateWorkflowInput) (*domain.Workflow, error) {
	// Recurring workflows scope to their entity's subtree; a project workflow
	// (no entity) is tenant-level and needs a tenant-wide grant.
	if err := uc.authorizer.EnsureEntityRef(ctx, input.EntityID, authz.WorkflowWrite); err != nil {
		return nil, err
	}
	if input.WorkflowCategory == "" {
		input.WorkflowCategory = "recurring"
	}
	return uc.repo.Create(ctx, input)
}
