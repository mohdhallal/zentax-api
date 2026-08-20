package domain

import (
	"context"

	"github.com/stretchr/testify/mock"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// ObligationTypeRepositoryMock is a testify mock of ObligationTypeRepository.
type ObligationTypeRepositoryMock struct {
	mock.Mock
}

var _ ObligationTypeRepository = (*ObligationTypeRepositoryMock)(nil)

func (m *ObligationTypeRepositoryMock) Create(ctx context.Context, input CreateObligationTypeInput) (*ObligationType, error) {
	args := m.Called(ctx, input)
	return obligationTypeOrNil(args.Get(0)), args.Error(1)
}

func (m *ObligationTypeRepositoryMock) GetById(ctx context.Context, id ObligationTypeID) (*ObligationType, error) {
	args := m.Called(ctx, id)
	return obligationTypeOrNil(args.Get(0)), args.Error(1)
}

func (m *ObligationTypeRepositoryMock) Update(ctx context.Context, id ObligationTypeID, input UpdateObligationTypeInput) (*ObligationType, error) {
	args := m.Called(ctx, id, input)
	return obligationTypeOrNil(args.Get(0)), args.Error(1)
}

func (m *ObligationTypeRepositoryMock) Delete(ctx context.Context, id ObligationTypeID) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *ObligationTypeRepositoryMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) ([]ObligationType, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ObligationType), args.Error(1)
}

func (m *ObligationTypeRepositoryMock) GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error) {
	args := m.Called(ctx, filters)
	return args.Int(0), args.Error(1)
}

// ObligationTypeUseCasesMock is a testify mock of ObligationTypeUseCases.
type ObligationTypeUseCasesMock struct {
	mock.Mock
}

var _ ObligationTypeUseCases = (*ObligationTypeUseCasesMock)(nil)

func (m *ObligationTypeUseCasesMock) Create(ctx context.Context, input CreateObligationTypeInput) (*ObligationType, error) {
	args := m.Called(ctx, input)
	return obligationTypeOrNil(args.Get(0)), args.Error(1)
}

func (m *ObligationTypeUseCasesMock) GetById(ctx context.Context, id ObligationTypeID) (*ObligationType, error) {
	args := m.Called(ctx, id)
	return obligationTypeOrNil(args.Get(0)), args.Error(1)
}

func (m *ObligationTypeUseCasesMock) Update(ctx context.Context, id ObligationTypeID, input UpdateObligationTypeInput) (*ObligationType, error) {
	args := m.Called(ctx, id, input)
	return obligationTypeOrNil(args.Get(0)), args.Error(1)
}

func (m *ObligationTypeUseCasesMock) Delete(ctx context.Context, id ObligationTypeID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *ObligationTypeUseCasesMock) List(ctx context.Context, listArgs sharedtypes.ListArgs) (*sharedtypes.ListResult[ObligationType], error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sharedtypes.ListResult[ObligationType]), args.Error(1)
}

func obligationTypeOrNil(v any) *ObligationType {
	if v == nil {
		return nil
	}
	return v.(*ObligationType)
}
