package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type WorkflowRepository interface {
	Create(ctx context.Context, input CreateWorkflowInput) (*Workflow, error)
	GetById(ctx context.Context, id WorkflowID) (*Workflow, error)
	Update(ctx context.Context, id WorkflowID, input UpdateWorkflowInput) (*Workflow, error)
	// CountDependents counts, in one round trip, everything a delete of this
	// workflow would cascade away. A workflow that does not exist counts zero.
	CountDependents(ctx context.Context, id WorkflowID) (WorkflowDependents, error)
	// QueueBlobReclaim copies the storage key of every document version under
	// the workflow into the reclaim queue and returns how many were queued, so
	// the purge job can still reach objects whose metadata rows the delete is
	// about to cascade away. Runs on the delete's own transaction.
	QueueBlobReclaim(ctx context.Context, id WorkflowID) (int, error)
	// Delete removes the workflow. A database-level refusal of a delete that
	// would destroy approved work (ADR-0018) surfaces as a conflict error.
	Delete(ctx context.Context, id WorkflowID) (bool, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]Workflow, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
}

type WorkflowUseCases interface {
	Create(ctx context.Context, input CreateWorkflowInput) (*Workflow, error)
	GetById(ctx context.Context, id WorkflowID) (*Workflow, error)
	Update(ctx context.Context, id WorkflowID, input UpdateWorkflowInput) (*Workflow, error)
	Delete(ctx context.Context, id WorkflowID) error
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[Workflow], error)
}
