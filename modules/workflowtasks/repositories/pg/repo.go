package pg

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

var _ domain.WorkflowTaskRepository = (*WorkflowTaskRepo)(nil)

// WorkflowTaskRepo persists workflow task templates. RLS (ADR-0004) isolates by
// tenant and scopes the workflow_id FK to the same tenant, so referencing
// another tenant's workflow fails the FK check → surfaced as a validation error.
type WorkflowTaskRepo struct {
	baserepo.BaseRepo[domain.WorkflowTask, domain.WorkflowTaskID]
}

func NewWorkflowTaskRepo(db database.ExecerPg) *WorkflowTaskRepo {
	cfg := sqlConfig
	cfg.ReadScope = readScope(db)
	return &WorkflowTaskRepo{
		BaseRepo: baserepo.NewBaseRepo[domain.WorkflowTask, domain.WorkflowTaskID](db, cfg),
	}
}

func (r *WorkflowTaskRepo) Create(ctx context.Context, input domain.CreateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	wt, err := r.QueryRow(ctx, r.SQL.Create,
		input.WorkflowID, input.Name, input.Description, input.TaskType, input.RoleLabel,
		input.ApprovalRequired, input.DueDateReference, input.DueDateOffsetValue, input.DueDateOffsetUnit,
		input.DueDateOffsetDirection, input.OrderIndex, input.DataTemplateID, input.RequiredDocuments,
	)
	if err != nil {
		return nil, mapFKViolation(err)
	}
	return wt, nil
}

// dataTemplateFK is the composite (tenant_id, data_template_id) constraint
// (migration 20260904000018) — the only other FK a template row carries.
const dataTemplateFK = "workflow_tasks_data_template_fk"

// mapFKViolation turns a composite-FK failure into the matching validation
// error: the workflow or the data template is not in this tenant (FK checks
// bypass RLS; the composite key is what refuses the cross-tenant reference).
func mapFKViolation(err error) error {
	if !database.IsForeignKeyViolation(err) {
		return err
	}
	if database.GetConstraintName(err) == dataTemplateFK {
		return apperrors.NewValidation(domain.ErrDataTemplateNotFound())
	}
	return apperrors.NewValidation(domain.ErrWorkflowNotFound())
}

func (r *WorkflowTaskRepo) ListByWorkflow(ctx context.Context, workflowID string) ([]domain.WorkflowTask, error) {
	var tasks []domain.WorkflowTask
	query := `SELECT ` + workflowTaskColumns + ` FROM workflow_tasks WHERE workflow_id = $1 ORDER BY order_index ASC`
	if err := r.DB.SelectContext(ctx, &tasks, query, workflowID); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *WorkflowTaskRepo) Update(ctx context.Context, id domain.WorkflowTaskID, input domain.UpdateWorkflowTaskInput) (*domain.WorkflowTask, error) {
	// workflow_id is fixed at creation; data_template_id is the one FK an
	// update can newly violate.
	wt, err := r.QueryRow(ctx, r.SQL.Update,
		id, input.Name, input.Description, input.TaskType, input.RoleLabel,
		input.ApprovalRequired, input.DueDateReference, input.DueDateOffsetValue, input.DueDateOffsetUnit,
		input.DueDateOffsetDirection, input.OrderIndex, input.DataTemplateID, input.RequiredDocuments,
	)
	if err != nil {
		return nil, mapFKViolation(err)
	}
	return wt, nil
}
