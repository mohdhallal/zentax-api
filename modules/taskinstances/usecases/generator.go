package usecases

import (
	"context"
	"sort"
	"strconv"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	tidomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// Generator plans and materializes per-period task instances from a workflow's
// task templates (GET /workflows/{id}/preview, POST /workflows/{id}/start). It
// reads across modules (workflow, entity, templates) and writes task instances,
// all within the request's tenant-scoped transaction.
//
// Preview and start share planWorkflow, so what preview shows is exactly what
// start persists.
type Generator struct {
	instances     tidomain.TaskInstanceRepository
	workflows     workflowsdomain.WorkflowRepository
	workflowTasks workflowtasksdomain.WorkflowTaskRepository
	entities      entitiesdomain.EntityRepository
	authorizer    *authz.Authorizer
	audit         *audit.Recorder
}

var _ workflowsdomain.Starter = (*Generator)(nil)

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

// plannedInstance is one instance the planner would create, plus the template
// attributes the preview renders (role label) but the instance does not carry.
type plannedInstance struct {
	tidomain.CreateTaskInstanceInput
	RoleLabel *string
}

// workflowPlan is the shared output of planWorkflow.
type workflowPlan struct {
	workflow  *workflowsdomain.Workflow
	templates int
	instances []plannedInstance
}

// PreviewWorkflow computes the instances StartWorkflow would create without
// creating anything: no writes, no audit entry, and it works whether or not
// the workflow was already started (the idempotency guard applies to start
// only). It shares the same validations and messages as start.
func (g *Generator) PreviewWorkflow(ctx context.Context, workflowID string) (*workflowsdomain.WorkflowPreview, error) {
	// Previewing is a workflow:read on the workflow's entity subtree.
	if err := g.authorizer.EnsureWorkflow(ctx, workflowID, authz.WorkflowRead); err != nil {
		return nil, err
	}
	wf, err := g.loadRecurringWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	plan, err := g.planWorkflow(ctx, wf, nil)
	if err != nil {
		return nil, err
	}

	preview := &workflowsdomain.WorkflowPreview{
		WorkflowID:        wf.ID,
		WorkflowName:      wf.Name,
		TotalPeriods:      len(wf.SelectedPeriods),
		TaskTemplates:     plan.templates,
		TotalTasks:        len(plan.instances),
		AssigneesImpacted: []string{},
		Tasks:             make([]workflowsdomain.PreviewTask, 0, len(plan.instances)),
	}
	seen := map[string]bool{}
	for _, inst := range plan.instances {
		preview.Tasks = append(preview.Tasks, workflowsdomain.PreviewTask{
			TemplateID:     inst.WorkflowTaskID,
			PeriodCode:     inst.PeriodCode,
			Name:           inst.Name,
			TaskType:       inst.TaskType,
			AssigneeName:   inst.RoleLabel,
			DueDate:        inst.DueDate,
			PeriodEndDate:  inst.PeriodEndDate,
			FilingDeadline: inst.FilingDeadline,
			OrderIndex:     inst.OrderIndex,
		})
	}
	// Distinct non-empty role labels in first-seen template order. Instances
	// are period-major, so walk the first period's templates only (every period
	// carries the same templates in the same order).
	for _, inst := range plan.instances {
		if inst.PeriodCode != plan.instances[0].PeriodCode {
			break
		}
		if inst.RoleLabel == nil || *inst.RoleLabel == "" || seen[*inst.RoleLabel] {
			continue
		}
		seen[*inst.RoleLabel] = true
		preview.AssigneesImpacted = append(preview.AssigneesImpacted, *inst.RoleLabel)
	}
	return preview, nil
}

// StartWorkflow generates task instances for a recurring workflow (applying the
// caller's per-instance overrides) and returns the count created. It is
// idempotent-guarded (refuses if instances already exist) and fails closed on
// unsupported fiscal calendars rather than emit an approximate — and therefore
// wrong — deadline.
func (g *Generator) StartWorkflow(ctx context.Context, workflowID string, overrides workflowsdomain.TaskOverrides) (int, error) {
	// Starting a workflow is a workflow:write on the workflow's entity subtree.
	if err := g.authorizer.EnsureWorkflow(ctx, workflowID, authz.WorkflowWrite); err != nil {
		return 0, err
	}
	wf, err := g.loadRecurringWorkflow(ctx, workflowID)
	if err != nil {
		return 0, err
	}

	existing, err := g.instances.CountByWorkflow(ctx, workflowID)
	if err != nil {
		return 0, err
	}
	if existing > 0 {
		return 0, apperrors.NewConflict("workflow has already been started (task instances exist)")
	}

	plan, err := g.planWorkflow(ctx, wf, overrides)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, inst := range plan.instances {
		if _, err := g.instances.Create(ctx, inst.CreateTaskInstanceInput); err != nil {
			return 0, err
		}
		count++
	}

	// Starting is the draft → active lifecycle transition; same transaction, so
	// a failure here rolls the instances back too.
	if _, err := g.workflows.Update(ctx, wf.ID, activatedInput(wf)); err != nil {
		return 0, err
	}

	if err := g.audit.Record(ctx, "workflow.started", "workflow", workflowID,
		map[string]any{
			"instancesCreated": count,
			"periods":          len(wf.SelectedPeriods),
			"overrides":        len(overrides),
		}); err != nil {
		return 0, err
	}

	return count, nil
}

// loadRecurringWorkflow fetches the workflow and applies the validations shared
// by preview and start (same messages for both).
func (g *Generator) loadRecurringWorkflow(ctx context.Context, workflowID string) (*workflowsdomain.Workflow, error) {
	wf, err := g.workflows.GetById(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, apperrors.NewNotFound("workflow not found: " + workflowID)
	}
	if wf.WorkflowCategory != "recurring" {
		return nil, apperrors.NewValidation("only recurring workflows generate task instances")
	}
	if wf.EntityID == nil {
		return nil, apperrors.NewValidation("recurring workflow has no entity")
	}
	if wf.Periodicity == nil || *wf.Periodicity == "" {
		return nil, apperrors.NewValidation("recurring workflow has no periodicity")
	}
	if wf.FinancialYear == nil || *wf.FinancialYear == "" {
		return nil, apperrors.NewValidation("recurring workflow has no financial year")
	}
	if len(wf.SelectedPeriods) == 0 {
		return nil, apperrors.NewValidation("recurring workflow has no selected periods")
	}
	return wf, nil
}

// planWorkflow is the single planning function behind preview and start. It
// resolves the entity's fiscal calendar and the task templates, then computes
// one instance per selected period × template — in selectedPeriods order (not
// lexicographic), then template orderIndex — with the caller's overrides
// applied. An override key matching no (template, period) pair is a
// validation error.
func (g *Generator) planWorkflow(
	ctx context.Context, wf *workflowsdomain.Workflow, overrides workflowsdomain.TaskOverrides,
) (*workflowPlan, error) {
	entity, err := g.entities.GetById(ctx, *wf.EntityID)
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, apperrors.NewValidation("workflow entity not found in this tenant")
	}
	if !deadline.IsSupportedPattern(entity.FiscalCalendarPattern) {
		return nil, apperrors.NewValidation(
			"fiscal calendar pattern '" + entity.FiscalCalendarPattern +
				"' is not yet supported for deadline computation")
	}

	startMonth, err := deadline.FiscalYearStartMonth(strDeref(entity.FinancialYearEnd))
	if err != nil {
		return nil, apperrors.NewValidation(err.Error())
	}
	fyEndYear, err := strconv.Atoi(*wf.FinancialYear)
	if err != nil {
		return nil, apperrors.NewValidation("financial year must be a numeric year")
	}

	tasks, err := g.workflowTasks.ListByWorkflow(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, apperrors.NewValidation("workflow has no task templates")
	}
	// Natural step order within a period, regardless of repository ordering.
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].OrderIndex < tasks[j].OrderIndex })

	// Every override must address a real (template, period) pair.
	if len(overrides) > 0 {
		known := make(map[string]bool, len(tasks)*len(wf.SelectedPeriods))
		for _, periodCode := range wf.SelectedPeriods {
			for i := range tasks {
				known[workflowsdomain.OverrideKey(tasks[i].ID, periodCode)] = true
			}
		}
		unknown := make([]string, 0)
		for key := range overrides {
			if !known[key] {
				unknown = append(unknown, key)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown) // deterministic error message
			return nil, apperrors.NewValidation("unknown task override: " + unknown[0])
		}
	}

	plan := &workflowPlan{
		workflow:  wf,
		templates: len(tasks),
		instances: make([]plannedInstance, 0, len(tasks)*len(wf.SelectedPeriods)),
	}
	for _, periodCode := range wf.SelectedPeriods {
		basePeriodEnd, err := deadline.PeriodEndDate(periodCode, *wf.Periodicity, startMonth, fyEndYear)
		if err != nil {
			return nil, apperrors.NewValidation(err.Error())
		}

		for i := range tasks {
			task := tasks[i]
			ov := overrides[workflowsdomain.OverrideKey(task.ID, periodCode)]

			// A period-end override is per instance: it moves that one
			// instance's period end, and its filing deadline + due date are
			// recomputed from it via the workflow rule + template offset.
			periodEnd := basePeriodEnd
			if ov.PeriodEndDate != nil {
				periodEnd = *ov.PeriodEndDate
			}
			filing := deadline.ApplyWeekendAdjustment(
				deadline.ApplyOffset(periodEnd, wf.DueDateRule.OffsetValue, wf.DueDateRule.OffsetUnit, wf.DueDateRule.OffsetDirection),
				wf.DueDateRule.WeekendAdjustment,
			)

			ref := periodEnd
			if task.DueDateReference == "filing_deadline" {
				ref = filing
			}
			due := deadline.ApplyOffset(ref, task.DueDateOffsetValue, task.DueDateOffsetUnit, task.DueDateOffsetDirection)
			if ov.DueDate != nil {
				due = *ov.DueDate
			}

			plan.instances = append(plan.instances, plannedInstance{
				CreateTaskInstanceInput: tidomain.CreateTaskInstanceInput{
					WorkflowID:       wf.ID,
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
				},
				RoleLabel: task.RoleLabel,
			})
		}
	}

	return plan, nil
}

// activatedInput re-submits the workflow unchanged except for status=active
// (the repository's Update is a full replacement).
func activatedInput(wf *workflowsdomain.Workflow) workflowsdomain.UpdateWorkflowInput {
	return workflowsdomain.UpdateWorkflowInput{
		Name:             wf.Name,
		Description:      wf.Description,
		WorkflowCategory: wf.WorkflowCategory,
		ProjectType:      wf.ProjectType,
		FinancialYear:    wf.FinancialYear,
		Periodicity:      wf.Periodicity,
		SelectedPeriods:  wf.SelectedPeriods,
		EntityID:         wf.EntityID,
		ObligationTypeID: wf.ObligationTypeID,
		DueDateRule:      wf.DueDateRule,
		StartDate:        wf.StartDate,
		EndDate:          wf.EndDate,
		TasksSequential:  wf.TasksSequential,
		Status:           "active",
	}
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// WithAudit injects the audit recorder (ADR-0008); nil-safe, chainable.
func (g *Generator) WithAudit(r *audit.Recorder) *Generator {
	g.audit = r
	return g
}
