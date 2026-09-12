package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Delete removes an entity — unless doing so would destroy attested evidence.
//
// An entity sits at the top of the cascade: workflows hang off it ON DELETE
// CASCADE, and beneath them workflow_tasks, task_instances, documents and
// document_versions. Its obligations and the RBAC grants scoped to it go too;
// its child entities are unparented rather than removed. Before this guard, one
// DELETE on an entity erased every approved filing in its group and the audit
// trail recorded nothing but "entity.deleted" with empty details.
//
// Three things happen here, in this order and on one transaction:
//
//  1. Census. ADR-0018 makes an approved task instance attested evidence, so a
//     delete that would take one with it is refused (409) with a message naming
//     what is in the way, how many, and the alternative the domain already has:
//     archive the entity (status: archived).
//  2. Blob reclaim. If the delete is allowed and documents go with it, their
//     storage keys are queued for the ADR-0007 purge job first — the metadata
//     rows are about to vanish and the objects would otherwise be unreachable
//     forever. Queueing rather than deleting the objects here keeps the whole
//     operation rollback-safe: a failure later in the transaction takes the
//     queue rows with it, and no byte has been touched.
//  3. Audit. The envelope carries what was removed, counted by kind, so the
//     trail is no longer silent about the cascade.
//
// The same refusal is enforced in the database by the BEFORE DELETE trigger on
// workflows (migration 20260912000023), which fires for each workflow this
// delete cascades into — so a path that forgets this use case is guarded too.
func (uc *UseCases) Delete(ctx context.Context, id domain.EntityID) error {
	if err := uc.authorizer.EnsureEntity(ctx, id, authz.EntityWrite); err != nil {
		return err
	}

	dependents, err := uc.repo.CountDependents(ctx, id)
	if err != nil {
		return err
	}
	if dependents.HasAttestedWork() {
		return apperrors.NewConflict(
			domain.ErrEntityHasApprovedWork(dependents.ApprovedTaskInstances, dependents.Workflows))
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
		return apperrors.NewNotFound(domain.ErrEntityNotFound(id))
	}
	return uc.audit.Record(ctx, "entity.deleted", "entity", id, dependents.AuditDetails(queued))
}
