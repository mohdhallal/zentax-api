package main

// The oracle: an independent recomputation of everything the demo dataset
// claims, from seed/demo/dataset.json alone.
//
// It exists to answer "what must the live API return?" WITHOUT asking the
// live API, so a wrong report can never agree with a wrong expectation. Two
// rules follow from that and are enforced by review, not by the compiler:
//
//  1. Nothing here imports modules/reports. The classification rule, the
//     traffic-light rule, the figure extraction and every aggregation are
//     re-derived from ADR-0021 / the dataset's $schemaNotes, not shared with
//     the SQL that produces the numbers under test.
//  2. Dates come from shared/deadline — the engine itself. That is deliberate:
//     the oracle is not a second date engine, so an engine regression would
//     move both sides at once. verify therefore also diffs every planned
//     instance against GET /workflows/{id}/preview (the engine as the API
//     exposes it) and, for started workflows, against the persisted
//     instances — an engine change that the dataset does not expect shows up
//     as an instance-level difference there.
//
// Everything in this file is package-level-prefixed with "oracle" so package
// main can be split across files owned by different authors without a name
// clash.

import (
	"fmt"
	"strconv"
	"time"
	_ "time/tzdata" // the binary must resolve tenant zones on any host

	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// Compliance classifications, as ADR-0021 rule 5 names them.
const (
	oracleOnTime = "on_time"
	oracleLate   = "late"
	oracleMissed = "missed"
	oracleNotDue = "not_due"
)

// Heatmap cell colours.
const (
	oracleCellGreen = "green"
	oracleCellAmber = "amber"
	oracleCellRed   = "red"
	oracleCellGrey  = "grey"
)

// Task-instance statuses the reports count.
const (
	oracleStatusNotStarted      = "not_started"
	oracleStatusInProgress      = "in_progress"
	oracleStatusInReview        = "in_review"
	oracleStatusPendingApproval = "pending_approval"
	oracleStatusCompleted       = "completed"
	oracleStatusBlocked         = "blocked"
)

// Workflow statuses that take part in the compliance / financial reports.
const (
	oracleWorkflowActive    = "active"
	oracleWorkflowCompleted = "completed"
)

// oracleUnknownKey is the group key the reports use when an entity /
// obligation type is absent (COALESCE(..., 'unknown')).
const oracleUnknownKey = "unknown"

// oracleWorld is the whole dataset, recomputed for one "today" per tenant.
type oracleWorld struct {
	spec    *spec.Spec
	tenants []*oracleTenant
	byKey   map[string]*oracleTenant
}

// oracleTenant is one tenant's recomputed world.
type oracleTenant struct {
	spec spec.Tenant
	// zone is the tenant's IANA zone — the one "day" every rule uses.
	zone *time.Location
	// today is the tenant's civil date (or the --today override).
	today dateonly.Date
	// todayOverridden records that today did not come from the clock.
	todayOverridden bool

	workflows []*oracleWorkflow
	byWfKey   map[string]*oracleWorkflow
	// instances are every materialized instance, workflow-major then in
	// generation order (period, then template orderIndex).
	instances []*oracleInstance
}

// oracleWorkflow is one workflow, its plan and (when started) its instances.
type oracleWorkflow struct {
	spec spec.Workflow
	// status is the status the workflow ends the seed run in.
	status string
	// started says whether POST /workflows/{id}/start ran (instances exist).
	started bool
	// participates is the compliance / financial report rule: active or
	// completed AND recurring.
	participates bool

	entity     *spec.Entity
	obligation *spec.ObligationType
	// obligationRule is the entity obligation resolved the way the generator
	// resolves it: by (entity, obligationType), not by the workflow's own
	// entityObligation key.
	obligationRule *spec.DeadlineRule

	// planned is what GET /workflows/{id}/preview must return (whether or not
	// the workflow was started); instances is planned once start ran.
	planned   []*oracleInstance
	instances []*oracleInstance
}

// oracleInstance is one task instance: the dates the engine computes plus the
// state the seed run leaves it in.
type oracleInstance struct {
	workflow *oracleWorkflow
	tenant   *oracleTenant

	TemplateKey      string
	Name             string
	TaskType         string
	RoleLabel        string
	OrderIndex       int
	ApprovalRequired bool
	DataTemplate     string // "" = none

	PeriodCode      string
	PeriodEnd       dateonly.Date
	FilingDeadline  dateonly.Date
	PaymentDeadline *dateonly.Date
	DueDate         dateonly.Date

	Status        string
	Via           string
	AssigneeKey   string
	AssigneeName  string
	CompletedOn   dateonly.Date // the civil completion date in the tenant zone
	CompletedAt   time.Time     // the instant the escape hatch must write (UTC)
	SubmittedOn   dateonly.Date
	TaxData       map[string]any
	TaxDataStatus string
	Notes         string
}

// oracleWorkflowKey / oracleEntityKey … are the spec keys the diff speaks in.
func (i *oracleInstance) workflowKey() string { return i.workflow.spec.Key }

func (i *oracleInstance) entityKey() string {
	if i.workflow.entity == nil {
		return ""
	}
	return i.workflow.entity.Key
}

func (i *oracleInstance) obligationKey() string {
	if i.workflow.obligation == nil {
		return ""
	}
	return i.workflow.obligation.Key
}

// taxType is obligation_types.template ("" when there is no obligation type).
func (i *oracleInstance) taxType() string {
	if i.workflow.obligation == nil {
		return ""
	}
	return i.workflow.obligation.Template
}

func (i *oracleInstance) entityName() string {
	if i.workflow.entity == nil {
		return "Unknown Entity"
	}
	return i.workflow.entity.Name
}

func (i *oracleInstance) country() string {
	if i.workflow.entity == nil {
		return ""
	}
	return i.workflow.entity.Country
}

func (i *oracleInstance) obligationName() string {
	if i.workflow.obligation == nil {
		return "Unknown Obligation"
	}
	return i.workflow.obligation.Name
}

// financialObligationName is the label the tax-financial report uses for a
// missing obligation type ("Unknown"), which differs from compliance-status'
// "Unknown Obligation".
func (i *oracleInstance) financialObligationName() string {
	if i.workflow.obligation == nil {
		return "Unknown"
	}
	return i.workflow.obligation.Name
}

func (i *oracleInstance) obligationCode() string {
	if i.workflow.obligation == nil {
		return ""
	}
	return i.workflow.obligation.Code
}

func (i *oracleInstance) isCompleted() bool { return i.Status == oracleStatusCompleted }

// ref is the human handle a difference names an instance by.
func (i *oracleInstance) ref() string {
	return fmt.Sprintf("%s/%s/%s", i.workflowKey(), i.PeriodCode, i.TemplateKey)
}

// ---------------------------------------------------------------------------
// Building the world
// ---------------------------------------------------------------------------

// buildOracleWorld recomputes every tenant from the spec. `now` is the instant
// each tenant's civil today is read at (its own zone); a non-zero
// `todayOverride` replaces that date for every tenant (the --today what-if).
func buildOracleWorld(s *spec.Spec, now time.Time, todayOverride dateonly.Date) (*oracleWorld, error) {
	w := &oracleWorld{spec: s, byKey: make(map[string]*oracleTenant, len(s.Tenants))}
	for i := range s.Tenants {
		t, err := buildOracleTenant(s, s.Tenants[i], now, todayOverride)
		if err != nil {
			return nil, err
		}
		w.tenants = append(w.tenants, t)
		w.byKey[t.spec.Key] = t
	}
	return w, nil
}

func buildOracleTenant(s *spec.Spec, st spec.Tenant, now time.Time, todayOverride dateonly.Date) (*oracleTenant, error) {
	zone, err := time.LoadLocation(st.Timezone)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: load timezone %q: %w", st.Key, st.Timezone, err)
	}
	t := &oracleTenant{
		spec:    st,
		zone:    zone,
		today:   dateonly.FromTime(now.In(zone)),
		byWfKey: make(map[string]*oracleWorkflow, len(st.Workflows)),
	}
	if !todayOverride.IsZero() {
		t.today = todayOverride
		t.todayOverridden = true
	}
	for i := range st.Workflows {
		ow, err := t.buildWorkflow(s, st.Workflows[i])
		if err != nil {
			return nil, err
		}
		t.workflows = append(t.workflows, ow)
		t.byWfKey[ow.spec.Key] = ow
		t.instances = append(t.instances, ow.instances...)
	}
	return t, nil
}

func (t *oracleTenant) buildWorkflow(s *spec.Spec, sw spec.Workflow) (*oracleWorkflow, error) {
	w := &oracleWorkflow{
		spec:    sw,
		status:  sw.Lifecycle.FinalStatus,
		started: sw.Lifecycle.Start,
	}
	w.participates = sw.Category == spec.CategoryRecurring &&
		(w.status == oracleWorkflowActive || w.status == oracleWorkflowCompleted)

	if sw.Entity != nil {
		e, ok := t.spec.Entity(*sw.Entity)
		if !ok {
			return nil, fmt.Errorf("workflow %s: unknown entity %q", sw.Key, *sw.Entity)
		}
		w.entity = &e
	}
	if sw.ObligationType != nil {
		ot, ok := t.spec.ObligationType(*sw.ObligationType)
		if !ok {
			return nil, fmt.Errorf("workflow %s: unknown obligation type %q", sw.Key, *sw.ObligationType)
		}
		w.obligation = &ot
	}
	// The generator resolves the payment rule by (entity, obligation type) —
	// the pair, never the workflow's entityObligation field — so the oracle
	// must too, or a workflow that omits the field would get the wrong rule.
	if w.entity != nil && w.obligation != nil {
		for _, eo := range t.spec.EntityObligations {
			if eo.Entity == w.entity.Key && eo.ObligationType == w.obligation.Key {
				rule := eo.DeadlineRule
				w.obligationRule = &rule
				break
			}
		}
	}

	planned, err := t.planWorkflow(w)
	if err != nil {
		return nil, err
	}
	w.planned = planned
	if !w.started {
		return w, nil
	}
	w.instances = planned
	if err := t.applyDeviations(s, w); err != nil {
		return nil, err
	}
	return w, nil
}

// planWorkflow computes the instances start would create — the same plan
// GET /workflows/{id}/preview returns: period-major (selectedPeriods order),
// then template orderIndex.
func (t *oracleTenant) planWorkflow(w *oracleWorkflow) ([]*oracleInstance, error) {
	templates := oracleSortedTemplates(w.spec.Templates)
	if w.spec.IsProject() {
		return t.planProject(w, templates)
	}
	return t.planRecurring(w, templates)
}

func (t *oracleTenant) planRecurring(w *oracleWorkflow, templates []spec.TaskTemplate) ([]*oracleInstance, error) {
	if w.entity == nil {
		return nil, fmt.Errorf("workflow %s: recurring workflow has no entity", w.spec.Key)
	}
	if w.spec.Periodicity == nil || *w.spec.Periodicity == "" {
		return nil, fmt.Errorf("workflow %s: recurring workflow has no periodicity", w.spec.Key)
	}
	fyEndYear, err := strconv.Atoi(w.spec.FinancialYear)
	if err != nil {
		return nil, fmt.Errorf("workflow %s: financial year %q is not numeric", w.spec.Key, w.spec.FinancialYear)
	}
	cal, err := oracleCalendarFor(*w.entity, fyEndYear)
	if err != nil {
		return nil, fmt.Errorf("workflow %s: %w", w.spec.Key, err)
	}

	out := make([]*oracleInstance, 0, len(templates)*len(w.spec.SelectedPeriods))
	for _, code := range w.spec.SelectedPeriods {
		period, err := cal.Period(code, *w.spec.Periodicity)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", w.spec.Key, err)
		}
		filing := deadline.ApplyWeekendAdjustment(
			deadline.ApplyOffset(period.End,
				w.spec.DueDateRule.OffsetValue, w.spec.DueDateRule.OffsetUnit, w.spec.DueDateRule.OffsetDirection),
			w.spec.DueDateRule.WeekendAdjustment,
		)
		payment, err := oraclePaymentDeadline(period.End, filing, w.obligationRule, w.spec.DueDateRule.WeekendAdjustment)
		if err != nil {
			return nil, fmt.Errorf("workflow %s period %s: %w", w.spec.Key, code, err)
		}
		for _, tmpl := range templates {
			ref := period.End
			switch tmpl.DueDateReference {
			case "filing_deadline":
				ref = filing
			case "payment_deadline":
				ref = payment
			}
			paymentCopy := payment
			inst := t.newInstance(w, tmpl)
			inst.PeriodCode = code
			inst.PeriodEnd = period.End
			inst.FilingDeadline = filing
			inst.PaymentDeadline = &paymentCopy
			inst.DueDate = deadline.ApplyOffset(ref,
				tmpl.DueDateOffsetValue, tmpl.DueDateOffsetUnit, tmpl.DueDateOffsetDirection)
			out = append(out, inst)
		}
	}
	return out, nil
}

// planProject mirrors the generator's project branch: one instance per
// template under the single PROJECT period, the workflow's end date as period
// end and filing deadline alike, no payment deadline, due = end ± the
// template offset (every reference resolves to the end date).
func (t *oracleTenant) planProject(w *oracleWorkflow, templates []spec.TaskTemplate) ([]*oracleInstance, error) {
	end := w.spec.EndDate
	if end.IsZero() {
		return nil, fmt.Errorf("workflow %s: project workflow has no end date", w.spec.Key)
	}
	out := make([]*oracleInstance, 0, len(templates))
	for _, tmpl := range templates {
		inst := t.newInstance(w, tmpl)
		inst.PeriodCode = spec.ProjectPeriodCode
		inst.PeriodEnd = end
		inst.FilingDeadline = end
		inst.PaymentDeadline = nil
		inst.DueDate = deadline.ApplyOffset(end,
			tmpl.DueDateOffsetValue, tmpl.DueDateOffsetUnit, tmpl.DueDateOffsetDirection)
		out = append(out, inst)
	}
	return out, nil
}

func (t *oracleTenant) newInstance(w *oracleWorkflow, tmpl spec.TaskTemplate) *oracleInstance {
	inst := &oracleInstance{
		workflow:         w,
		tenant:           t,
		TemplateKey:      tmpl.Key,
		Name:             tmpl.Name,
		TaskType:         tmpl.TaskType,
		RoleLabel:        tmpl.RoleLabel,
		OrderIndex:       tmpl.OrderIndex,
		ApprovalRequired: tmpl.ApprovalRequired,
		Status:           oracleStatusNotStarted,
		TaxDataStatus:    "draft",
	}
	if tmpl.DataTemplate != nil {
		inst.DataTemplate = *tmpl.DataTemplate
	}
	return inst
}

// oracleCalendarFor resolves an entity's fiscal calendar through the same
// entry point the generator uses, so the defaults (week end day, year end
// rule) can never drift apart.
func oracleCalendarFor(e spec.Entity, fiscalYear int) (*deadline.Calendar, error) {
	fyEnd := e.FinancialYearEnd
	entity := &entitiesdomain.Entity{
		FiscalCalendarPattern: e.FiscalCalendarPattern,
	}
	if fyEnd != "" {
		entity.FinancialYearEnd = &fyEnd
	}
	return entitiesdomain.CalendarFor(entity, fiscalYear)
}

// oraclePaymentDeadline is ADR-0023 §5, recomputed:
//   - paymentOffset  → period end + months (month-end clamped) + days, then
//     the weekend adjustment (the obligation's own, else the workflow's);
//   - paymentFixedDates → the first MM-DD on/after the period end, NO weekend
//     adjustment;
//   - neither, or no obligation at all → the filing deadline.
func oraclePaymentDeadline(
	periodEnd, filing dateonly.Date, rule *spec.DeadlineRule, fallbackAdjustment string,
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

// oracleSortedTemplates orders task templates by orderIndex (the generator
// sorts them regardless of how the repository returns them).
func oracleSortedTemplates(in []spec.TaskTemplate) []spec.TaskTemplate {
	out := append([]spec.TaskTemplate(nil), in...)
	for i := 1; i < len(out); i++ { // insertion sort: a workflow has a handful
		for j := i; j > 0 && out[j].OrderIndex < out[j-1].OrderIndex; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// applyDeviations overlays the dataset's per-instance deviations (status,
// assignee, completion, tax data) on the generated defaults.
func (t *oracleTenant) applyDeviations(s *spec.Spec, w *oracleWorkflow) error {
	index := make(map[string]*oracleInstance, len(w.instances))
	for _, inst := range w.instances {
		index[inst.PeriodCode+"\x00"+inst.TemplateKey] = inst
	}
	for _, dev := range w.spec.Instances {
		inst, ok := index[dev.Period+"\x00"+dev.Task]
		if !ok {
			return fmt.Errorf("workflow %s: instance deviation (%s, %s) matches no generated instance",
				w.spec.Key, dev.Period, dev.Task)
		}
		inst.Status = dev.Status
		inst.Via = dev.Via
		inst.AssigneeKey = dev.Assignee
		if dev.Assignee != "" {
			user, ok := t.spec.User(dev.Assignee)
			if !ok {
				return fmt.Errorf("workflow %s: instance (%s, %s) assignee %q is unknown",
					w.spec.Key, dev.Period, dev.Task, dev.Assignee)
			}
			inst.AssigneeName = user.Name
		}
		inst.SubmittedOn = dev.SubmittedOn
		inst.Notes = dev.Notes
		inst.TaxData = dev.TaxData
		if dev.TaxDataStatus != "" {
			inst.TaxDataStatus = dev.TaxDataStatus
		}
		if !dev.CompletedOn.IsZero() {
			inst.CompletedOn = dev.CompletedOn
			at, ok := dev.CompletionInstant(t.zone, s.CompletionLocalTime())
			if !ok {
				return fmt.Errorf("workflow %s: instance (%s, %s) has an unusable completion time",
					w.spec.Key, dev.Period, dev.Task)
			}
			inst.CompletedAt = at
			// The dataset may pin the instant explicitly; it must agree.
			if !dev.CompletedAtUtc.IsZero() && !dev.CompletedAtUtc.Equal(at) {
				return fmt.Errorf("workflow %s: instance (%s, %s) declares completedAtUtc %s but "+
					"completedOn %s at %s in %s is %s",
					w.spec.Key, dev.Period, dev.Task,
					dev.CompletedAtUtc.UTC().Format(time.RFC3339), dev.CompletedOn,
					dev.LocalCompletionTime(s.CompletionLocalTime()), t.spec.Timezone,
					at.Format(time.RFC3339))
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

// oracleClassify is ADR-0021 rule 5, recomputed for one deadline column:
//
//	completed with a completion instant → on_time when the completion's civil
//	date IN THE TENANT'S ZONE is on or before the deadline, else late;
//	otherwise missed once the deadline is behind the tenant's today, else
//	not_due (a deadline equal to today is not_due).
func (i *oracleInstance) classify(deadlineDate dateonly.Date) string {
	if i.isCompleted() && !i.CompletedAt.IsZero() {
		completedCivil := dateonly.FromTime(i.CompletedAt.In(i.tenant.zone))
		if oracleCompareDates(completedCivil, deadlineDate) <= 0 {
			return oracleOnTime
		}
		return oracleLate
	}
	if oracleCompareDates(deadlineDate, i.tenant.today) < 0 {
		return oracleMissed
	}
	return oracleNotDue
}

// completedCivilDate is the completion's calendar date in the tenant's zone
// (zero when the instance carries no completion instant).
func (i *oracleInstance) completedCivilDate() dateonly.Date {
	if i.CompletedAt.IsZero() {
		return dateonly.Date{}
	}
	return dateonly.FromTime(i.CompletedAt.In(i.tenant.zone))
}

// oracleCompareDates orders two civil dates (-1, 0, +1).
func oracleCompareDates(a, b dateonly.Date) int {
	switch {
	case a.Year != b.Year:
		return oracleSign(a.Year - b.Year)
	case a.Month != b.Month:
		return oracleSign(a.Month - b.Month)
	default:
		return oracleSign(a.Day - b.Day)
	}
}

func oracleSign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// oracleAddDays shifts a civil date.
func oracleAddDays(d dateonly.Date, days int) dateonly.Date {
	return dateonly.FromTime(d.Time().AddDate(0, 0, days))
}

// oracleEndOfWeek is the Saturday of the week `d` falls in — the dashboard's
// "this week" boundary (date-fns' default endOfWeek, weeks starting Sunday).
func oracleEndOfWeek(d dateonly.Date) dateonly.Date {
	return oracleAddDays(d, 6-int(d.Time().Weekday()))
}
