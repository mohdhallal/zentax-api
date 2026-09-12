package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Delete removes a workflow task — unless doing so would destroy attested
// evidence.
//
// This is the quietest of the three delete paths and the most dangerous-looking
// in a UI: "remove a step from the template" reads like an edit, but
// task_instances.workflow_task_id is NOT NULL and cascades, so the delete takes
// every instance ever generated from that step, approved ones included.
//
// So the census runs first (ADR-0018: an approved instance is attested
// evidence, 409 with a count), and the audit envelope carries how many
// instances a permitted delete removed. There is no blob reclaim here: a
// document attached to one of those instances is unlinked (task_instance_id
// SET NULL), never deleted, so this path cannot orphan an object.
//
// Workflow tasks carry no status column, so there is no "archive this step" to
// offer — the way to retire the work is to archive the workflow that owns it.
// The same refusal is enforced by a BEFORE DELETE trigger in the database
// (migration 20260912000023).
func (uc *UseCases) Delete(ctx context.Context, id domain.WorkflowTaskID) error {
	if err := uc.authorizer.EnsureWorkflowTask(ctx, id, authz.WorkflowTaskWrite); err != nil {
		return err
	}

	dependents, err := uc.repo.CountDependents(ctx, id)
	if err != nil {
		return err
	}
	if dependents.HasAttestedWork() {
		return apperrors.NewConflict(
			domain.ErrWorkflowTaskHasApprovedWork(dependents.ApprovedTaskInstances))
	}

	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrWorkflowTaskNotFound(id))
	}
	return uc.audit.Record(ctx, "workflow_task.deleted", "workflow_task", id, dependents.AuditDetails())
}
