package usecases

import (
	"context"
	"sort"
	"strconv"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	tidomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// Workflow categories the planner knows.
const (
	categoryRecurring = "recurring"
	categoryProject   = "project"
)

// Generator plans and materializes task instances from a workflow's task
// templates (GET /workflows/{id}/preview, POST /workflows/{id}/start). It
// reads across modules (workflow, entity, templates, entity obligation) and
// writes task instances, all within the request's tenant-scoped transaction.
//
// Preview and start share planWorkflow, so what preview shows is exactly what
// start persists. A recurring workflow yields one instance per selected period
// × template through the entity's fiscal calendar (ADR-0023); a project
// workflow yields one instance per template under the single "PROJECT"
// period, its endDate being period end and filing deadline alike.
type Generator struct {
	instances     tidomain.TaskInstanceRepository
	workflows     workflowsdomain.WorkflowRepository
	workflowTasks workflowtasksdomain.WorkflowTaskRepository
	entities      entitiesdomain.EntityRepository
	obligations   tidomain.ObligationResolver // optional; nil = payment deadline = filing deadline
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

// WithObligations injects the entity-obligation lookup the payment deadline is
// derived from (ADR-0023 §5). Optional and nil-safe: without it every payment
// deadline equals the filing deadline.
func (g *Generator) WithObligations(r tidomain.ObligationResolver) *Generator {
	g.obligations = r
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
	periods   int // period codes planned: len(selectedPeriods), or 1 for a project
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
	wf, err := g.loadWorkflow(ctx, workflowID)
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
		TotalPeriods:      plan.periods,
		TaskTemplates:     plan.templates,
		TotalTasks:        len(plan.instances),
		AssigneesImpacted: []string{},
		Tasks:             make([]workflowsdomain.PreviewTask, 0, len(plan.instances)),
	}
	seen := map[string]bool{}
	for _, inst := range plan.instances {
		preview.Tasks = append(preview.Tasks, workflowsdomain.PreviewTask{
			TemplateID:      inst.WorkflowTaskID,
			PeriodCode:      inst.PeriodCode,
			Name:            inst.Name,
			TaskType:        inst.TaskType,
			AssigneeName:    inst.RoleLabel,
			DueDate:         inst.DueDate,
			PeriodEndDate:   inst.PeriodEndDate,
			FilingDeadline:  inst.FilingDeadline,
			PaymentDeadline: inst.PaymentDeadline,
			OrderIndex:      inst.OrderIndex,
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

// StartWorkflow generates the workflow's task instances (applying the caller's
// per-instance overrides) and returns the count created. It is
// idempotent-guarded (refuses if instances already exist) and fails closed on
// anything the calendar engine cannot compute rather than emit an approximate
// — and therefore wrong — deadline.
func (g *Generator) StartWorkflow(ctx context.Context, workflowID string, overrides workflowsdomain.TaskOverrides) (int, error) {
	// Starting a workflow is a workflow:write on the workflow's entity subtree.
	if err := g.authorizer.EnsureWorkflow(ctx, workflowID, authz.WorkflowWrite); err != nil {
		return 0, err
	}
	wf, err := g.loadWorkflow(ctx, workflowID)
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
			"periods":          plan.periods,
			"overrides":        len(overrides),
		}); err != nil {
		return 0, err
	}

	return count, nil
}

// loadWorkflow fetches the workflow and applies the validations shared by
// preview and start (same messages for both), per category: a recurring
// workflow needs its entity, periodicity, financial year and periods; a project
// workflow needs its end date — the one deadline its instances hang off.
func (g *Generator) loadWorkflow(ctx context.Context, workflowID string) (*workflowsdomain.Workflow, error) {
	wf, err := g.workflows.GetById(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, apperrors.NewNotFound("workflow not found: " + workflowID)
	}
	switch wf.WorkflowCategory {
	case categoryRecurring:
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
	case categoryProject:
		if wf.EndDate == nil || *wf.EndDate == "" {
			return nil, apperrors.NewValidation("project workflows need an end date")
		}
		if _, err := dateonly.Parse(*wf.EndDate); err != nil {
			return nil, apperrors.NewValidation("project workflow end date must be a YYYY-MM-DD date")
		}
	default:
		return nil, apperrors.NewValidation("only recurring and project workflows generate task instances")
	}
	return wf, nil
}

// planWorkflow is the single planning function behind preview and start,
// dispatching on the workflow category.
func (g *Generator) planWorkflow(
	ctx context.Context, wf *workflowsdomain.Workflow, overrides workflowsdomain.TaskOverrides,
) (*workflowPlan, error) {
	if wf.WorkflowCategory == categoryProject {
		return g.planProject(ctx, wf, overrides)
	}
	return g.planRecurring(ctx, wf, overrides)
}

// planRecurring resolves the entity's fiscal calendar (ADR-0023 — every
// pattern, through the same engine GET /entities/{id}/periods uses), the
// payment rule of the entity obligation and the task templates, then computes
// one instance per selected period × template — in selectedPeriods order (not
// lexicographic), then template orderIndex — with the caller's overrides
// applied. An override key matching no (template, period) pair, or a period
// code outside the entity's calendar, is a validation error.
func (g *Generator) planRecurring(
	ctx context.Context, wf *workflowsdomain.Workflow, overrides workflowsdomain.TaskOverrides,
) (*workflowPlan, error) {
	entity, err := g.entities.GetById(ctx, *wf.EntityID)
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, apperrors.NewValidation("workflow entity not found in this tenant")
	}
	fyEndYear, err := strconv.Atoi(*wf.FinancialYear)
	if err != nil {
		return nil, apperrors.NewValidation("financial year must be a numeric year")
	}
	cal, err := entitiesdomain.CalendarFor(entity, fyEndYear)
	if err != nil {
		return nil, apperrors.NewValidation(err.Error())
	}

	tasks, err := g.loadTemplates(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	if err := validateOverrides(overrides, tasks, wf.SelectedPeriods); err != nil {
		return nil, err
	}

	// The payment rule comes from the entity obligation linking the workflow's
	// entity and obligation type (ADR-0023 §5); none → payment = filing.
	var rule *entityobligationsdomain.DeadlineRule
	if g.obligations != nil && wf.ObligationTypeID != nil && *wf.ObligationTypeID != "" {
		eo, err := g.obligations.FindByEntityAndType(ctx, *wf.EntityID, *wf.ObligationTypeID)
		if err != nil {
			return nil, err
		}
		if eo != nil {
			rule = &eo.DeadlineRule
		}
	}

	plan := &workflowPlan{
		workflow:  wf,
		periods:   len(wf.SelectedPeriods),
		templates: len(tasks),
		instances: make([]plannedInstance, 0, len(tasks)*len(wf.SelectedPeriods)),
	}
	for _, periodCode := range wf.SelectedPeriods {
		period, err := cal.Period(periodCode, *wf.Periodicity)
		if err != nil {
			return nil, apperrors.NewValidation(err.Error())
		}

		for i := range tasks {
			task := tasks[i]
			ov := overrides[workflowsdomain.OverrideKey(task.ID, periodCode)]

			// A period-end override is per instance: it moves that one
			// instance's period end, and its filing deadline, payment deadline
			// and due date are recomputed from it via the rules + template offset.
			periodEnd := period.End
			if ov.PeriodEndDate != nil {
				periodEnd = *ov.PeriodEndDate
			}
			filing := deadline.ApplyWeekendAdjustment(
				deadline.ApplyOffset(periodEnd, wf.DueDateRule.OffsetValue, wf.DueDateRule.OffsetUnit, wf.DueDateRule.OffsetDirection),
				wf.DueDateRule.WeekendAdjustment,
			)
			payment, err := paymentDeadline(periodEnd, filing, rule, wf.DueDateRule.WeekendAdjustment)
			if err != nil {
				return nil, apperrors.NewValidation(err.Error())
			}
			if ov.PaymentDeadline != nil {
				payment = *ov.PaymentDeadline
			}

			ref := periodEnd
			switch task.DueDateReference {
			case "filing_deadline":
				ref = filing
			case "payment_deadline":
				ref = payment
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
					PaymentDeadline:  &payment,
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

// planProject computes a project workflow's instances: ONE per template (in
// orderIndex order) under the single ProjectPeriodCode period. The workflow's
// endDate is the project's period end and filing deadline alike; there is no
// payment deadline (NULL), so a template referencing payment_deadline falls
// back to the filing deadline — every reference resolves to the end date, and
// the task due date is that date ± the template offset. The workflow's
// dueDateRule and the entity's calendar play no part (a project has no fiscal
// period), and the entity itself stays optional. Overrides address
// "<templateId>_PROJECT": dueDate replaces the due date; periodEndDate moves
// that instance's end date (its filing deadline and due date follow); a
// paymentDeadline override is refused — there is nothing for it to replace.
func (g *Generator) planProject(
	ctx context.Context, wf *workflowsdomain.Workflow, overrides workflowsdomain.TaskOverrides,
) (*workflowPlan, error) {
	end, err := dateonly.Parse(*wf.EndDate) // validated by loadWorkflow
	if err != nil {
		return nil, apperrors.NewValidation("project workflow end date must be a YYYY-MM-DD date")
	}
	tasks, err := g.loadTemplates(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	if err := validateOverrides(overrides, tasks, []string{workflowsdomain.ProjectPeriodCode}); err != nil {
		return nil, err
	}

	plan := &workflowPlan{
		workflow:  wf,
		periods:   1,
		templates: len(tasks),
		instances: make([]plannedInstance, 0, len(tasks)),
	}
	for i := range tasks {
		task := tasks[i]
		key := workflowsdomain.OverrideKey(task.ID, workflowsdomain.ProjectPeriodCode)
		ov := overrides[key]
		if ov.PaymentDeadline != nil {
			return nil, apperrors.NewValidation("task override " + key + ": project workflows have no payment deadline")
		}

		periodEnd := end
		if ov.PeriodEndDate != nil {
			periodEnd = *ov.PeriodEndDate
		}
		filing := periodEnd
		due := deadline.ApplyOffset(filing, task.DueDateOffsetValue, task.DueDateOffsetUnit, task.DueDateOffsetDirection)
		if ov.DueDate != nil {
			due = *ov.DueDate
		}

		plan.instances = append(plan.instances, plannedInstance{
			CreateTaskInstanceInput: tidomain.CreateTaskInstanceInput{
				WorkflowID:       wf.ID,
				WorkflowTaskID:   task.ID,
				PeriodCode:       workflowsdomain.ProjectPeriodCode,
				Name:             task.Name,
				Description:      task.Description,
				TaskType:         task.TaskType,
				DueDate:          due,
				PeriodEndDate:    periodEnd,
				FilingDeadline:   filing,
				PaymentDeadline:  nil,
				ApprovalRequired: task.ApprovalRequired,
				OrderIndex:       task.OrderIndex,
				DataTemplateID:   task.DataTemplateID,
			},
			RoleLabel: task.RoleLabel,
		})
	}
	return plan, nil
}

// loadTemplates lists the workflow's task templates in natural step order
// (orderIndex, regardless of repository ordering); none is a validation error.
func (g *Generator) loadTemplates(ctx context.Context, workflowID string) ([]workflowtasksdomain.WorkflowTask, error) {
	tasks, err := g.workflowTasks.ListByWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, apperrors.NewValidation("workflow has no task templates")
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].OrderIndex < tasks[j].OrderIndex })
	return tasks, nil
}

// validateOverrides checks that every override addresses a real (template,
// period) pair — the keys the preview lists — before anything is planned.
func validateOverrides(overrides workflowsdomain.TaskOverrides, tasks []workflowtasksdomain.WorkflowTask, periodCodes []string) error {
	if len(overrides) == 0 {
		return nil
	}
	known := make(map[string]bool, len(tasks)*len(periodCodes))
	for _, periodCode := range periodCodes {
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
		return apperrors.NewValidation("unknown task override: " + unknown[0])
	}
	return nil
}

// paymentDeadline derives the payment deadline of one period (ADR-0023 §5):
//   - rule.PaymentOffset set → period end + months (month-end clamped) + days,
//     then weekend adjustment (the rule's own, else the workflow rule's);
//   - else rule.PaymentFixedDates non-empty → the first MM-DD on/after the
//     period end (the period end's year, then the next), no weekend adjustment;
//   - else (or no obligation at all) → the filing deadline.
func paymentDeadline(
	periodEnd, filing dateonly.Date, rule *entityobligationsdomain.DeadlineRule, fallbackAdjustment string,
) (dateonly.Date, error) {
	if rule == nil {
		return filing, nil
	}
	switch {
	case rule.PaymentOffset != nil:
		adjustment := rule.WeekendAdjustment
		if adjustment == "" {
			adjustment = fallbackAdjustment
		}
		raw := deadline.ApplyMonthDayOffset(periodEnd, rule.PaymentOffset.Months, rule.PaymentOffset.Days)
		return deadline.ApplyWeekendAdjustment(raw, adjustment), nil
	case len(rule.PaymentFixedDates) > 0:
		return deadline.FirstFixedDateOnOrAfter(periodEnd, rule.PaymentFixedDates)
	default:
		return filing, nil
	}
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

// WithAudit injects the audit recorder (ADR-0008); nil-safe, chainable.
func (g *Generator) WithAudit(r *audit.Recorder) *Generator {
	g.audit = r
	return g
}
