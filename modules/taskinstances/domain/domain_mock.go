package domain

import (
	"context"

	"github.com/stretchr/testify/mock"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// TaskInstanceRepositoryMock is a testify mock of TaskInstanceRepository.
type TaskInstanceRepositoryMock struct {
	mock.Mock
}

var _ TaskInstanceRepository = (*TaskInstanceRepositoryMock)(nil)

func (m *TaskInstanceRepositoryMock) Create(ctx context.Context, input CreateTaskInstanceInput) (*TaskInstance, error) {
	args := m.Called(ctx, input)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) GetById(ctx context.Context, id TaskInstanceID) (*TaskInstance, error) {
	args := m.Called(ctx, id)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) Update(ctx context.Context, id TaskInstanceID, input UpdateTaskInstanceInput) (*TaskInstance, error) {
	args := m.Called(ctx, id, input)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) ([]TaskInstance, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]TaskInstance), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error) {
	args := m.Called(ctx, filters)
	return args.Int(0), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) CountByWorkflow(ctx context.Context, workflowID string) (int, error) {
	args := m.Called(ctx, workflowID)
	return args.Int(0), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) SubmitForApproval(ctx context.Context, id TaskInstanceID, submittedBy string) (*TaskInstance, error) {
	args := m.Called(ctx, id, submittedBy)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) Approve(ctx context.Context, id TaskInstanceID, approvedBy string) (*TaskInstance, error) {
	args := m.Called(ctx, id, approvedBy)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceRepositoryMock) Reject(ctx context.Context, id TaskInstanceID, reason *string) (*TaskInstance, error) {
	args := m.Called(ctx, id, reason)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

// TaskInstanceUseCasesMock is a testify mock of TaskInstanceUseCases.
type TaskInstanceUseCasesMock struct {
	mock.Mock
}

var _ TaskInstanceUseCases = (*TaskInstanceUseCasesMock)(nil)

func (m *TaskInstanceUseCasesMock) GetById(ctx context.Context, id TaskInstanceID) (*TaskInstance, error) {
	args := m.Called(ctx, id)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceUseCasesMock) Update(ctx context.Context, id TaskInstanceID, input UpdateTaskInstanceInput) (*TaskInstance, error) {
	args := m.Called(ctx, id, input)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceUseCasesMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) (*sharedtypes.ListResult[TaskInstance], error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sharedtypes.ListResult[TaskInstance]), args.Error(1)
}

func (m *TaskInstanceUseCasesMock) SubmitForApproval(ctx context.Context, id TaskInstanceID, actorID string) (*TaskInstance, error) {
	args := m.Called(ctx, id, actorID)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceUseCasesMock) Approve(ctx context.Context, id TaskInstanceID, actorID string) (*TaskInstance, error) {
	args := m.Called(ctx, id, actorID)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func (m *TaskInstanceUseCasesMock) Reject(ctx context.Context, id TaskInstanceID, reason *string) (*TaskInstance, error) {
	args := m.Called(ctx, id, reason)
	return taskInstanceOrNil(args.Get(0)), args.Error(1)
}

func taskInstanceOrNil(v any) *TaskInstance {
	if v == nil {
		return nil
	}
	return v.(*TaskInstance)
}
