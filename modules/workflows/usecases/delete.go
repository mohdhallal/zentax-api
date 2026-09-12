package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Delete removes a workflow — unless doing so would destroy attested evidence.
//
// The workflow chain cascades: task_instances, workflow_tasks, documents and
// document_versions all hang off the workflow ON DELETE CASCADE. Before this
// guard, one DELETE erased every approved filing beneath a workflow and the
// audit trail recorded nothing but "workflow.deleted" with empty details.
//
// Three things happen here, in this order and on one transaction:
//
//  1. Census. ADR-0018 makes an approved task instance attested evidence, so a
//     delete that would take one with it is refused (409) with a message naming
//     what is in the way, how many, and the alternative the domain already has:
//     archive the workflow (status: archived).
//  2. Blob reclaim. If the delete is allowed and documents go with it, their
//     storage keys are queued for the ADR-0007 purge job first — the metadata
//     rows are about to vanish and the objects would otherwise be unreachable
//     forever. Queueing rather than deleting the objects here keeps the whole
//     operation rollback-safe: a failure later in the transaction takes the
//     queue rows with it, and no byte has been touched.
//  3. Audit. The envelope carries what was removed, counted by kind, so the
//     trail is no longer silent about the cascade.
//
// The same refusal is enforced by a BEFORE DELETE trigger in the database
// (migration 20260912000023), which is what protects a path that forgets this
// use case entirely.
func (uc *UseCases) Delete(ctx context.Context, id domain.WorkflowID) error {
	if err := uc.authorizer.EnsureWorkflow(ctx, id, authz.WorkflowWrite); err != nil {
		return err
	}

	dependents, err := uc.repo.CountDependents(ctx, id)
	if err != nil {
		return err
	}
	if dependents.HasAttestedWork() {
		return apperrors.NewConflict(
			domain.ErrWorkflowHasApprovedWork(dependents.ApprovedTaskInstances))
	}

	queued, err := uc.repo.QueueBlobReclaim(ctx, id)
	if err != nil {
		return err
	}

	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrWorkflowNotFound(id))
	}
	return uc.audit.Record(ctx, "workflow.deleted", "workflow", id, dependents.AuditDetails(queued))
}
