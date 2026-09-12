package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleWorkflowTask() *domain.WorkflowTask {
	return &domain.WorkflowTask{
		ID:                     "55555555-5555-5555-5555-555555555555",
		WorkflowID:             "44444444-4444-4444-4444-444444444444",
		Name:                   "Prepare VAT",
		TaskType:               "preparation",
		DueDateReference:       "filing_deadline",
		DueDateOffsetValue:     5,
		DueDateOffsetUnit:      "days",
		DueDateOffsetDirection: "before",
		CreatedAt:              time.Now(),
		UpdatedAt:              time.Now(),
	}
}

func TestWTCreate_DefaultsDueDateFields(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	// caller omits the due-date rule fields
	in := domain.CreateWorkflowTaskInput{
		WorkflowID: "44444444-4444-4444-4444-444444444444", Name: "Prepare VAT", TaskType: "preparation",
	}
	want := in
	want.DueDateReference = "filing_deadline"
	want.DueDateOffsetUnit = "days"
	want.DueDateOffsetDirection = "before"

	repo.On("Create", ctx, want).Return(sampleWorkflowTask(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestWTCreate_RepoError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateWorkflowTaskInput{
		WorkflowID: "wf", Name: "T", TaskType: "review",
		DueDateReference: "period_end", DueDateOffsetUnit: "weeks", DueDateOffsetDirection: "after",
	}
	repo.On("Create", ctx, in).Return(nil, assert.AnError).Once()

	_, err := uc.Create(ctx, in)
	assert.ErrorIs(t, err, assert.AnError)
	repo.AssertExpectations(t)
}

func TestWTGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestWTGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	wt := sampleWorkflowTask()
	repo.On("GetById", ctx, wt.ID).Return(wt, nil).Once()

	result, err := uc.GetById(ctx, wt.ID)
	require.NoError(t, err)
	assert.Equal(t, wt, result)
	repo.AssertExpectations(t)
}

func TestWTUpdate_DefaultsAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.UpdateWorkflowTaskInput{Name: "T", TaskType: "review"} // no due-date fields
	want := in
	want.DueDateReference = "filing_deadline"
	want.DueDateOffsetUnit = "days"
	want.DueDateOffsetDirection = "before"

	repo.On("Update", ctx, "missing", want).Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", in)
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestWTUpdate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	wt := sampleWorkflowTask()
	in := domain.UpdateWorkflowTaskInput{
		Name: "T", TaskType: "submission",
		DueDateReference: "period_end", DueDateOffsetUnit: "months", DueDateOffsetDirection: "after",
	}
	repo.On("Update", ctx, wt.ID, in).Return(wt, nil).Once()

	result, err := uc.Update(ctx, wt.ID, in)
	require.NoError(t, err)
	assert.Equal(t, wt, result)
	repo.AssertExpectations(t)
}

func TestWTDelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("CountDependents", ctx, "missing").Return(domain.WorkflowTaskDependents{}, nil).Once()
	repo.On("Delete", ctx, "missing").Return(false, nil).Once()

	err := uc.Delete(ctx, "missing")
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestWTDelete_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("CountDependents", ctx, "wt1").
		Return(domain.WorkflowTaskDependents{TaskInstances: 6}, nil).Once()
	repo.On("Delete", ctx, "wt1").Return(true, nil).Once()

	err := uc.Delete(ctx, "wt1")
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// ADR-0018: removing a template step that generated approved instances is a
// conflict — the instances are attested evidence, not template detail.
func TestWTDelete_RefusedWhenApprovedWorkExists(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("CountDependents", ctx, "wt1").
		Return(domain.WorkflowTaskDependents{ApprovedTaskInstances: 3, TaskInstances: 12}, nil).Once()

	err := uc.Delete(ctx, "wt1")
	require.IsType(t, &apperrors.ConflictError{}, err)
	assert.Contains(t, err.Error(), "3 approved task instance(s)")
	repo.AssertExpectations(t)
	repo.AssertNotCalled(t, "Delete", ctx, "wt1")
}

func TestWTList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.WorkflowTask{*sampleWorkflowTask()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
