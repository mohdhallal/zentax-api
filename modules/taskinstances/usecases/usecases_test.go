package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleTaskInstance() *domain.TaskInstance {
	return &domain.TaskInstance{
		ID: "ti-1", WorkflowID: "wf-1", WorkflowTaskID: "wt-1", PeriodCode: "M1",
		Name: "Prepare", TaskType: "preparation", Status: "not_started",
		DueDate:        dateonly.New(2025, 2, 10),
		PeriodEndDate:  dateonly.New(2025, 1, 31),
		FilingDeadline: dateonly.New(2025, 2, 15),
		TaxDataStatus:  "draft",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}

func TestTIGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestTIGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()

	result, err := uc.GetById(ctx, ti.ID)
	require.NoError(t, err)
	assert.Equal(t, ti, result)
	repo.AssertExpectations(t)
}

func TestTIUpdate_DefaultsTaxDataStatusAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.UpdateTaskInstanceInput{Status: "in_progress"} // no taxDataStatus
	want := in
	want.TaxDataStatus = "draft"

	repo.On("Update", ctx, "missing", want).Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", in)
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestTIUpdate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	in := domain.UpdateTaskInstanceInput{Status: "completed", TaxDataStatus: "final"}
	repo.On("Update", ctx, ti.ID, in).Return(ti, nil).Once()

	result, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	assert.Equal(t, ti, result)
	repo.AssertExpectations(t)
}

func TestTIList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.TaskInstance{*sampleTaskInstance()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
