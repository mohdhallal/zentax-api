package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type TaskInstanceRepository interface {
	Create(ctx context.Context, input CreateTaskInstanceInput) (*TaskInstance, error)
	GetById(ctx context.Context, id TaskInstanceID) (*TaskInstance, error)
	Update(ctx context.Context, id TaskInstanceID, input UpdateTaskInstanceInput) (*TaskInstance, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]TaskInstance, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
	// CountByWorkflow reports how many instances already exist for a workflow —
	// used to keep workflow-start idempotent (refuse re-generation).
	CountByWorkflow(ctx context.Context, workflowID string) (int, error)
}

// TaskInstanceUseCases is the read/update surface. Instances are created by
// workflow-start generation, not via a public create endpoint.
type TaskInstanceUseCases interface {
	GetById(ctx context.Context, id TaskInstanceID) (*TaskInstance, error)
	Update(ctx context.Context, id TaskInstanceID, input UpdateTaskInstanceInput) (*TaskInstance, error)
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[TaskInstance], error)
}
