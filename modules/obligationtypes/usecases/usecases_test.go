package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleObligationType() *domain.ObligationType {
	return &domain.ObligationType{
		ID:        "22222222-2222-2222-2222-222222222222",
		Name:      "VAT Return",
		Code:      "VAT-RET",
		Category:  "custom",
		Template:  "VAT",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestOTCreate_DefaultsCategory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateObligationTypeInput{Name: "VAT Return", Code: "VAT-RET", Template: "VAT"}
	want := in
	want.Category = "custom" // repo must receive the default

	repo.On("Create", ctx, want).Return(sampleObligationType(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestOTCreate_PassesCategory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateObligationTypeInput{Name: "CIT", Code: "CIT-A", Template: "CIT", Category: "predefined"}
	repo.On("Create", ctx, in).Return(sampleObligationType(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestOTGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestOTGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	ot := sampleObligationType()
	repo.On("GetById", ctx, ot.ID).Return(ot, nil).Once()

	result, err := uc.GetById(ctx, ot.ID)
	require.NoError(t, err)
	assert.Equal(t, ot, result)
	repo.AssertExpectations(t)
}

func TestOTGetById_RepoError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "x").Return(nil, assert.AnError).Once()

	_, err := uc.GetById(ctx, "x")
	assert.ErrorIs(t, err, assert.AnError)
	repo.AssertExpectations(t)
}

func TestOTUpdate_DefaultsAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.UpdateObligationTypeInput{Name: "VAT", Code: "VAT-RET", Template: "VAT"} // no category/status
	want := in
	want.Category = "custom"
	want.Status = "active"

	repo.On("Update", ctx, "missing", want).Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", in)
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestOTUpdate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	ot := sampleObligationType()
	in := domain.UpdateObligationTypeInput{
		Name: "VAT", Code: "VAT-RET", Template: "VAT", Category: "custom", Status: "inactive",
	}
	repo.On("Update", ctx, ot.ID, in).Return(ot, nil).Once()

	result, err := uc.Update(ctx, ot.ID, in)
	require.NoError(t, err)
	assert.Equal(t, ot, result)
	repo.AssertExpectations(t)
}

func TestOTDelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("Delete", ctx, "missing").Return(false, nil).Once()

	err := uc.Delete(ctx, "missing")
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestOTDelete_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("Delete", ctx, "ot1").Return(true, nil).Once()

	err := uc.Delete(ctx, "ot1")
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestOTList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.ObligationType{*sampleObligationType()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
