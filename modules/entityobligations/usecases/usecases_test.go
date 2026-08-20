package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleEntityObligation() *domain.EntityObligation {
	jur := "DE"
	return &domain.EntityObligation{
		ID:               "33333333-3333-3333-3333-333333333333",
		EntityID:         "11111111-1111-1111-1111-111111111111",
		ObligationTypeID: "22222222-2222-2222-2222-222222222222",
		Jurisdiction:     &jur,
		Periodicity:      "monthly",
		DeadlineRule: domain.DeadlineRule{
			Type: "period_offset", Reference: "period_end",
			OffsetUnit: "days", OffsetValue: 20, OffsetDirection: "after",
		},
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func sampleCreateInput() domain.CreateEntityObligationInput {
	return domain.CreateEntityObligationInput{
		EntityID:         "11111111-1111-1111-1111-111111111111",
		ObligationTypeID: "22222222-2222-2222-2222-222222222222",
		Periodicity:      "monthly",
		DeadlineRule:     domain.DeadlineRule{Type: "period_offset", OffsetUnit: "days", OffsetValue: 20},
	}
}

func TestEOCreate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	in := sampleCreateInput()
	eo := sampleEntityObligation()
	repo.On("Create", ctx, in).Return(eo, nil).Once()

	result, err := uc.Create(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, eo, result)
	repo.AssertExpectations(t)
}

func TestEOCreate_RepoError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	in := sampleCreateInput()
	repo.On("Create", ctx, in).Return(nil, assert.AnError).Once()

	_, err := uc.Create(ctx, in)
	assert.ErrorIs(t, err, assert.AnError)
	repo.AssertExpectations(t)
}

func TestEOGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestEOGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	eo := sampleEntityObligation()
	repo.On("GetById", ctx, eo.ID).Return(eo, nil).Once()

	result, err := uc.GetById(ctx, eo.ID)
	require.NoError(t, err)
	assert.Equal(t, eo, result)
	repo.AssertExpectations(t)
}

func TestEOUpdate_DefaultsStatusAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.UpdateEntityObligationInput{Periodicity: "quarterly"} // no status
	want := in
	want.Status = "active"

	repo.On("Update", ctx, "missing", want).Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", in)
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestEOUpdate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	eo := sampleEntityObligation()
	in := domain.UpdateEntityObligationInput{Periodicity: "annual", Status: "inactive"}
	repo.On("Update", ctx, eo.ID, in).Return(eo, nil).Once()

	result, err := uc.Update(ctx, eo.ID, in)
	require.NoError(t, err)
	assert.Equal(t, eo, result)
	repo.AssertExpectations(t)
}

func TestEODelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("Delete", ctx, "missing").Return(false, nil).Once()

	err := uc.Delete(ctx, "missing")
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestEODelete_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("Delete", ctx, "eo1").Return(true, nil).Once()

	err := uc.Delete(ctx, "eo1")
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestEOList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityObligationRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.EntityObligation{*sampleEntityObligation()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
