package domain

import (
	"context"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

type WorkflowRepository interface {
	Create(ctx context.Context, input CreateWorkflowInput) (*Workflow, error)
	GetById(ctx context.Context, id WorkflowID) (*Workflow, error)
	Update(ctx context.Context, id WorkflowID, input UpdateWorkflowInput) (*Workflow, error)
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
