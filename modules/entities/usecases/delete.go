package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
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
//  3. Audit. The envelope carries what was removed, counted by kind; WHOSE
//     access was revoked with it, by user id and role; and what the entity
//     itself was: the same whitelist an update records, taken from the row on
//     its way out. Counts alone said how much left and nothing about
//     what — and the row is hard-deleted, there is no history table, and
//     resource_id afterwards points at nothing, so the fiscal calendar that
//     decided when every filing beneath this entity was legally due existed
//     nowhere else the moment it committed. (A create envelope is not a
//     substitute: it records the entity as born, not as it stood after any
//     number of edits, and every entity predating the envelope work has an
//     empty one.)
//
// The same refusal is enforced in the database by the BEFORE DELETE trigger on
// workflows (migration 20260912000023), which fires for each workflow this
// delete cascades into — so a path that forgets this use case is guarded too.
func (uc *UseCases) Delete(ctx context.Context, id domain.EntityID) error {
	if err := uc.authorizer.EnsureEntity(ctx, id, authz.EntityWrite); err != nil {
		return err
	}

	before, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return err
	}
	if before == nil {
		return apperrors.NewNotFound(domain.ErrEntityNotFound(id))
	}

	dependents, err := uc.repo.CountDependents(ctx, id)
	if err != nil {
		return err
	}
	if dependents.HasAttestedWork() {
		return apperrors.NewConflict(
			domain.ErrEntityHasApprovedWork(dependents.ApprovedTaskInstances, dependents.Workflows))
	}

	// Read the access before it is gone: the cascade hard-deletes user_grants —
	// no revoked_at, no history table, no trigger — so this is the last instant
	// the grants exist anywhere in the system.
	revoked, err := uc.repo.ScopedGrants(ctx, id)
	if err != nil {
		return err
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
	// Three things answer three different questions and sit side by side in one
	// envelope: the counts say how much left, `revokedGrants` says whose access
	// left, and `fields` says what the row itself was. audit.Changes keys its
	// payload under `fields`, so nothing collides with a count.
	//
	// revokedGrants closes the last silence here. It used to be a bare
	// userGrantsRevoked count — "two grants were revoked", never whose or at
	// which role — deferred to the identity module on the grounds that the rows
	// are not part of the entity's whitelist. But identity never runs on this
	// path: no use case there sees the cascade, and its own comment
	// (usecases/member.go) states the principle this broke — user_grants
	// survives in the trail or nowhere. Recovering a grant by scanning earlier
	// member.role_changed entries only works for grants made through the grant
	// routes; one made at invite time is invisible there. The rows were already
	// being counted here, one predicate away from being read.
	details := dependents.AuditDetails(queued, revoked)
	for name, change := range audit.Changes(auditValues(before), nil) {
		details[name] = change
	}
	return uc.audit.Record(ctx, "entity.deleted", "entity", id, details)
}
