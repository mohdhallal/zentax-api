package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleWorkflow() *domain.Workflow {
	return &domain.Workflow{
		ID:               "44444444-4444-4444-4444-444444444444",
		Name:             "Monthly VAT",
		WorkflowCategory: "recurring",
		SelectedPeriods:  domain.Periods{"M1", "M2"},
		DueDateRule:      domain.DueDateRule{Reference: "period_end", OffsetUnit: "days", OffsetValue: 15},
		Status:           "draft",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
}

func TestWorkflowCreate_DefaultsCategory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateWorkflowInput{Name: "Monthly VAT"} // no category
	want := in
	want.WorkflowCategory = "recurring"

	repo.On("Create", ctx, want).Return(sampleWorkflow(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestWorkflowCreate_RepoError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateWorkflowInput{Name: "Wf", WorkflowCategory: "project"}
	repo.On("Create", ctx, in).Return(nil, assert.AnError).Once()

	_, err := uc.Create(ctx, in)
	assert.ErrorIs(t, err, assert.AnError)
	repo.AssertExpectations(t)
}

func TestWorkflowGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestWorkflowGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	wf := sampleWorkflow()
	repo.On("GetById", ctx, wf.ID).Return(wf, nil).Once()

	result, err := uc.GetById(ctx, wf.ID)
	require.NoError(t, err)
	assert.Equal(t, wf, result)
	repo.AssertExpectations(t)
}

func TestWorkflowUpdate_DefaultsAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.UpdateWorkflowInput{Name: "Wf"} // no category/status
	want := in
	want.WorkflowCategory = "recurring"
	want.Status = "draft"

	repo.On("Update", ctx, "missing", want).Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", in)
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestWorkflowUpdate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	wf := sampleWorkflow()
	in := domain.UpdateWorkflowInput{Name: "Wf", WorkflowCategory: "project", Status: "active"}
	repo.On("Update", ctx, wf.ID, in).Return(wf, nil).Once()

	result, err := uc.Update(ctx, wf.ID, in)
	require.NoError(t, err)
	assert.Equal(t, wf, result)
	repo.AssertExpectations(t)
}

func TestWorkflowDelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("CountDependents", ctx, "missing").Return(domain.WorkflowDependents{}, nil).Once()
	repo.On("QueueBlobReclaim", ctx, "missing").Return(0, nil).Once()
	repo.On("Delete", ctx, "missing").Return(false, nil).Once()

	err := uc.Delete(ctx, "missing")
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestWorkflowDelete_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("CountDependents", ctx, "wf1").
		Return(domain.WorkflowDependents{TaskInstances: 4, WorkflowTasks: 2}, nil).Once()
	repo.On("QueueBlobReclaim", ctx, "wf1").Return(0, nil).Once()
	repo.On("Delete", ctx, "wf1").Return(true, nil).Once()

	err := uc.Delete(ctx, "wf1")
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// ADR-0018: approved work below the workflow makes the delete a conflict, and
// nothing is queued or deleted — the census is the only call that happens.
func TestWorkflowDelete_RefusedWhenApprovedWorkExists(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("CountDependents", ctx, "wf1").
		Return(domain.WorkflowDependents{ApprovedTaskInstances: 24, TaskInstances: 108}, nil).Once()

	err := uc.Delete(ctx, "wf1")
	require.IsType(t, &apperrors.ConflictError{}, err)
	assert.Contains(t, err.Error(), "24 approved task instance(s)")
	assert.Contains(t, err.Error(), "archive")
	repo.AssertExpectations(t)
	repo.AssertNotCalled(t, "Delete", ctx, "wf1")
	repo.AssertNotCalled(t, "QueueBlobReclaim", ctx, "wf1")
}

func TestWorkflowList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.Workflow{*sampleWorkflow()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
