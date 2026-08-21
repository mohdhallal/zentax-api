package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

var _ domain.TaskInstanceRepository = (*TaskInstanceRepo)(nil)

// TaskInstanceRepo persists task instances. RLS (ADR-0004) isolates by tenant;
// the DATE columns round-trip through dateonly.Date (ADR-0002).
type TaskInstanceRepo struct {
	baserepo.BaseRepo[domain.TaskInstance, domain.TaskInstanceID]
}

func NewTaskInstanceRepo(db database.ExecerPg) *TaskInstanceRepo {
	return &TaskInstanceRepo{
		BaseRepo: baserepo.NewBaseRepo[domain.TaskInstance, domain.TaskInstanceID](db, sqlConfig),
	}
}

func (r *TaskInstanceRepo) Create(ctx context.Context, input domain.CreateTaskInstanceInput) (*domain.TaskInstance, error) {
	return r.QueryRow(ctx, r.SQL.Create,
		input.WorkflowID, input.WorkflowTaskID, input.PeriodCode, input.Name, input.Description,
		input.TaskType, input.DueDate, input.PeriodEndDate, input.FilingDeadline,
		input.ApprovalRequired, input.OrderIndex, input.DataTemplateID,
	)
}

func (r *TaskInstanceRepo) Update(ctx context.Context, id domain.TaskInstanceID, input domain.UpdateTaskInstanceInput) (*domain.TaskInstance, error) {
	return r.QueryRow(ctx, r.SQL.Update,
		id, input.Status, input.AssigneeID, input.Notes, input.TaxData, input.TaxDataStatus,
	)
}

func (r *TaskInstanceRepo) CountByWorkflow(ctx context.Context, workflowID string) (int, error) {
	var count int
	err := r.DB.GetContext(ctx, &count,
		`SELECT COUNT(*)::int FROM task_instances WHERE workflow_id = $1`, workflowID)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (r *TaskInstanceRepo) SubmitForApproval(ctx context.Context, id domain.TaskInstanceID, submittedBy string) (*domain.TaskInstance, error) {
	return r.QueryRow(ctx, submitForApprovalSQL, id, submittedBy)
}

func (r *TaskInstanceRepo) Approve(ctx context.Context, id domain.TaskInstanceID, approvedBy string) (*domain.TaskInstance, error) {
	return r.QueryRow(ctx, approveSQL, id, approvedBy)
}

func (r *TaskInstanceRepo) Reject(ctx context.Context, id domain.TaskInstanceID, reason *string) (*domain.TaskInstance, error) {
	return r.QueryRow(ctx, rejectSQL, id, reason)
}
