package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type WorkflowTaskRepository interface {
	Create(ctx context.Context, input CreateWorkflowTaskInput) (*WorkflowTask, error)
	GetById(ctx context.Context, id WorkflowTaskID) (*WorkflowTask, error)
	Update(ctx context.Context, id WorkflowTaskID, input UpdateWorkflowTaskInput) (*WorkflowTask, error)
	Delete(ctx context.Context, id WorkflowTaskID) (bool, error)
	List(ctx context.Context, args sharedtypes.ListArgs) ([]WorkflowTask, error)
	GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error)
}

type WorkflowTaskUseCases interface {
	Create(ctx context.Context, input CreateWorkflowTaskInput) (*WorkflowTask, error)
	GetById(ctx context.Context, id WorkflowTaskID) (*WorkflowTask, error)
	Update(ctx context.Context, id WorkflowTaskID, input UpdateWorkflowTaskInput) (*WorkflowTask, error)
	Delete(ctx context.Context, id WorkflowTaskID) error
	List(ctx context.Context, args sharedtypes.ListArgs) (*sharedtypes.ListResult[WorkflowTask], error)
}
