package usecases

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
)

// stubAssigneeChecker is the AssigneeChecker port with a fixed answer.
type stubAssigneeChecker struct {
	assignable map[string]bool
	calls      []string
}

func (s *stubAssigneeChecker) IsAssignable(_ context.Context, userID string) (bool, error) {
	s.calls = append(s.calls, userID)
	return s.assignable[userID], nil
}

func TestTIUpdate_RejectsNonMemberAssignee(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	checker := &stubAssigneeChecker{assignable: map[string]bool{"member-1": true}}
	uc := NewUseCases(repo).WithAssigneeChecker(checker)

	ti := sampleTaskInstance()
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()

	foreign := "foreign-9"
	result, err := uc.Update(ctx, ti.ID, domain.UpdateTaskInstanceInput{Status: "in_progress", AssigneeID: &foreign})
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Equal(t, []string{"foreign-9"}, checker.calls)
	repo.AssertExpectations(t) // Update never reached
}

func TestTIUpdate_AcceptsActiveMemberAssignee(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	checker := &stubAssigneeChecker{assignable: map[string]bool{"member-1": true}}
	uc := NewUseCases(repo).WithAssigneeChecker(checker)

	ti := sampleTaskInstance()
	member := "member-1"
	in := domain.UpdateTaskInstanceInput{Status: "in_progress", AssigneeID: &member}
	want := in
	want.TaxDataStatus = "draft"

	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()

	result, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	assert.Equal(t, ti, result)
	repo.AssertExpectations(t)
}

// A save that carries the CURRENT assignee back unchanged is not re-checked —
// a task assigned to a since-disabled member stays editable — while moving it
// to someone else is.
func TestTIUpdate_UnchangedAssigneeNotRechecked(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	checker := &stubAssigneeChecker{} // nobody is assignable any more
	uc := NewUseCases(repo).WithAssigneeChecker(checker)

	gone := "member-gone"
	ti := sampleTaskInstance()
	ti.AssigneeID = &gone
	in := domain.UpdateTaskInstanceInput{Status: "in_progress", AssigneeID: &gone}
	want := in
	want.TaxDataStatus = "draft"
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Twice()
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()

	_, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	assert.Empty(t, checker.calls)

	other := "member-2"
	_, err = uc.Update(ctx, ti.ID, domain.UpdateTaskInstanceInput{Status: "in_progress", AssigneeID: &other})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Equal(t, []string{"member-2"}, checker.calls)
	repo.AssertExpectations(t)
}

// Without an assignee (or without a checker wired) nothing is consulted — the
// port is optional and nil-safe.
func TestTIUpdate_NoAssigneeSkipsCheck(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	checker := &stubAssigneeChecker{}
	uc := NewUseCases(repo).WithAssigneeChecker(checker)

	ti := sampleTaskInstance()
	in := domain.UpdateTaskInstanceInput{Status: "in_progress"}
	want := in
	want.TaxDataStatus = "draft"
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()

	_, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	assert.Empty(t, checker.calls)
	repo.AssertExpectations(t)
}
