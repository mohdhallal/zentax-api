package domain

import (
	"context"

	"github.com/mohamadhallal/zentax-api/shared/deadline"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type EntityRepository interface {
	Create(ctx context.Context, input CreateEntityInput) (*Entity, error)
	GetById(ctx context.Context, id EntityID) (*Entity, error)
	Update(ctx context.Context, id EntityID, input UpdateEntityInput) (*Entity, error)
	// CountDependents counts, in one round trip, everything a delete of this
	// entity would cascade away (and the child entities it would detach). An
	// entity that does not exist counts zero.
	CountDependents(ctx context.Context, id EntityID) (EntityDependents, error)
	// QueueBlobReclaim copies the storage key of every document version under
	// the entity's workflows into the reclaim queue and returns how many were
	// queued, so the purge job can still reach objects whose metadata rows the
	// delete is about to cascade away. Runs on the delete's own transaction.
	QueueBlobReclaim(ctx context.Context, id EntityID) (int, error)
	// Delete removes the entity. A database-level refusal of a delete that
	// would destroy approved work (ADR-0018) surfaces as a conflict error.
	Delete(ctx context.Context, id EntityID) (bool, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]Entity, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
}

type EntityUseCases interface {
	Create(ctx context.Context, input CreateEntityInput) (*Entity, error)
	GetById(ctx context.Context, id EntityID) (*Entity, error)
	Update(ctx context.Context, id EntityID, input UpdateEntityInput) (*Entity, error)
	Delete(ctx context.Context, id EntityID) error
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[Entity], error)
	// Periods lists the entity's reporting periods for a periodicity and
	// fiscal year from the same calendar engine the generator uses
	// (ADR-0023): an unknown entity is a not-found error, a combination the
	// engine cannot compute a validation error.
	Periods(ctx context.Context, id EntityID, periodicity string, financialYear int) ([]deadline.Period, error)
}
