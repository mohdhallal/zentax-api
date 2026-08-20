package domain

import (
	"context"

	"github.com/stretchr/testify/mock"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// EntityObligationRepositoryMock is a testify mock of EntityObligationRepository.
type EntityObligationRepositoryMock struct {
	mock.Mock
}

var _ EntityObligationRepository = (*EntityObligationRepositoryMock)(nil)

func (m *EntityObligationRepositoryMock) Create(ctx context.Context, input CreateEntityObligationInput) (*EntityObligation, error) {
	args := m.Called(ctx, input)
	return entityObligationOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityObligationRepositoryMock) GetById(ctx context.Context, id EntityObligationID) (*EntityObligation, error) {
	args := m.Called(ctx, id)
	return entityObligationOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityObligationRepositoryMock) Update(ctx context.Context, id EntityObligationID, input UpdateEntityObligationInput) (*EntityObligation, error) {
	args := m.Called(ctx, id, input)
	return entityObligationOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityObligationRepositoryMock) Delete(ctx context.Context, id EntityObligationID) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *EntityObligationRepositoryMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) ([]EntityObligation, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]EntityObligation), args.Error(1)
}

func (m *EntityObligationRepositoryMock) GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error) {
	args := m.Called(ctx, filters)
	return args.Int(0), args.Error(1)
}

// EntityObligationUseCasesMock is a testify mock of EntityObligationUseCases.
type EntityObligationUseCasesMock struct {
	mock.Mock
}

var _ EntityObligationUseCases = (*EntityObligationUseCasesMock)(nil)

func (m *EntityObligationUseCasesMock) Create(ctx context.Context, input CreateEntityObligationInput) (*EntityObligation, error) {
	args := m.Called(ctx, input)
	return entityObligationOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityObligationUseCasesMock) GetById(ctx context.Context, id EntityObligationID) (*EntityObligation, error) {
	args := m.Called(ctx, id)
	return entityObligationOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityObligationUseCasesMock) Update(ctx context.Context, id EntityObligationID, input UpdateEntityObligationInput) (*EntityObligation, error) {
	args := m.Called(ctx, id, input)
	return entityObligationOrNil(args.Get(0)), args.Error(1)
}

func (m *EntityObligationUseCasesMock) Delete(ctx context.Context, id EntityObligationID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *EntityObligationUseCasesMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) (*sharedtypes.ListResult[EntityObligation], error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sharedtypes.ListResult[EntityObligation]), args.Error(1)
}

func entityObligationOrNil(v any) *EntityObligation {
	if v == nil {
		return nil
	}
	return v.(*EntityObligation)
}
