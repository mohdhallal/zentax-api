package domain

import (
	"context"

	"github.com/stretchr/testify/mock"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// WorkflowTaskRepositoryMock is a testify mock of WorkflowTaskRepository.
type WorkflowTaskRepositoryMock struct {
	mock.Mock
}

var _ WorkflowTaskRepository = (*WorkflowTaskRepositoryMock)(nil)

func (m *WorkflowTaskRepositoryMock) Create(ctx context.Context, input CreateWorkflowTaskInput) (*WorkflowTask, error) {
	args := m.Called(ctx, input)
	return workflowTaskOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowTaskRepositoryMock) GetById(ctx context.Context, id WorkflowTaskID) (*WorkflowTask, error) {
	args := m.Called(ctx, id)
	return workflowTaskOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowTaskRepositoryMock) Update(ctx context.Context, id WorkflowTaskID, input UpdateWorkflowTaskInput) (*WorkflowTask, error) {
	args := m.Called(ctx, id, input)
	return workflowTaskOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowTaskRepositoryMock) CountDependents(ctx context.Context, id WorkflowTaskID) (WorkflowTaskDependents, error) {
	args := m.Called(ctx, id)
	dep, _ := args.Get(0).(WorkflowTaskDependents)
	return dep, args.Error(1)
}

func (m *WorkflowTaskRepositoryMock) Delete(ctx context.Context, id WorkflowTaskID) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *WorkflowTaskRepositoryMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) ([]WorkflowTask, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]WorkflowTask), args.Error(1)
}

func (m *WorkflowTaskRepositoryMock) GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error) {
	args := m.Called(ctx, filters)
	return args.Int(0), args.Error(1)
}

func (m *WorkflowTaskRepositoryMock) ListByWorkflow(ctx context.Context, workflowID string) ([]WorkflowTask, error) {
	args := m.Called(ctx, workflowID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]WorkflowTask), args.Error(1)
}

// WorkflowTaskUseCasesMock is a testify mock of WorkflowTaskUseCases.
type WorkflowTaskUseCasesMock struct {
	mock.Mock
}

var _ WorkflowTaskUseCases = (*WorkflowTaskUseCasesMock)(nil)

func (m *WorkflowTaskUseCasesMock) Create(ctx context.Context, input CreateWorkflowTaskInput) (*WorkflowTask, error) {
	args := m.Called(ctx, input)
	return workflowTaskOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowTaskUseCasesMock) GetById(ctx context.Context, id WorkflowTaskID) (*WorkflowTask, error) {
	args := m.Called(ctx, id)
	return workflowTaskOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowTaskUseCasesMock) Update(ctx context.Context, id WorkflowTaskID, input UpdateWorkflowTaskInput) (*WorkflowTask, error) {
	args := m.Called(ctx, id, input)
	return workflowTaskOrNil(args.Get(0)), args.Error(1)
}

func (m *WorkflowTaskUseCasesMock) Delete(ctx context.Context, id WorkflowTaskID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *WorkflowTaskUseCasesMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) (*sharedtypes.ListResult[WorkflowTask], error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sharedtypes.ListResult[WorkflowTask]), args.Error(1)
}

func workflowTaskOrNil(v any) *WorkflowTask {
	if v == nil {
		return nil
	}
	return v.(*WorkflowTask)
}
