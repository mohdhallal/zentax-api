package usecases

import (
	"context"
	"strconv"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	tidomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// Generator materializes per-period task instances from a workflow's task
// templates (POST /workflows/{id}/start). It reads across modules (workflow,
// entity, templates) and writes task instances, all within the request's
// tenant-scoped transaction.
type Generator struct {
	instances     tidomain.TaskInstanceRepository
	workflows     workflowsdomain.WorkflowRepository
	workflowTasks workflowtasksdomain.WorkflowTaskRepository
	entities      entitiesdomain.EntityRepository
	authorizer    *authz.Authorizer
}

func NewGenerator(
	instances tidomain.TaskInstanceRepository,
	workflows workflowsdomain.WorkflowRepository,
	workflowTasks workflowtasksdomain.WorkflowTaskRepository,
	entities entitiesdomain.EntityRepository,
	authorizer ...*authz.Authorizer,
) *Generator {
	g := &Generator{
		instances:     instances,
		workflows:     workflows,
		workflowTasks: workflowTasks,
		entities:      entities,
	}
	if len(authorizer) > 0 {
		g.authorizer = authorizer[0]
	}
	return g
}

// StartWorkflow generates task instances for a recurring workflow and returns
// the count created. It is idempotent-guarded (refuses if instances already
// exist) and fails closed on unsupported fiscal calendars rather than emit an
// approximate — and therefore wrong — deadline.
func (g *Generator) StartWorkflow(ctx context.Context, workflowID string) (int, error) {
	// Starting a workflow is a workflow:write on the workflow's entity subtree.
	if err := g.authorizer.EnsureWorkflow(ctx, workflowID, authz.WorkflowWrite); err != nil {
		return 0, err
	}
	wf, err := g.workflows.GetById(ctx, workflowID)
	if err != nil {
		return 0, err
	}
	if wf == nil {
		return 0, apperrors.NewNotFound("workflow not found: " + workflowID)
	}
	if wf.WorkflowCategory != "recurring" {
		return 0, apperrors.NewValidation("only recurring workflows generate task instances")
	}
	if wf.EntityID == nil {
		return 0, apperrors.NewValidation("recurring workflow has no entity")
	}
	if wf.Periodicity == nil || *wf.Periodicity == "" {
		return 0, apperrors.NewValidation("recurring workflow has no periodicity")
	}
	if wf.FinancialYear == nil || *wf.FinancialYear == "" {
		return 0, apperrors.NewValidation("recurring workflow has no financial year")
	}
	if len(wf.SelectedPeriods) == 0 {
		return 0, apperrors.NewValidation("recurring workflow has no selected periods")
	}

	existing, err := g.instances.CountByWorkflow(ctx, workflowID)
	if err != nil {
		return 0, err
	}
	if existing > 0 {
		return 0, apperrors.NewConflict("workflow has already been started (task instances exist)")
	}

	entity, err := g.entities.GetById(ctx, *wf.EntityID)
	if err != nil {
		return 0, err
	}
	if entity == nil {
		return 0, apperrors.NewValidation("workflow entity not found in this tenant")
	}
	if !deadline.IsSupportedPattern(entity.FiscalCalendarPattern) {
		return 0, apperrors.NewValidation(
			"fiscal calendar pattern '" + entity.FiscalCalendarPattern +
				"' is not yet supported for deadline computation")
	}

	startMonth, err := deadline.FiscalYearStartMonth(strDeref(entity.FinancialYearEnd))
	if err != nil {
		return 0, apperrors.NewValidation(err.Error())
	}
	fyEndYear, err := strconv.Atoi(*wf.FinancialYear)
	if err != nil {
		return 0, apperrors.NewValidation("financial year must be a numeric year")
	}

	tasks, err := g.workflowTasks.ListByWorkflow(ctx, workflowID)
	if err != nil {
		return 0, err
	}
	if len(tasks) == 0 {
		return 0, apperrors.NewValidation("workflow has no task templates")
	}

	count := 0
	for _, periodCode := range wf.SelectedPeriods {
		periodEnd, err := deadline.PeriodEndDate(periodCode, *wf.Periodicity, startMonth, fyEndYear)
		if err != nil {
			return 0, apperrors.NewValidation(err.Error())
		}
		filing := deadline.ApplyWeekendAdjustment(
			deadline.ApplyOffset(periodEnd, wf.DueDateRule.OffsetValue, wf.DueDateRule.OffsetUnit, wf.DueDateRule.OffsetDirection),
			wf.DueDateRule.WeekendAdjustment,
		)

		for i := range tasks {
			task := tasks[i]
			ref := periodEnd
			if task.DueDateReference == "filing_deadline" {
				ref = filing
			}
			due := deadline.ApplyOffset(ref, task.DueDateOffsetValue, task.DueDateOffsetUnit, task.DueDateOffsetDirection)

			if _, err := g.instances.Create(ctx, tidomain.CreateTaskInstanceInput{
				WorkflowID:       workflowID,
				WorkflowTaskID:   task.ID,
				PeriodCode:       periodCode,
				Name:             task.Name,
				Description:      task.Description,
				TaskType:         task.TaskType,
				DueDate:          due,
				PeriodEndDate:    periodEnd,
				FilingDeadline:   filing,
				ApprovalRequired: task.ApprovalRequired,
				OrderIndex:       task.OrderIndex,
				DataTemplateID:   task.DataTemplateID,
			}); err != nil {
				return 0, err
			}
			count++
		}
	}

	return count, nil
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
