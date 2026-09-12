package domain

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/mohamadhallal/zentax-api/shared/deadline"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// EntityRepositoryMock is a testify mock of EntityRepository for use-case tests.
type EntityRepositoryMock struct {
	mock.Mock
}

var _ EntityRepository = (*EntityRepositoryMock)(nil)

func (m *EntityRepositoryMock) Create(ctx context.Context, input CreateEntityInput) (*Entity, error) {
	args := m.Called(ctx, input)
	return entityOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityRepositoryMock) GetById(ctx context.Context, id EntityID) (*Entity, error) {
	args := m.Called(ctx, id)
	return entityOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityRepositoryMock) Update(ctx context.Context, id EntityID, input UpdateEntityInput) (*Entity, error) {
	args := m.Called(ctx, id, input)
	return entityOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityRepositoryMock) CountDependents(ctx context.Context, id EntityID) (EntityDependents, error) {
	args := m.Called(ctx, id)
	dep, _ := args.Get(0).(EntityDependents)
	return dep, args.Error(1)
}

func (m *EntityRepositoryMock) QueueBlobReclaim(ctx context.Context, id EntityID) (int, error) {
	args := m.Called(ctx, id)
	return args.Int(0), args.Error(1)
}

func (m *EntityRepositoryMock) Delete(ctx context.Context, id EntityID) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *EntityRepositoryMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) ([]Entity, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]Entity), args.Error(1)
}

func (m *EntityRepositoryMock) GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error) {
	args := m.Called(ctx, filters)
	return args.Int(0), args.Error(1)
}

// EntityUseCasesMock is a testify mock of EntityUseCases for handler tests.
type EntityUseCasesMock struct {
	mock.Mock
}

var _ EntityUseCases = (*EntityUseCasesMock)(nil)

func (m *EntityUseCasesMock) Create(ctx context.Context, input CreateEntityInput) (*Entity, error) {
	args := m.Called(ctx, input)
	return entityOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityUseCasesMock) GetById(ctx context.Context, id EntityID) (*Entity, error) {
	args := m.Called(ctx, id)
	return entityOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityUseCasesMock) Update(ctx context.Context, id EntityID, input UpdateEntityInput) (*Entity, error) {
	args := m.Called(ctx, id, input)
	return entityOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityUseCasesMock) Delete(ctx context.Context, id EntityID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *EntityUseCasesMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) (*sharedtypes.ListResult[Entity], error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sharedtypes.ListResult[Entity]), args.Error(1)
}

func (m *EntityUseCasesMock) Periods(ctx context.Context, id EntityID, periodicity string, financialYear int) ([]deadline.Period, error) {
	args := m.Called(ctx, id, periodicity, financialYear)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]deadline.Period), args.Error(1)
}

func entityOrNil(v any) *Entity {
	if v == nil {
		return nil
	}
	return v.(*Entity)
}
