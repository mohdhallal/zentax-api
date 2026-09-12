package pg

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// sqlstateAttestedDelete is the SQLSTATE the ADR-0018 database trigger
// (migration 20260912000023) raises when a delete would cascade away approved
// task instances. An entity delete trips it through the workflows it cascades
// into. The use case's own census normally refuses first with a counted
// message; this is the belt to that pair of braces — a delete that slipped past
// it (a concurrent approval between the count and the DELETE) comes back as a
// 409, never a 500.
const sqlstateAttestedDelete = "ZT018"

// CountDependents runs the pre-delete census. An entity that does not exist, or
// one in another tenant (invisible under RLS), counts zero across the board —
// the Delete that follows then reports not-found, as it always did.
func (r *EntityRepo) CountDependents(ctx context.Context, id domain.EntityID) (domain.EntityDependents, error) {
	var dep domain.EntityDependents
	if err := r.DB.GetContext(ctx, &dep, countEntityDependentsSQL, id); err != nil {
		return domain.EntityDependents{}, err
	}
	return dep, nil
}

// QueueBlobReclaim records the storage key of every document version under the
// entity's workflows before the delete cascades the metadata away, and returns
// how many keys were queued. Nothing is removed from object storage here: the
// queue is drained by the ADR-0007 purge job, so a rolled-back delete leaves no
// half-erased evidence behind.
func (r *EntityRepo) QueueBlobReclaim(ctx context.Context, id domain.EntityID) (int, error) {
	result, err := r.DB.ExecContext(ctx, queueEntityBlobReclaimSQL, id)
	if err != nil {
		return 0, err
	}
	queued, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(queued), nil
}

// Delete removes the entity, translating the database guard's refusal into the
// same conflict the use case raises.
func (r *EntityRepo) Delete(ctx context.Context, id domain.EntityID) (bool, error) {
	deleted, err := r.BaseRepo.Delete(ctx, id)
	if err != nil {
		if pgErr := database.IsPgError(err); pgErr != nil && pgErr.Code == sqlstateAttestedDelete {
			return false, apperrors.NewConflict(domain.ErrEntityHasApprovedWork(0, 0))
		}
		return false, err
	}
	return deleted, nil
}
