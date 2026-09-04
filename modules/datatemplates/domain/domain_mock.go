package domain

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// DataTemplateRepositoryMock is a testify mock of DataTemplateRepository.
type DataTemplateRepositoryMock struct {
	mock.Mock
}

var _ DataTemplateRepository = (*DataTemplateRepositoryMock)(nil)

func (m *DataTemplateRepositoryMock) Create(ctx context.Context, input CreateDataTemplateInput) (*DataTemplate, error) {
	args := m.Called(ctx, input)
	return dataTemplateOrNil(args.Get(0)), args.Error(1)
}

func (m *DataTemplateRepositoryMock) GetById(ctx context.Context, id DataTemplateID) (*DataTemplate, error) {
	args := m.Called(ctx, id)
	return dataTemplateOrNil(args.Get(0)), args.Error(1)
}

func (m *DataTemplateRepositoryMock) Update(ctx context.Context, id DataTemplateID, input UpdateDataTemplateInput) (*DataTemplate, error) {
	args := m.Called(ctx, id, input)
	return dataTemplateOrNil(args.Get(0)), args.Error(1)
}

func (m *DataTemplateRepositoryMock) Delete(ctx context.Context, id DataTemplateID) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *DataTemplateRepositoryMock) List(ctx context.Context, listArgs ListDataTemplatesArgs) ([]DataTemplate, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]DataTemplate), args.Error(1)
}

func (m *DataTemplateRepositoryMock) ReferenceCount(ctx context.Context, id DataTemplateID) (int, error) {
	args := m.Called(ctx, id)
	return args.Int(0), args.Error(1)
}

// DataTemplateUseCasesMock is a testify mock of DataTemplateUseCases.
type DataTemplateUseCasesMock struct {
	mock.Mock
}

var _ DataTemplateUseCases = (*DataTemplateUseCasesMock)(nil)

func (m *DataTemplateUseCasesMock) Create(ctx context.Context, input CreateDataTemplateInput) (*DataTemplate, error) {
	args := m.Called(ctx, input)
	return dataTemplateOrNil(args.Get(0)), args.Error(1)
}

func (m *DataTemplateUseCasesMock) GetById(ctx context.Context, id DataTemplateID) (*DataTemplate, error) {
	args := m.Called(ctx, id)
	return dataTemplateOrNil(args.Get(0)), args.Error(1)
}

func (m *DataTemplateUseCasesMock) Update(ctx context.Context, id DataTemplateID, input UpdateDataTemplateInput) (*DataTemplate, error) {
	args := m.Called(ctx, id, input)
	return dataTemplateOrNil(args.Get(0)), args.Error(1)
}

func (m *DataTemplateUseCasesMock) Delete(ctx context.Context, id DataTemplateID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *DataTemplateUseCasesMock) List(ctx context.Context, listArgs ListDataTemplatesArgs) ([]DataTemplate, error) {
	args := m.Called(ctx, listArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]DataTemplate), args.Error(1)
}

func (m *DataTemplateUseCasesMock) SeedPredefined(ctx context.Context) ([]DataTemplate, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]DataTemplate), args.Error(1)
}

func dataTemplateOrNil(v any) *DataTemplate {
	if v == nil {
		return nil
	}
	return v.(*DataTemplate)
}
