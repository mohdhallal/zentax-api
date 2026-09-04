package usecases

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	tidomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// obligationResolverMock is a testify mock of tidomain.ObligationResolver.
type obligationResolverMock struct{ mock.Mock }

func (m *obligationResolverMock) FindByEntityAndType(ctx context.Context, entityID, obligationTypeID string) (*entityobligationsdomain.EntityObligation, error) {
	args := m.Called(ctx, entityID, obligationTypeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entityobligationsdomain.EntityObligation), args.Error(1)
}

// previewWith runs PreviewWorkflow over mocked repositories and returns the
// planned rows (preview and start share planWorkflow).
func previewWith(t *testing.T, wf *workflowsdomain.Workflow, entity *entitiesdomain.Entity, tasks []workflowtasksdomain.WorkflowTask, obligation *entityobligationsdomain.EntityObligation) []workflowsdomain.PreviewTask {
	t.Helper()
	ctx := t.Context()
	gen, _, workflows, workflowTasks, entities := newGenerator()
	workflows.On("GetById", ctx, wf.ID).Return(wf, nil).Once()
	entities.On("GetById", ctx, entity.ID).Return(entity, nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, wf.ID).Return(tasks, nil).Once()
	if wf.ObligationTypeID != nil {
		obligations := new(obligationResolverMock)
		obligations.On("FindByEntityAndType", ctx, entity.ID, *wf.ObligationTypeID).Return(obligation, nil).Once()
		gen.WithObligations(obligations)
	}
	preview, err := gen.PreviewWorkflow(ctx, wf.ID)
	require.NoError(t, err)
	return preview.Tasks
}

func filingTask() workflowtasksdomain.WorkflowTask {
	return workflowtasksdomain.WorkflowTask{
		ID: "wt-file", WorkflowID: "wf-1", Name: "File", TaskType: "submission",
		DueDateReference: "filing_deadline", DueDateOffsetUnit: "days", DueDateOffsetDirection: "before",
	}
}

func paymentTask() workflowtasksdomain.WorkflowTask {
	return workflowtasksdomain.WorkflowTask{
		ID: "wt-pay", WorkflowID: "wf-1", Name: "Pay", TaskType: "payment", OrderIndex: 1,
		DueDateReference: "payment_deadline", DueDateOffsetUnit: "days", DueDateOffsetDirection: "before",
	}
}

// A 4-4-5 entity (Saturday nearest 31 Jan) plans FY2025 = 4 Feb 2024 – 1 Feb
// 2025 through the calendar engine: M1 ends 2 Mar 2024, filing +15d.
func TestGenerator_Uses445Calendar(t *testing.T) {
	t.Parallel()
	wf := recurringWorkflow()
	wf.SelectedPeriods = workflowsdomain.Periods{"M1", "M12"}
	entity := &entitiesdomain.Entity{
		ID: "ent-1", FiscalCalendarPattern: "445", FinancialYearEnd: strp("01-31"),
		FiscalWeekEndDay: "saturday", FiscalYearEndRule: "nearest",
	}
	rows := previewWith(t, wf, entity, []workflowtasksdomain.WorkflowTask{filingTask()}, nil)
	require.Len(t, rows, 2)
	assert.Equal(t, dateonly.New(2024, 3, 2), rows[0].PeriodEndDate)
	assert.Equal(t, dateonly.New(2024, 3, 17), rows[0].FilingDeadline)
	assert.Equal(t, dateonly.New(2024, 3, 17), rows[0].PaymentDeadline, "no obligation → payment = filing")
	assert.Equal(t, dateonly.New(2025, 2, 1), rows[1].PeriodEndDate)
}

// A 13-period entity refuses M codes (its months are P1..P13).
func TestGenerator_13PeriodRefusesMCodes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, _, workflows, workflowTasks, entities := newGenerator()
	wf := recurringWorkflow() // M1, M2
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "13-period", FinancialYearEnd: strp("12-31"), FiscalWeekEndDay: "saturday", FiscalYearEndRule: "nearest"}
	workflows.On("GetById", ctx, "wf-1").Return(wf, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(entity, nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return([]workflowtasksdomain.WorkflowTask{filingTask()}, nil).Once()

	_, err := gen.PreviewWorkflow(ctx, "wf-1")
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Contains(t, err.Error(), `"M1"`)
}

// paymentOffset: M3 2025 ends 31 Mar → +1 month = 30 Apr → +10 days = 10 May
// (a Saturday) → next business day 12 May. The payment task (due 3 days
// before the payment deadline) lands on 9 May; the filing task stays on the
// filing deadline (15 Apr − 5d = 10 Apr).
func TestGenerator_PaymentOffsetRule(t *testing.T) {
	t.Parallel()
	wf := recurringWorkflow()
	wf.SelectedPeriods = workflowsdomain.Periods{"M3"}
	wf.ObligationTypeID = strp("ot-1")
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	obligation := &entityobligationsdomain.EntityObligation{
		ID: "eo-1", EntityID: "ent-1", ObligationTypeID: "ot-1", Periodicity: "monthly",
		DeadlineRule: entityobligationsdomain.DeadlineRule{
			Type:              "period_offset",
			FilingOffset:      &entityobligationsdomain.MonthDayOffset{Months: 0, Days: 15},
			PaymentOffset:     &entityobligationsdomain.MonthDayOffset{Months: 1, Days: 10},
			WeekendAdjustment: "next-business-day",
		},
	}
	file := filingTask()
	file.DueDateOffsetValue = 5
	pay := paymentTask()
	pay.DueDateOffsetValue = 3

	rows := previewWith(t, wf, entity, []workflowtasksdomain.WorkflowTask{file, pay}, obligation)
	require.Len(t, rows, 2)
	assert.Equal(t, dateonly.New(2025, 3, 31), rows[0].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 4, 15), rows[0].FilingDeadline)
	assert.Equal(t, dateonly.New(2025, 5, 12), rows[0].PaymentDeadline)
	assert.Equal(t, dateonly.New(2025, 4, 10), rows[0].DueDate)
	assert.Equal(t, "Pay", rows[1].Name)
	assert.Equal(t, dateonly.New(2025, 5, 12), rows[1].PaymentDeadline)
	assert.Equal(t, dateonly.New(2025, 5, 9), rows[1].DueDate)
}

// Without its own weekend adjustment the payment rule borrows the workflow's.
func TestGenerator_PaymentOffsetBorrowsWorkflowAdjustment(t *testing.T) {
	t.Parallel()
	wf := recurringWorkflow()
	wf.SelectedPeriods = workflowsdomain.Periods{"M3"}
	wf.ObligationTypeID = strp("ot-1")
	wf.DueDateRule.WeekendAdjustment = "prev-business-day"
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	obligation := &entityobligationsdomain.EntityObligation{
		DeadlineRule: entityobligationsdomain.DeadlineRule{PaymentOffset: &entityobligationsdomain.MonthDayOffset{Months: 1, Days: 10}},
	}
	rows := previewWith(t, wf, entity, []workflowtasksdomain.WorkflowTask{filingTask()}, obligation)
	assert.Equal(t, dateonly.New(2025, 5, 9), rows[0].PaymentDeadline, "Sat 10 May → Fri 9 May")
}

// paymentFixedDates: the first fixed MM-DD on/after the period end, no
// weekend adjustment; a period end past the last fixed date wraps into the
// next year.
func TestGenerator_PaymentFixedDatesRule(t *testing.T) {
	t.Parallel()
	wf := recurringWorkflow()
	wf.Periodicity = strp("quarterly")
	wf.SelectedPeriods = workflowsdomain.Periods{"Q1", "Q4"}
	wf.ObligationTypeID = strp("ot-1")
	wf.DueDateRule.WeekendAdjustment = "next-business-day"
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	obligation := &entityobligationsdomain.EntityObligation{
		DeadlineRule: entityobligationsdomain.DeadlineRule{
			Type: "fixed", FixedDates: []string{"04-30", "10-31"}, PaymentFixedDates: []string{"05-31", "11-30"},
		},
	}
	rows := previewWith(t, wf, entity, []workflowtasksdomain.WorkflowTask{filingTask()}, obligation)
	require.Len(t, rows, 2)
	assert.Equal(t, dateonly.New(2025, 3, 31), rows[0].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 5, 31), rows[0].PaymentDeadline, "31 May 2025 is a Saturday and stays")
	assert.Equal(t, dateonly.New(2025, 12, 31), rows[1].PeriodEndDate)
	assert.Equal(t, dateonly.New(2026, 5, 31), rows[1].PaymentDeadline, "wraps into the next year")
}

// An obligation with neither payment offset nor fixed dates → payment = filing.
func TestGenerator_PaymentSameAsFiling(t *testing.T) {
	t.Parallel()
	wf := recurringWorkflow()
	wf.ObligationTypeID = strp("ot-1")
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	obligation := &entityobligationsdomain.EntityObligation{
		DeadlineRule: entityobligationsdomain.DeadlineRule{Type: "period_offset", FilingOffset: &entityobligationsdomain.MonthDayOffset{Days: 20}},
	}
	rows := previewWith(t, wf, entity, []workflowtasksdomain.WorkflowTask{filingTask()}, obligation)
	for _, r := range rows {
		assert.Equal(t, r.FilingDeadline, r.PaymentDeadline)
	}
	// And with no obligation at all (resolver returns nil).
	rows = previewWith(t, wf, entity, []workflowtasksdomain.WorkflowTask{filingTask()}, nil)
	for _, r := range rows {
		assert.Equal(t, r.FilingDeadline, r.PaymentDeadline)
	}
}

// Overrides: paymentDeadline replaces the derived value (and moves a task
// referencing it); a periodEndDate override recomputes the payment deadline.
func TestGenerator_PaymentDeadlineOverrides(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	wf := recurringWorkflow()
	wf.SelectedPeriods = workflowsdomain.Periods{"M1", "M2"}
	wf.ObligationTypeID = strp("ot-1")
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	obligation := &entityobligationsdomain.EntityObligation{
		DeadlineRule: entityobligationsdomain.DeadlineRule{PaymentOffset: &entityobligationsdomain.MonthDayOffset{Months: 1}},
	}
	obligations := new(obligationResolverMock)
	obligations.On("FindByEntityAndType", ctx, "ent-1", "ot-1").Return(obligation, nil).Once()
	gen.WithObligations(obligations)

	pay := paymentTask()
	pay.DueDateOffsetValue = 2
	workflows.On("GetById", ctx, "wf-1").Return(wf, nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(0, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(entity, nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return([]workflowtasksdomain.WorkflowTask{pay}, nil).Once()
	workflows.On("Update", ctx, "wf-1", mock.Anything).Return(wf, nil).Once()

	var created []tidomain.CreateTaskInstanceInput
	instances.On("Create", ctx, mock.Anything).
		Run(func(args mock.Arguments) { created = append(created, args.Get(1).(tidomain.CreateTaskInstanceInput)) }).
		Return(&tidomain.TaskInstance{}, nil).Times(2)

	m1Payment := dateonly.New(2025, 3, 20)
	m2PeriodEnd := dateonly.New(2025, 2, 20)
	_, err := gen.StartWorkflow(ctx, "wf-1", workflowsdomain.TaskOverrides{
		"wt-pay_M1": {PaymentDeadline: &m1Payment},
		"wt-pay_M2": {PeriodEndDate: &m2PeriodEnd},
	})
	require.NoError(t, err)
	require.Len(t, created, 2)

	// M1: derived would be 28 Feb; override 20 Mar → due 18 Mar.
	assert.Equal(t, m1Payment, created[0].PaymentDeadline)
	assert.Equal(t, dateonly.New(2025, 3, 18), created[0].DueDate)
	// M2: period end moved to 20 Feb → payment 20 Mar → due 18 Mar.
	assert.Equal(t, m2PeriodEnd, created[1].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 3, 20), created[1].PaymentDeadline)
	assert.Equal(t, dateonly.New(2025, 3, 18), created[1].DueDate)
}

// A custom-calendar entity plans its own codes; a code that is not listed is a 400.
func TestGenerator_CustomCalendar(t *testing.T) {
	t.Parallel()
	wf := recurringWorkflow()
	wf.SelectedPeriods = workflowsdomain.Periods{"T2"}
	entity := &entitiesdomain.Entity{
		ID: "ent-1", FiscalCalendarPattern: "custom", FinancialYearEnd: strp("12-31"),
		CustomPeriods: entitiesdomain.CustomPeriods{
			{Code: "T1", Name: "Trimester 1", StartDate: "01-01", EndDate: "04-30"},
			{Code: "T2", Name: "Trimester 2", StartDate: "05-01", EndDate: "08-31"},
			{Code: "T3", Name: "Trimester 3", StartDate: "09-01", EndDate: "12-31"},
		},
	}
	rows := previewWith(t, wf, entity, []workflowtasksdomain.WorkflowTask{filingTask()}, nil)
	require.Len(t, rows, 1)
	assert.Equal(t, "T2", rows[0].PeriodCode)
	assert.Equal(t, dateonly.New(2025, 8, 31), rows[0].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 9, 15), rows[0].FilingDeadline)
}
