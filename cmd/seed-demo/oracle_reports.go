package main

// What every report must return, recomputed from the world the oracle built.
//
// The participation rule is the hinge of the whole file: only workflows that
// are ACTIVE or COMPLETED and RECURRING feed the compliance heatmap, the
// compliance-status report and tax-financial. Draft, archived and project
// workflows generate task instances all the same, and those instances stay
// visible in /reports/task-instances, workflow-stats, the raw export and the
// dashboard.

import (
	"math"
	"sort"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Heatmap view modes.
const (
	oracleViewPeriod  = "period"
	oracleViewTaxType = "tax-type"
)

// Tax-financial groupings.
const (
	oracleGroupEntity     = "entity"
	oracleGroupCountry    = "country"
	oracleGroupTaxType    = "taxType"
	oracleGroupPeriod     = "period"
	oracleGroupObligation = "obligation"
)

// Export datasets.
const (
	oracleDatasetWorkflows = "workflows"
	oracleDatasetTasks     = "tasks"
	oracleDatasetTaxData   = "tax-data"
)

// oracleFilters is the workflow-level filter trio the three compliance /
// financial reports share, expressed in spec keys. An empty field (or the
// legacy "all") means "no filter".
type oracleFilters struct {
	Year          string
	EntityKey     string
	ObligationKey string
}

func oracleFilterSet(v string) bool { return v != "" && v != "all" }

// matches applies the trio to an instance's workflow.
func (f oracleFilters) matches(i *oracleInstance) bool {
	if oracleFilterSet(f.Year) && i.workflow.spec.FinancialYear != f.Year {
		return false
	}
	if oracleFilterSet(f.EntityKey) && i.entityKey() != f.EntityKey {
		return false
	}
	if oracleFilterSet(f.ObligationKey) && i.obligationKey() != f.ObligationKey {
		return false
	}
	return true
}

// participating returns the instances the compliance / financial reports see.
func (t *oracleTenant) participating(f oracleFilters) []*oracleInstance {
	out := make([]*oracleInstance, 0, len(t.instances))
	for _, inst := range t.instances {
		if inst.workflow.participates && f.matches(inst) {
			out = append(out, inst)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// compliance-heatmap
// ---------------------------------------------------------------------------

type oracleHeatmapCell struct {
	RowKey   string // entity key, or "unknown"
	RowLabel string
	ColKey   string // period code, or obligation-type key / "unknown"
	ColLabel string

	TotalTasks      int
	CompletedTasks  int
	OverdueTasks    int
	CompletedLate   int
	InProgressTasks int

	// FirstPeriodEnd is MIN(period_end_date) over the cell — what orders the
	// columns by the calendar instead of by period-code text.
	FirstPeriodEnd dateonly.Date
	// WorkflowKeys are the distinct workflows feeding the cell.
	WorkflowKeys map[string]bool
}

// status is the legacy traffic light: anything missed or completed late is
// red, an all-completed cell is green, otherwise amber.
func (c *oracleHeatmapCell) status() string {
	switch {
	case c.TotalTasks == 0:
		return oracleCellGrey
	case c.OverdueTasks > 0 || c.CompletedLate > 0:
		return oracleCellRed
	case c.CompletedTasks == c.TotalTasks:
		return oracleCellGreen
	default:
		return oracleCellAmber
	}
}

type oracleHeatmapSummary struct {
	TotalCells int
	Green      int
	Amber      int
	Red        int
}

type oracleHeatmap struct {
	// Cells is keyed "<rowKey>|<colKey>".
	Cells map[string]*oracleHeatmapCell
	// RowOrder / ColOrder are the orders the payload must list them in.
	RowOrder []string
	ColOrder []string
	Summary  oracleHeatmapSummary
}

// oracleHeatmapCellKey is the key a difference names a cell by.
func oracleHeatmapCellKey(rowKey, colKey string) string { return rowKey + "|" + colKey }

// heatmap recomputes one heatmap: cells per entity × (period | obligation
// type), classified on the DUE DATE.
func (t *oracleTenant) heatmap(f oracleFilters, viewMode string) *oracleHeatmap {
	h := &oracleHeatmap{Cells: map[string]*oracleHeatmapCell{}}
	for _, inst := range t.participating(f) {
		rowKey, rowLabel := oracleUnknownKey, "Unknown Entity"
		if inst.workflow.entity != nil {
			rowKey, rowLabel = inst.workflow.entity.Key, inst.workflow.entity.Name
		}
		colKey, colLabel := inst.PeriodCode, inst.PeriodCode
		if viewMode == oracleViewTaxType {
			colKey, colLabel = oracleUnknownKey, "Unknown"
			if ot := inst.workflow.obligation; ot != nil {
				colKey, colLabel = ot.Key, ot.Name+" ("+ot.Code+")"
			}
		}

		key := oracleHeatmapCellKey(rowKey, colKey)
		cell := h.Cells[key]
		if cell == nil {
			cell = &oracleHeatmapCell{
				RowKey: rowKey, RowLabel: rowLabel, ColKey: colKey, ColLabel: colLabel,
				FirstPeriodEnd: inst.PeriodEnd,
				WorkflowKeys:   map[string]bool{},
			}
			h.Cells[key] = cell
		}
		cell.TotalTasks++
		if inst.isCompleted() {
			cell.CompletedTasks++
		}
		switch inst.classify(inst.DueDate) {
		case oracleMissed:
			cell.OverdueTasks++
		case oracleLate:
			cell.CompletedLate++
		}
		switch inst.Status {
		case oracleStatusInProgress, oracleStatusInReview, oracleStatusPendingApproval:
			cell.InProgressTasks++
		}
		if oracleCompareDates(inst.PeriodEnd, cell.FirstPeriodEnd) < 0 {
			cell.FirstPeriodEnd = inst.PeriodEnd
		}
		cell.WorkflowKeys[inst.workflowKey()] = true
	}

	h.Summary.TotalCells = len(h.Cells)
	rowLabels := map[string]string{}
	colFirst := map[string]dateonly.Date{}
	colLabels := map[string]string{}
	for _, cell := range h.Cells {
		switch cell.status() {
		case oracleCellGreen:
			h.Summary.Green++
		case oracleCellAmber:
			h.Summary.Amber++
		case oracleCellRed:
			h.Summary.Red++
		}
		rowLabels[cell.RowKey] = cell.RowLabel
		colLabels[cell.ColKey] = cell.ColLabel
		if first, seen := colFirst[cell.ColKey]; !seen || oracleCompareDates(cell.FirstPeriodEnd, first) < 0 {
			colFirst[cell.ColKey] = cell.FirstPeriodEnd
		}
	}

	// Rows come out in row-label order; columns are ordered globally by the
	// earliest period they contain, then by label — so M2 precedes M10
	// whichever row introduced it.
	h.RowOrder = oracleSortedKeys(rowLabels, func(a, b string) bool {
		if rowLabels[a] != rowLabels[b] {
			return rowLabels[a] < rowLabels[b]
		}
		return a < b
	})
	h.ColOrder = oracleSortedKeys(colLabels, func(a, b string) bool {
		if c := oracleCompareDates(colFirst[a], colFirst[b]); c != 0 {
			return c < 0
		}
		if colLabels[a] != colLabels[b] {
			return colLabels[a] < colLabels[b]
		}
		return a < b
	})
	return h
}

// oracleSortedKeys returns a map's keys, ordered by `less`.
func oracleSortedKeys[V any](m map[string]V, less func(a, b string) bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
	return keys
}

// ---------------------------------------------------------------------------
// compliance-status
// ---------------------------------------------------------------------------

type oracleComplianceRow struct {
	Instance *oracleInstance

	EntityName     string
	EntityKey      string
	TaxType        string
	ObligationName string
	ObligationCode string
	Period         string
	FilingDeadline dateonly.Date
	// CompletedOn is the completion's civil date in the tenant zone (zero when
	// the instance has no completion instant); the payload carries the instant.
	CompletedOn     dateonly.Date
	Class           string
	PenaltyInterest string
}

type oracleComplianceSummary struct {
	Total  int
	OnTime int
	Late   int
	Missed int
	NotDue int
}

type oracleCompliance struct {
	// Rows are every classified instance, in the report's order (entity name,
	// obligation name, period — the id tiebreak is not predictable and does
	// not need to be).
	Rows    []*oracleComplianceRow
	Summary oracleComplianceSummary
}

// rowsWithClass is the status-filtered page set ("" = the whole set).
func (c *oracleCompliance) rowsWithClass(class string) []*oracleComplianceRow {
	if class == "" || class == "all" {
		return c.Rows
	}
	out := make([]*oracleComplianceRow, 0, len(c.Rows))
	for _, r := range c.Rows {
		if r.Class == class {
			out = append(out, r)
		}
	}
	return out
}

// compliance classifies every participating instance against its FILING
// deadline. The summary counts the whole set — the status filter narrows the
// rows and totalCount only (product fix F5).
func (t *oracleTenant) compliance(f oracleFilters) *oracleCompliance {
	c := &oracleCompliance{}
	for _, inst := range t.participating(f) {
		row := &oracleComplianceRow{
			Instance:        inst,
			EntityName:      inst.entityName(),
			EntityKey:       inst.entityKey(),
			TaxType:         inst.taxType(),
			ObligationName:  inst.obligationName(),
			ObligationCode:  inst.obligationCode(),
			Period:          inst.PeriodCode,
			FilingDeadline:  inst.FilingDeadline,
			CompletedOn:     inst.completedCivilDate(),
			Class:           inst.classify(inst.FilingDeadline),
			PenaltyInterest: oraclePenaltyInterest(inst),
		}
		c.Rows = append(c.Rows, row)
		c.Summary.Total++
		switch row.Class {
		case oracleOnTime:
			c.Summary.OnTime++
		case oracleLate:
			c.Summary.Late++
		case oracleMissed:
			c.Summary.Missed++
		case oracleNotDue:
			c.Summary.NotDue++
		}
	}
	sort.SliceStable(c.Rows, func(i, j int) bool {
		a, b := c.Rows[i], c.Rows[j]
		if a.EntityName != b.EntityName {
			return a.EntityName < b.EntityName
		}
		if a.ObligationName != b.ObligationName {
			return a.ObligationName < b.ObligationName
		}
		return a.Period < b.Period
	})
	return c
}

// ---------------------------------------------------------------------------
// tax-financial
// ---------------------------------------------------------------------------

type oracleFinancialRow struct {
	Instance *oracleInstance

	EntityName     string
	EntityKey      string
	Country        string
	TaxType        string
	ObligationName string
	ObligationCode string
	Period         string
	FinancialYear  string
	Figures        *oracleFigures
}

type oracleFinancialGroup struct {
	Key     string
	Label   string
	Figures oracleFigures
	Count   int
	// FirstPeriodEnd orders period buckets by the calendar (product fix F4).
	FirstPeriodEnd dateonly.Date
}

type oracleFinancial struct {
	Rows        []*oracleFinancialRow
	Summary     oracleFigures
	RecordCount int
	// Groups is the requested groupBy bucket set, keyed in spec-key space.
	Groups     map[string]*oracleFinancialGroup
	GroupOrder []string
	// Chart is the per-period roll-up, in calendar order.
	Chart      []*oracleFinancialGroup
	ChartOrder []string
}

// financial recomputes the tax-financial report: participating instances that
// carry tax data, their figures, and the three exact roll-ups.
func (t *oracleTenant) financial(f oracleFilters, groupBy string) *oracleFinancial {
	fin := &oracleFinancial{Groups: map[string]*oracleFinancialGroup{}}
	periods := map[string]*oracleFinancialGroup{}

	for _, inst := range t.participating(f) {
		if len(inst.TaxData) == 0 { // tax_data IS NOT NULL AND <> '{}'
			continue
		}
		figures := oracleTaxFinancialFigures(inst.taxType(), inst.TaxData)
		row := &oracleFinancialRow{
			Instance:       inst,
			EntityName:     inst.entityName(),
			EntityKey:      inst.entityKey(),
			Country:        inst.country(),
			TaxType:        inst.taxType(),
			ObligationName: inst.financialObligationName(),
			ObligationCode: inst.obligationCode(),
			Period:         inst.PeriodCode,
			FinancialYear:  inst.workflow.spec.FinancialYear,
			Figures:        figures,
		}
		fin.Rows = append(fin.Rows, row)
		fin.RecordCount++
		fin.Summary.add(figures)

		key, label := oracleFinancialGroupOf(row, groupBy)
		group := fin.Groups[key]
		if group == nil {
			group = &oracleFinancialGroup{Key: key, Label: label, FirstPeriodEnd: inst.PeriodEnd}
			fin.Groups[key] = group
		}
		group.Figures.add(figures)
		group.Count++
		if oracleCompareDates(inst.PeriodEnd, group.FirstPeriodEnd) < 0 {
			group.FirstPeriodEnd = inst.PeriodEnd
		}

		period := periods[inst.PeriodCode]
		if period == nil {
			period = &oracleFinancialGroup{Key: inst.PeriodCode, Label: inst.PeriodCode, FirstPeriodEnd: inst.PeriodEnd}
			periods[inst.PeriodCode] = period
		}
		period.Figures.add(figures)
		period.Count++
		if oracleCompareDates(inst.PeriodEnd, period.FirstPeriodEnd) < 0 {
			period.FirstPeriodEnd = inst.PeriodEnd
		}
	}

	sort.SliceStable(fin.Rows, func(i, j int) bool {
		a, b := fin.Rows[i], fin.Rows[j]
		if a.EntityName != b.EntityName {
			return a.EntityName < b.EntityName
		}
		if a.ObligationName != b.ObligationName {
			return a.ObligationName < b.ObligationName
		}
		return a.Period < b.Period
	})

	// Period buckets follow the calendar; every other grouping is ordered by
	// label (the report orders by label, but only the period order is a
	// contract the oracle asserts — see verify).
	fin.ChartOrder = oracleSortedKeys(periods, func(a, b string) bool {
		if c := oracleCompareDates(periods[a].FirstPeriodEnd, periods[b].FirstPeriodEnd); c != 0 {
			return c < 0
		}
		return a < b
	})
	for _, code := range fin.ChartOrder {
		fin.Chart = append(fin.Chart, periods[code])
	}
	fin.GroupOrder = oracleSortedKeys(fin.Groups, func(a, b string) bool {
		if groupBy == oracleGroupPeriod {
			if c := oracleCompareDates(fin.Groups[a].FirstPeriodEnd, fin.Groups[b].FirstPeriodEnd); c != 0 {
				return c < 0
			}
		}
		if fin.Groups[a].Label != fin.Groups[b].Label {
			return fin.Groups[a].Label < fin.Groups[b].Label
		}
		return a < b
	})
	return fin
}

// oracleFinancialGroupOf resolves one row's bucket key and label. Keys stay in
// spec-key space (entity / obligation keys, not ids); verify translates the
// live ids before diffing.
func oracleFinancialGroupOf(row *oracleFinancialRow, groupBy string) (key, label string) {
	switch groupBy {
	case oracleGroupCountry:
		if row.Country == "" {
			return oracleUnknownKey, "Unknown"
		}
		return row.Country, row.Country
	case oracleGroupTaxType:
		if row.TaxType == "" {
			return oracleUnknownKey, "Unknown"
		}
		return row.TaxType, row.TaxType
	case oracleGroupPeriod:
		return row.Period, row.Period
	case oracleGroupObligation:
		if row.Instance.workflow.obligation == nil {
			return oracleUnknownKey, row.ObligationName
		}
		return row.Instance.workflow.obligation.Key, row.ObligationName
	default: // entity
		if row.EntityKey == "" {
			return oracleUnknownKey, row.EntityName
		}
		return row.EntityKey, row.EntityName
	}
}

// ---------------------------------------------------------------------------
// export-raw
// ---------------------------------------------------------------------------

// oracleExportFilters are the raw export's filters: entity / obligation type /
// workflow category plus an inclusive date-only window (workflows.created_at's
// UTC date for the workflows dataset, the instance due date for the other two).
type oracleExportFilters struct {
	EntityKey     string
	ObligationKey string
	Category      string
	DateFrom      dateonly.Date
	DateTo        dateonly.Date
}

func (f oracleExportFilters) withinWindow(d dateonly.Date) bool {
	if !f.DateFrom.IsZero() && oracleCompareDates(d, f.DateFrom) < 0 {
		return false
	}
	if !f.DateTo.IsZero() && oracleCompareDates(d, f.DateTo) > 0 {
		return false
	}
	return true
}

// matchesWorkflow applies the entity / obligation / category filters. Every
// workflow status takes part in the export.
func (f oracleExportFilters) matchesWorkflow(w *oracleWorkflow) bool {
	if oracleFilterSet(f.EntityKey) {
		if w.entity == nil || w.entity.Key != f.EntityKey {
			return false
		}
	}
	if oracleFilterSet(f.ObligationKey) {
		if w.obligation == nil || w.obligation.Key != f.ObligationKey {
			return false
		}
	}
	if oracleFilterSet(f.Category) && w.spec.Category != f.Category {
		return false
	}
	return true
}

// exportWorkflows lists the workflows the export returns. createdDates maps a
// workflow key to the UTC calendar date its row was created on — a runtime
// fact the oracle cannot know, so verify reads it from the live workflow list
// and the oracle applies the window to it.
func (t *oracleTenant) exportWorkflows(f oracleExportFilters, createdDates map[string]dateonly.Date) []*oracleWorkflow {
	out := make([]*oracleWorkflow, 0, len(t.workflows))
	for _, w := range t.workflows {
		if !f.matchesWorkflow(w) {
			continue
		}
		if !f.DateFrom.IsZero() || !f.DateTo.IsZero() {
			created, ok := createdDates[w.spec.Key]
			if !ok || !f.withinWindow(created) {
				continue
			}
		}
		out = append(out, w)
	}
	return out
}

// exportInstances lists the instances the tasks / tax-data datasets return
// (tax-data keeps only instances whose tax_data is not NULL). Both window on
// the instance's due date.
func (t *oracleTenant) exportInstances(f oracleExportFilters, taxDataOnly bool) []*oracleInstance {
	out := make([]*oracleInstance, 0, len(t.instances))
	for _, inst := range t.instances {
		if !f.matchesWorkflow(inst.workflow) {
			continue
		}
		if !f.withinWindow(inst.DueDate) {
			continue
		}
		if taxDataOnly && inst.TaxData == nil {
			continue
		}
		out = append(out, inst)
	}
	return out
}

// ---------------------------------------------------------------------------
// workflow-stats and the dashboard
// ---------------------------------------------------------------------------

type oracleWorkflowStats struct {
	TotalTasks        int
	CompletedTasks    int
	CompletionPercent int
	// NextDueDate is the earliest due date among the not-yet-completed
	// instances; zero renders as null.
	NextDueDate dateonly.Date
}

// stats is the per-workflow completion summary — every workflow in the
// tenant, drafts and projects included, at zero when it was never started.
func (w *oracleWorkflow) stats() oracleWorkflowStats {
	var s oracleWorkflowStats
	for _, inst := range w.instances {
		s.TotalTasks++
		if inst.isCompleted() {
			s.CompletedTasks++
			continue
		}
		if s.NextDueDate.IsZero() || oracleCompareDates(inst.DueDate, s.NextDueDate) < 0 {
			s.NextDueDate = inst.DueDate
		}
	}
	if s.TotalTasks > 0 {
		// Integer rounding without float drift, as the API computes it.
		s.CompletionPercent = (2*s.CompletedTasks*100 + s.TotalTasks) / (2 * s.TotalTasks)
	}
	return s
}

type oracleDashboard struct {
	Today            dateonly.Date
	WeekEnd          dateonly.Date
	TotalInstances   int
	Overdue          int
	DueToday         int
	ThisWeek         int
	Active           int
	Completed        int
	AwaitingApproval int
	CompletionRate   int
	ByStatus         map[string]int
}

// dashboard is what the UI computes from /reports/task-instances and
// /reports/workflow-stats against the tenant's civil today.
func (t *oracleTenant) dashboard() oracleDashboard {
	d := oracleDashboard{Today: t.today, WeekEnd: oracleEndOfWeek(t.today), ByStatus: map[string]int{}}
	for _, inst := range t.instances {
		d.TotalInstances++
		d.ByStatus[inst.Status]++
		if inst.Status == oracleStatusPendingApproval {
			d.AwaitingApproval++
		}
		if inst.isCompleted() {
			d.Completed++
			continue
		}
		d.Active++
		switch {
		case oracleCompareDates(inst.DueDate, d.Today) < 0:
			d.Overdue++
		case oracleCompareDates(inst.DueDate, d.Today) == 0:
			d.DueToday++
		case oracleCompareDates(inst.DueDate, d.WeekEnd) <= 0:
			d.ThisWeek++
		}
	}
	total, completed := 0, 0
	for _, w := range t.workflows {
		s := w.stats()
		total += s.TotalTasks
		completed += s.CompletedTasks
	}
	if total > 0 {
		d.CompletionRate = int(math.Round(100 * float64(completed) / float64(total)))
	}
	return d
}
