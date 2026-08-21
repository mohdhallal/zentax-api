package pg

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

var _ domain.WorkflowRepository = (*WorkflowRepo)(nil)

// WorkflowRepo persists workflows. RLS (ADR-0004) isolates by tenant and scopes
// the entity_id / obligation_type_id foreign keys to the same tenant, so a
// cross-tenant reference surfaces as a validation error, not a leak.
type WorkflowRepo struct {
	baserepo.BaseRepo[domain.Workflow, domain.WorkflowID]
}

func NewWorkflowRepo(db database.ExecerPg) *WorkflowRepo {
	return &WorkflowRepo{
		BaseRepo: baserepo.NewBaseRepo[domain.Workflow, domain.WorkflowID](db, sqlConfig),
	}
}

func (r *WorkflowRepo) Create(ctx context.Context, input domain.CreateWorkflowInput) (*domain.Workflow, error) {
	wf, err := r.QueryRow(ctx, r.SQL.Create,
		input.Name, input.Description, input.WorkflowCategory, input.ProjectType,
		input.FinancialYear, input.Periodicity, input.SelectedPeriods, input.EntityID,
		input.ObligationTypeID, input.DueDateRule, input.StartDate, input.EndDate, input.TasksSequential,
	)
	if err != nil {
		if database.IsForeignKeyViolation(err) {
			return nil, apperrors.NewValidation(domain.ErrWorkflowRefNotFound())
		}
		return nil, err
	}
	return wf, nil
}

func (r *WorkflowRepo) Update(ctx context.Context, id domain.WorkflowID, input domain.UpdateWorkflowInput) (*domain.Workflow, error) {
	wf, err := r.QueryRow(ctx, r.SQL.Update,
		id, input.Name, input.Description, input.WorkflowCategory, input.ProjectType,
		input.FinancialYear, input.Periodicity, input.SelectedPeriods, input.EntityID,
		input.ObligationTypeID, input.DueDateRule, input.StartDate, input.EndDate,
		input.TasksSequential, input.Status,
	)
	if err != nil {
		if database.IsForeignKeyViolation(err) {
			return nil, apperrors.NewValidation(domain.ErrWorkflowRefNotFound())
		}
		return nil, err
	}
	return wf, nil
}
