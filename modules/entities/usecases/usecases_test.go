package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleEntity() *domain.Entity {
	return &domain.Entity{
		ID:                    "11111111-1111-1111-1111-111111111111",
		Name:                  "Acme GmbH",
		Country:               "Germany",
		FiscalCalendarPattern: "standard",
		Status:                "active",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
}

func TestEntityCreate_DefaultsFiscalPattern(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateEntityInput{Name: "Acme GmbH", Country: "Germany"} // pattern omitted
	want := in
	want.FiscalCalendarPattern = "standard" // repo must receive the defaults
	want.FiscalWeekEndDay = "saturday"
	want.FiscalYearEndRule = "nearest"

	entity := sampleEntity()
	repo.On("Create", ctx, want).Return(entity, nil).Once()

	result, err := uc.Create(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, entity, result)
	repo.AssertExpectations(t)
}

func TestEntityCreate_PassesThroughPattern(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateEntityInput{
		Name: "Acme", Country: "Germany", FiscalCalendarPattern: "445",
		FiscalWeekEndDay: "sunday", FiscalYearEndRule: "last",
	}
	repo.On("Create", ctx, in).Return(sampleEntity(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestEntityGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	assert.Contains(t, err.Error(), "missing")
	repo.AssertExpectations(t)
}

func TestEntityGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	entity := sampleEntity()
	repo.On("GetById", ctx, entity.ID).Return(entity, nil).Once()

	result, err := uc.GetById(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, entity, result)
	repo.AssertExpectations(t)
}

func TestEntityGetById_RepoError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "x").Return(nil, assert.AnError).Once()

	_, err := uc.GetById(ctx, "x")
	assert.ErrorIs(t, err, assert.AnError)
	repo.AssertExpectations(t)
}

func TestEntityUpdate_DefaultsAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.UpdateEntityInput{Name: "Acme", Country: "Germany"} // no pattern, no status
	want := in
	want.FiscalCalendarPattern = "standard"
	want.FiscalWeekEndDay = "saturday"
	want.FiscalYearEndRule = "nearest"
	want.Status = "active"

	repo.On("Update", ctx, "missing", want).Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", in)
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestEntityUpdate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	entity := sampleEntity()
	in := domain.UpdateEntityInput{
		Name: "Acme", Country: "Germany", FiscalCalendarPattern: "454", Status: "inactive",
		FiscalWeekEndDay: "saturday", FiscalYearEndRule: "nearest",
	}
	repo.On("Update", ctx, entity.ID, in).Return(entity, nil).Once()

	result, err := uc.Update(ctx, entity.ID, in)
	require.NoError(t, err)
	assert.Equal(t, entity, result)
	repo.AssertExpectations(t)
}

func TestEntityDelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("Delete", ctx, "missing").Return(false, nil).Once()

	err := uc.Delete(ctx, "missing")
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestEntityDelete_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("Delete", ctx, "e1").Return(true, nil).Once()

	err := uc.Delete(ctx, "e1")
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestEntityList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.Entity{*sampleEntity()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
