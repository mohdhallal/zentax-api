package usecases

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
)

func TestSubmit_RequiresApprovalRequired(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	ti.ApprovalRequired = false
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()

	_, err := uc.SubmitForApproval(ctx, ti.ID, "user-1")
	assert.IsType(t, &apperrors.ValidationError{}, err)
	repo.AssertExpectations(t) // SubmitForApproval never reached
}

func TestSubmit_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	ti.ApprovalRequired = true
	ti.Status = domain.StatusInProgress
	submitted := *ti
	submitted.Status = domain.StatusPendingApproval

	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("SubmitForApproval", ctx, ti.ID, "user-1").Return(&submitted, nil).Once()

	res, err := uc.SubmitForApproval(ctx, ti.ID, "user-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPendingApproval, res.Status)
	repo.AssertExpectations(t)
}

// The submitter cannot approve their own submission (segregation of duties).
func TestApprove_SeparationOfDuties(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	ti.Status = domain.StatusPendingApproval
	submitter := "user-1"
	ti.SubmittedBy = &submitter
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()

	_, err := uc.Approve(ctx, ti.ID, "user-1")
	assert.IsType(t, &apperrors.ForbiddenError{}, err)
	repo.AssertExpectations(t) // Approve never reached
}

func TestApprove_DifferentApproverSucceeds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	ti.Status = domain.StatusPendingApproval
	submitter := "user-1"
	ti.SubmittedBy = &submitter
	approved := *ti
	approver := "user-2"
	approved.Status = domain.StatusCompleted
	approved.ApprovedBy = &approver

	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("Approve", ctx, ti.ID, "user-2").Return(&approved, nil).Once()

	res, err := uc.Approve(ctx, ti.ID, "user-2")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, res.Status)
	repo.AssertExpectations(t)
}

func TestApprove_NotPendingIsConflict(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance() // not_started — not pending approval
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()

	_, err := uc.Approve(ctx, ti.ID, "user-2")
	assert.IsType(t, &apperrors.ConflictError{}, err)
	repo.AssertExpectations(t)
}
