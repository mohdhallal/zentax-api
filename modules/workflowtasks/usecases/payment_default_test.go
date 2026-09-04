package usecases

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
)

// A payment-type task with no explicit reference is due off the payment
// deadline (ADR-0023 §5); an explicit reference is always kept.
func TestWTCreate_PaymentTaskDefaultsToPaymentDeadline(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.WorkflowTaskRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateWorkflowTaskInput{WorkflowID: "wf", Name: "Pay", TaskType: "payment"}
	want := in
	want.DueDateReference = "payment_deadline"
	want.DueDateOffsetUnit = "days"
	want.DueDateOffsetDirection = "before"
	repo.On("Create", ctx, want).Return(sampleWorkflowTask(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)

	explicit := domain.CreateWorkflowTaskInput{
		WorkflowID: "wf", Name: "Pay", TaskType: "payment",
		DueDateReference: "filing_deadline", DueDateOffsetUnit: "days", DueDateOffsetDirection: "before",
	}
	repo.On("Create", ctx, explicit).Return(sampleWorkflowTask(), nil).Once()
	_, err = uc.Create(ctx, explicit)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}
