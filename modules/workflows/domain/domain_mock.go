package domain

import (
	"context"

	"github.com/stretchr/testify/mock"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// WorkflowRepositoryMock is a testify mock of WorkflowRepository.
type WorkflowRepositoryMock struct {
	mock.Mock
}

var _ WorkflowRepository = (*WorkflowRepositoryMock)(nil)

func (m *WorkflowRepositoryMock) Create(ctx context.Context, input CreateWorkflowInput) (*Workflow, error) {
	args := m.Called(ctx, input)
	return workflowOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowRepositoryMock) GetById(ctx context.Context, id WorkflowID) (*Workflow, error) {
	args := m.Called(ctx, id)
	return workflowOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowRepositoryMock) Update(ctx context.Context, id WorkflowID, input UpdateWorkflowInput) (*Workflow, error) {
	args := m.Called(ctx, id, input)
	return workflowOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowRepositoryMock) Delete(ctx context.Context, id WorkflowID) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *WorkflowRepositoryMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) ([]Workflow, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]Workflow), args.Error(1)
}

func (m *WorkflowRepositoryMock) GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error) {
	args := m.Called(ctx, filters)
	return args.Int(0), args.Error(1)
}

// WorkflowUseCasesMock is a testify mock of WorkflowUseCases.
type WorkflowUseCasesMock struct {
	mock.Mock
}

var _ WorkflowUseCases = (*WorkflowUseCasesMock)(nil)

func (m *WorkflowUseCasesMock) Create(ctx context.Context, input CreateWorkflowInput) (*Workflow, error) {
	args := m.Called(ctx, input)
	return workflowOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowUseCasesMock) GetById(ctx context.Context, id WorkflowID) (*Workflow, error) {
	args := m.Called(ctx, id)
	return workflowOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowUseCasesMock) Update(ctx context.Context, id WorkflowID, input UpdateWorkflowInput) (*Workflow, error) {
	args := m.Called(ctx, id, input)
	return workflowOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowUseCasesMock) Delete(ctx context.Context, id WorkflowID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *WorkflowUseCasesMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) (*sharedtypes.ListResult[Workflow], error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sharedtypes.ListResult[Workflow]), args.Error(1)
}

func workflowOrNil(v any) *Workflow {
	if v == nil {
		return nil
	}
	return v.(*Workflow)
}
