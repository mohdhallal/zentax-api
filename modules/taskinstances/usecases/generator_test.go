package usecases

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	tidomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

func strp(s string) *string { return &s }

func recurringWorkflow() *workflowsdomain.Workflow {
	return &workflowsdomain.Workflow{
		ID:               "wf-1",
		WorkflowCategory: "recurring",
		EntityID:         strp("ent-1"),
		Periodicity:      strp("monthly"),
		FinancialYear:    strp("2025"),
		SelectedPeriods:  workflowsdomain.Periods{"M1", "M2"},
		DueDateRule: workflowsdomain.DueDateRule{
			Reference: "period_end", OffsetValue: 15, OffsetUnit: "days",
			OffsetDirection: "after", WeekendAdjustment: "none",
		},
	}
}

func newGenerator() (*Generator, *tidomain.TaskInstanceRepositoryMock, *workflowsdomain.WorkflowRepositoryMock, *workflowtasksdomain.WorkflowTaskRepositoryMock, *entitiesdomain.EntityRepositoryMock) {
	instances := new(tidomain.TaskInstanceRepositoryMock)
	workflows := new(workflowsdomain.WorkflowRepositoryMock)
	workflowTasks := new(workflowtasksdomain.WorkflowTaskRepositoryMock)
	entities := new(entitiesdomain.EntityRepositoryMock)
	return NewGenerator(instances, workflows, workflowTasks, entities), instances, workflows, workflowTasks, entities
}

func TestGenerator_ComputesDeadlines(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	wf := recurringWorkflow()
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	task := workflowtasksdomain.WorkflowTask{
		ID: "wt-1", WorkflowID: "wf-1", Name: "Prepare", TaskType: "preparation",
		DueDateReference: "filing_deadline", DueDateOffsetValue: 5, DueDateOffsetUnit: "days", DueDateOffsetDirection: "before",
	}

	workflows.On("GetById", ctx, "wf-1").Return(wf, nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(0, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(entity, nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return([]workflowtasksdomain.WorkflowTask{task}, nil).Once()
	// Start is the draft → active transition; everything else is re-submitted as is.
	workflows.On("Update", ctx, "wf-1", mock.MatchedBy(func(in workflowsdomain.UpdateWorkflowInput) bool {
		return in.Status == "active" && in.EntityID != nil && *in.EntityID == "ent-1" && len(in.SelectedPeriods) == 2
	})).Return(wf, nil).Once()

	var created []tidomain.CreateTaskInstanceInput
	instances.On("Create", ctx, mock.Anything).
		Run(func(args mock.Arguments) {
			created = append(created, args.Get(1).(tidomain.CreateTaskInstanceInput))
		}).
		Return(&tidomain.TaskInstance{}, nil).Times(2)

	count, err := gen.StartWorkflow(ctx, "wf-1", nil)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	require.Len(t, created, 2)

	// M1: period end Jan 31 2025; filing = +15d = Feb 15; task due = filing -5d = Feb 10.
	assert.Equal(t, "M1", created[0].PeriodCode)
	assert.Equal(t, dateonly.New(2025, 1, 31), created[0].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 2, 15), created[0].FilingDeadline)
	assert.Equal(t, dateonly.New(2025, 2, 10), created[0].DueDate)

	// M2: period end Feb 28 2025; filing = Mar 15; task due = Mar 10.
	assert.Equal(t, "M2", created[1].PeriodCode)
	assert.Equal(t, dateonly.New(2025, 2, 28), created[1].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 3, 15), created[1].FilingDeadline)
	assert.Equal(t, dateonly.New(2025, 3, 10), created[1].DueDate)

	instances.AssertExpectations(t)
}

func TestGenerator_FailsClosedOnCodeOutsideTheCalendar(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	wf := recurringWorkflow()
	wf.SelectedPeriods = workflowsdomain.Periods{"M1", "M13"}
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	task := workflowtasksdomain.WorkflowTask{ID: "wt-1", WorkflowID: "wf-1", Name: "Prepare", TaskType: "preparation", DueDateReference: "filing_deadline"}

	workflows.On("GetById", ctx, "wf-1").Return(wf, nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(0, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(entity, nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return([]workflowtasksdomain.WorkflowTask{task}, nil).Once()

	_, err := gen.StartWorkflow(ctx, "wf-1", nil)
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Contains(t, err.Error(), `"M13"`)
	instances.AssertNotCalled(t, "Create")
}

func TestGenerator_FailsClosedOnCustomCalendarWithoutPeriods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, _, entities := newGenerator()

	wf := recurringWorkflow()
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "custom", FinancialYearEnd: strp("12-31")}

	workflows.On("GetById", ctx, "wf-1").Return(wf, nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(0, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(entity, nil).Once()

	_, err := gen.StartWorkflow(ctx, "wf-1", nil)
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Contains(t, err.Error(), "no periods")
}

func TestGenerator_RefusesIfAlreadyStarted(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, _, _ := newGenerator()

	workflows.On("GetById", ctx, "wf-1").Return(recurringWorkflow(), nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(3, nil).Once()

	_, err := gen.StartWorkflow(ctx, "wf-1", nil)
	assert.IsType(t, &apperrors.ConflictError{}, err)
}

func TestGenerator_RejectsNonRecurring(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, _, workflows, _, _ := newGenerator()

	project := &workflowsdomain.Workflow{ID: "wf-1", WorkflowCategory: "project"}
	workflows.On("GetById", ctx, "wf-1").Return(project, nil).Once()

	_, err := gen.StartWorkflow(ctx, "wf-1", nil)
	assert.IsType(t, &apperrors.ValidationError{}, err)
}

func TestGenerator_WorkflowNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, _, workflows, _, _ := newGenerator()

	workflows.On("GetById", ctx, "missing").Return(nil, nil).Once()

	_, err := gen.StartWorkflow(ctx, "missing", nil)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
}
