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

func standardEntity() *entitiesdomain.Entity {
	return &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
}

// Two templates, deliberately listed out of orderIndex order.
func twoTemplates() []workflowtasksdomain.WorkflowTask {
	return []workflowtasksdomain.WorkflowTask{
		{ID: "wt-prepare", WorkflowID: "wf-1", Name: "Prepare", TaskType: "preparation", RoleLabel: strp("Tax Manager"),
			DueDateReference: "filing_deadline", DueDateOffsetValue: 5, DueDateOffsetUnit: "days", DueDateOffsetDirection: "before", OrderIndex: 1},
		{ID: "wt-collect", WorkflowID: "wf-1", Name: "Collect", TaskType: "data_request", RoleLabel: strp("Tax Analyst"),
			DueDateReference: "period_end", DueDateOffsetValue: 2, DueDateOffsetUnit: "days", DueDateOffsetDirection: "after", OrderIndex: 0},
	}
}

func TestGenerator_PreviewFollowsSelectedPeriodOrderAndCreatesNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	wf := recurringWorkflow()
	wf.Name = "Monthly VAT"
	wf.SelectedPeriods = workflowsdomain.Periods{"M1", "M2", "M10"} // lexicographic would put M10 before M2

	workflows.On("GetById", ctx, "wf-1").Return(wf, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(standardEntity(), nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return(twoTemplates(), nil).Once()

	preview, err := gen.PreviewWorkflow(ctx, "wf-1")
	require.NoError(t, err)

	assert.Equal(t, "Monthly VAT", preview.WorkflowName)
	assert.Equal(t, 3, preview.TotalPeriods)
	assert.Equal(t, 2, preview.TaskTemplates)
	assert.Equal(t, 6, preview.TotalTasks)
	assert.Equal(t, []string{"Tax Analyst", "Tax Manager"}, preview.AssigneesImpacted)

	var order []string
	for _, task := range preview.Tasks {
		order = append(order, task.PeriodCode+":"+task.Name)
	}
	assert.Equal(t, []string{"M1:Collect", "M1:Prepare", "M2:Collect", "M2:Prepare", "M10:Collect", "M10:Prepare"}, order)

	// M1: period end Jan 31; filing Feb 15; Collect = period end + 2d; Prepare = filing - 5d.
	assert.Equal(t, dateonly.New(2025, 2, 2), preview.Tasks[0].DueDate)
	assert.Equal(t, dateonly.New(2025, 2, 10), preview.Tasks[1].DueDate)
	assert.Equal(t, "wt-collect", preview.Tasks[0].TemplateID)
	// M10: period end Oct 31; filing Nov 15; Prepare due Nov 10.
	assert.Equal(t, dateonly.New(2025, 10, 31), preview.Tasks[5].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 11, 10), preview.Tasks[5].DueDate)

	// A preview is a pure read: no instance creation, no idempotency lookup.
	instances.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	instances.AssertNotCalled(t, "CountByWorkflow", mock.Anything, mock.Anything)
}

func TestGenerator_StartAppliesOverrides(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	workflows.On("GetById", ctx, "wf-1").Return(recurringWorkflow(), nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(0, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(standardEntity(), nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return(twoTemplates(), nil).Once()
	workflows.On("Update", ctx, "wf-1", mock.MatchedBy(func(in workflowsdomain.UpdateWorkflowInput) bool {
		return in.Status == "active"
	})).Return(recurringWorkflow(), nil).Once()

	created := map[string]tidomain.CreateTaskInstanceInput{}
	instances.On("Create", ctx, mock.Anything).
		Run(func(args mock.Arguments) {
			in := args.Get(1).(tidomain.CreateTaskInstanceInput)
			created[workflowsdomain.OverrideKey(in.WorkflowTaskID, in.PeriodCode)] = in
		}).
		Return(&tidomain.TaskInstance{}, nil).Times(4)

	due := dateonly.New(2025, 2, 12)
	periodEnd := dateonly.New(2025, 2, 20)
	count, err := gen.StartWorkflow(ctx, "wf-1", workflowsdomain.TaskOverrides{
		"wt-prepare_M1": {DueDate: &due},
		"wt-collect_M2": {PeriodEndDate: &periodEnd},
	})
	require.NoError(t, err)
	assert.Equal(t, 4, count)

	// dueDate override moves only the due date.
	assert.Equal(t, due, created["wt-prepare_M1"].DueDate)
	assert.Equal(t, dateonly.New(2025, 1, 31), created["wt-prepare_M1"].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 2, 15), created["wt-prepare_M1"].FilingDeadline)
	// periodEndDate override recomputes filing (+15d) and due (+2d from period end).
	assert.Equal(t, periodEnd, created["wt-collect_M2"].PeriodEndDate)
	assert.Equal(t, dateonly.New(2025, 3, 7), created["wt-collect_M2"].FilingDeadline)
	assert.Equal(t, dateonly.New(2025, 2, 22), created["wt-collect_M2"].DueDate)
	// Siblings untouched.
	assert.Equal(t, dateonly.New(2025, 2, 2), created["wt-collect_M1"].DueDate)
	assert.Equal(t, dateonly.New(2025, 2, 28), created["wt-prepare_M2"].PeriodEndDate)
}

func TestGenerator_StartRejectsUnknownOverrideBeforeCreating(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	workflows.On("GetById", ctx, "wf-1").Return(recurringWorkflow(), nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(0, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(standardEntity(), nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return(twoTemplates(), nil).Once()

	due := dateonly.New(2025, 2, 12)
	_, err := gen.StartWorkflow(ctx, "wf-1", workflowsdomain.TaskOverrides{"bogus_M1": {DueDate: &due}})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Contains(t, err.Error(), "unknown task override: bogus_M1")
	instances.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	workflows.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
}
