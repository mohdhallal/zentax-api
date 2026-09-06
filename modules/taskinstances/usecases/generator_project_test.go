package usecases

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	tidomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// A project workflow: no entity, no periodicity, no periods — just an end date.
func projectWorkflow() *workflowsdomain.Workflow {
	return &workflowsdomain.Workflow{
		ID: "wf-p", Name: "TP audit", WorkflowCategory: "project",
		ProjectType: strp("audit_verification"), StartDate: strp("2025-05-01"), EndDate: strp("2025-06-30"),
		// A due-date rule on a project is inert: the end date IS the deadline.
		DueDateRule: workflowsdomain.DueDateRule{OffsetValue: 15, OffsetUnit: "days", OffsetDirection: "after"},
	}
}

// Three templates, deliberately listed out of orderIndex order, one per
// due-date reference so every reference is seen resolving to the end date.
func projectTemplates() []workflowtasksdomain.WorkflowTask {
	return []workflowtasksdomain.WorkflowTask{
		{ID: "wt-close", WorkflowID: "wf-p", Name: "Close file", TaskType: "other",
			DueDateReference: "period_end", DueDateOffsetValue: 5, DueDateOffsetUnit: "days", DueDateOffsetDirection: "after", OrderIndex: 2},
		{ID: "wt-pay", WorkflowID: "wf-p", Name: "Pay advisor", TaskType: "payment",
			DueDateReference: "payment_deadline", DueDateOffsetValue: 3, DueDateOffsetUnit: "days", DueDateOffsetDirection: "before", OrderIndex: 1},
		{ID: "wt-field", WorkflowID: "wf-p", Name: "Fieldwork", TaskType: "preparation", RoleLabel: strp("Tax Manager"),
			DueDateReference: "filing_deadline", DueDateOffsetValue: 10, DueDateOffsetUnit: "days", DueDateOffsetDirection: "before", OrderIndex: 0},
	}
}

// Start on a project: one instance per template under the single "PROJECT"
// period; period end = filing deadline = the workflow's end date; no payment
// deadline (a payment_deadline reference falls back to the filing deadline);
// due = end date ± template offset. The entity is never looked up, the workflow
// flips to active, and the audit entry counts one period.
func TestGenerator_ProjectStartCreatesOneInstancePerTemplate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	wf := projectWorkflow()
	workflows.On("GetById", ctx, "wf-p").Return(wf, nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-p").Return(0, nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-p").Return(projectTemplates(), nil).Once()
	workflows.On("Update", ctx, "wf-p", mock.MatchedBy(func(in workflowsdomain.UpdateWorkflowInput) bool {
		return in.Status == "active" && in.WorkflowCategory == "project" &&
			in.EndDate != nil && *in.EndDate == "2025-06-30" && in.EntityID == nil
	})).Return(wf, nil).Once()

	var created []tidomain.CreateTaskInstanceInput
	instances.On("Create", ctx, mock.Anything).
		Run(func(args mock.Arguments) { created = append(created, args.Get(1).(tidomain.CreateTaskInstanceInput)) }).
		Return(&tidomain.TaskInstance{}, nil).Times(3)

	count, err := gen.StartWorkflow(ctx, "wf-p", nil)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
	require.Len(t, created, 3)

	end := dateonly.New(2025, 6, 30)
	var names []string
	for _, in := range created {
		names = append(names, in.Name)
		assert.Equal(t, "wf-p", in.WorkflowID)
		assert.Equal(t, workflowsdomain.ProjectPeriodCode, in.PeriodCode)
		assert.Equal(t, end, in.PeriodEndDate)
		assert.Equal(t, end, in.FilingDeadline)
		assert.Nil(t, in.PaymentDeadline, "a project has no payment deadline")
	}
	assert.Equal(t, []string{"Fieldwork", "Pay advisor", "Close file"}, names, "template orderIndex order")
	assert.Equal(t, dateonly.New(2025, 6, 20), created[0].DueDate, "filing_deadline − 10d")
	assert.Equal(t, dateonly.New(2025, 6, 27), created[1].DueDate, "payment_deadline → end date − 3d")
	assert.Equal(t, dateonly.New(2025, 7, 5), created[2].DueDate, "period_end + 5d")
	assert.Equal(t, "wt-field", created[0].WorkflowTaskID)
	assert.Equal(t, 0, created[0].OrderIndex)

	entities.AssertNotCalled(t, "GetById", mock.Anything, mock.Anything)
	instances.AssertExpectations(t)
}

// Preview of a project is the same plan without writes: one period, the
// templates' role labels, null payment deadlines.
func TestGenerator_ProjectPreview(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	workflows.On("GetById", ctx, "wf-p").Return(projectWorkflow(), nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-p").Return(projectTemplates(), nil).Once()

	preview, err := gen.PreviewWorkflow(ctx, "wf-p")
	require.NoError(t, err)
	assert.Equal(t, "TP audit", preview.WorkflowName)
	assert.Equal(t, 1, preview.TotalPeriods)
	assert.Equal(t, 3, preview.TaskTemplates)
	assert.Equal(t, 3, preview.TotalTasks)
	assert.Equal(t, []string{"Tax Manager"}, preview.AssigneesImpacted)
	require.Len(t, preview.Tasks, 3)
	for _, task := range preview.Tasks {
		assert.Equal(t, workflowsdomain.ProjectPeriodCode, task.PeriodCode)
		assert.Equal(t, dateonly.New(2025, 6, 30), task.PeriodEndDate)
		assert.Equal(t, dateonly.New(2025, 6, 30), task.FilingDeadline)
		assert.Nil(t, task.PaymentDeadline)
	}
	assert.Equal(t, "wt-field", preview.Tasks[0].TemplateID)
	assert.Equal(t, dateonly.New(2025, 6, 20), preview.Tasks[0].DueDate)
	assert.Equal(t, dateonly.New(2025, 7, 5), preview.Tasks[2].DueDate)

	instances.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	instances.AssertNotCalled(t, "CountByWorkflow", mock.Anything, mock.Anything)
	entities.AssertNotCalled(t, "GetById", mock.Anything, mock.Anything)
}

// A project without an end date (absent, blank, or not a date) cannot be
// planned — preview and start share the 400 — and nothing is read or written.
func TestGenerator_ProjectNeedsAnEndDate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	for name, endDate := range map[string]*string{"absent": nil, "blank": strp("")} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gen, instances, workflows, workflowTasks, _ := newGenerator()
			wf := projectWorkflow()
			wf.EndDate = endDate
			workflows.On("GetById", ctx, "wf-p").Return(wf, nil).Twice()

			_, err := gen.PreviewWorkflow(ctx, "wf-p")
			assert.IsType(t, &apperrors.ValidationError{}, err)
			assert.EqualError(t, err, "project workflows need an end date")
			_, err = gen.StartWorkflow(ctx, "wf-p", nil)
			assert.EqualError(t, err, "project workflows need an end date")

			workflowTasks.AssertNotCalled(t, "ListByWorkflow", mock.Anything, mock.Anything)
			instances.AssertNotCalled(t, "CountByWorkflow", mock.Anything, mock.Anything)
			instances.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		})
	}

	t.Run("malformed", func(t *testing.T) {
		t.Parallel()
		gen, instances, workflows, _, _ := newGenerator()
		wf := projectWorkflow()
		wf.EndDate = strp("2025-13-40")
		workflows.On("GetById", ctx, "wf-p").Return(wf, nil).Once()

		_, err := gen.StartWorkflow(ctx, "wf-p", nil)
		assert.IsType(t, &apperrors.ValidationError{}, err)
		assert.Contains(t, err.Error(), "YYYY-MM-DD")
		instances.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})
}

// Start on a project is idempotent-guarded like a recurring one.
func TestGenerator_ProjectStartRefusesIfAlreadyStarted(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, _ := newGenerator()

	workflows.On("GetById", ctx, "wf-p").Return(projectWorkflow(), nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-p").Return(3, nil).Once()

	_, err := gen.StartWorkflow(ctx, "wf-p", nil)
	assert.IsType(t, &apperrors.ConflictError{}, err)
	workflowTasks.AssertNotCalled(t, "ListByWorkflow", mock.Anything, mock.Anything)
}

// Project overrides address "<templateId>_PROJECT": dueDate replaces the due
// date, periodEndDate moves that instance's end date (filing + due follow), a
// paymentDeadline override is refused, an unknown pair is refused — before
// anything is created.
func TestGenerator_ProjectOverrides(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("applied", func(t *testing.T) {
		t.Parallel()
		gen, instances, workflows, workflowTasks, _ := newGenerator()
		workflows.On("GetById", ctx, "wf-p").Return(projectWorkflow(), nil).Once()
		instances.On("CountByWorkflow", ctx, "wf-p").Return(0, nil).Once()
		workflowTasks.On("ListByWorkflow", ctx, "wf-p").Return(projectTemplates(), nil).Once()
		workflows.On("Update", ctx, "wf-p", mock.Anything).Return(projectWorkflow(), nil).Once()

		created := map[string]tidomain.CreateTaskInstanceInput{}
		instances.On("Create", ctx, mock.Anything).
			Run(func(args mock.Arguments) {
				in := args.Get(1).(tidomain.CreateTaskInstanceInput)
				created[in.WorkflowTaskID] = in
			}).
			Return(&tidomain.TaskInstance{}, nil).Times(3)

		due := dateonly.New(2025, 6, 15)
		end := dateonly.New(2025, 7, 15)
		count, err := gen.StartWorkflow(ctx, "wf-p", workflowsdomain.TaskOverrides{
			"wt-field_PROJECT": {DueDate: &due},
			"wt-close_PROJECT": {PeriodEndDate: &end},
		})
		require.NoError(t, err)
		assert.Equal(t, 3, count)

		assert.Equal(t, due, created["wt-field"].DueDate)
		assert.Equal(t, dateonly.New(2025, 6, 30), created["wt-field"].FilingDeadline, "dueDate override moves the due date only")
		assert.Equal(t, end, created["wt-close"].PeriodEndDate)
		assert.Equal(t, end, created["wt-close"].FilingDeadline)
		assert.Equal(t, dateonly.New(2025, 7, 20), created["wt-close"].DueDate, "recomputed from the moved end date")
		assert.Nil(t, created["wt-close"].PaymentDeadline)
		assert.Equal(t, dateonly.New(2025, 6, 27), created["wt-pay"].DueDate, "sibling untouched")
	})

	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		gen, instances, workflows, workflowTasks, _ := newGenerator()
		workflows.On("GetById", ctx, "wf-p").Return(projectWorkflow(), nil).Twice()
		instances.On("CountByWorkflow", ctx, "wf-p").Return(0, nil).Twice()
		workflowTasks.On("ListByWorkflow", ctx, "wf-p").Return(projectTemplates(), nil).Twice()

		payment := dateonly.New(2025, 6, 20)
		_, err := gen.StartWorkflow(ctx, "wf-p", workflowsdomain.TaskOverrides{"wt-pay_PROJECT": {PaymentDeadline: &payment}})
		assert.IsType(t, &apperrors.ValidationError{}, err)
		assert.EqualError(t, err, "task override wt-pay_PROJECT: project workflows have no payment deadline")

		_, err = gen.StartWorkflow(ctx, "wf-p", workflowsdomain.TaskOverrides{"wt-pay_M1": {DueDate: &payment}})
		assert.IsType(t, &apperrors.ValidationError{}, err)
		assert.EqualError(t, err, "unknown task override: wt-pay_M1")

		instances.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		workflows.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
	})
}
