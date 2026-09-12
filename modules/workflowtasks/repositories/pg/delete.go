package pg

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// sqlstateAttestedDelete is the SQLSTATE the ADR-0018 database trigger
// (migration 20260912000023) raises when a delete would cascade away approved
// task instances. The use case's own census normally refuses first with a
// counted message; this is the belt to that pair of braces — a delete that
// slipped past it (a concurrent approval between the count and the DELETE)
// comes back as a 409, never a 500.
const sqlstateAttestedDelete = "ZT018"

// countWorkflowTaskDependentsSQL is the pre-delete census: the instances
// generated from this template row, and how many of them are approved.
// tenant_id is not named — RLS scopes the table to the caller's tenant
// (ADR-0004). No document counts: a document attached to one of these
// instances is unlinked (task_instance_id SET NULL), never deleted, so no blob
// can be orphaned by this path and there is nothing to queue for reclaim.
const countWorkflowTaskDependentsSQL = `
	SELECT
		(SELECT COUNT(*)::int FROM task_instances
		  WHERE workflow_task_id = $1 AND approved_at IS NOT NULL) AS approved_task_instances,
		(SELECT COUNT(*)::int FROM task_instances
		  WHERE workflow_task_id = $1)                             AS task_instances`

// CountDependents runs the pre-delete census. A template row that does not
// exist, or one in another tenant (invisible under RLS), counts zero — the
// Delete that follows then reports not-found, as it always did.
func (r *WorkflowTaskRepo) CountDependents(
	ctx context.Context, id domain.WorkflowTaskID,
) (domain.WorkflowTaskDependents, error) {
	var dep domain.WorkflowTaskDependents
	if err := r.DB.GetContext(ctx, &dep, countWorkflowTaskDependentsSQL, id); err != nil {
		return domain.WorkflowTaskDependents{}, err
	}
	return dep, nil
}

// Delete removes the template row, translating the database guard's refusal
// into the same conflict the use case raises.
func (r *WorkflowTaskRepo) Delete(ctx context.Context, id domain.WorkflowTaskID) (bool, error) {
	deleted, err := r.BaseRepo.Delete(ctx, id)
	if err != nil {
		if pgErr := database.IsPgError(err); pgErr != nil && pgErr.Code == sqlstateAttestedDelete {
			return false, apperrors.NewConflict(domain.ErrWorkflowTaskHasApprovedWork(0))
		}
		return false, err
	}
	return deleted, nil
}
