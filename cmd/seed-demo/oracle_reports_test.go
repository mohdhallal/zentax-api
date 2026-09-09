package main

// Unit tests for the report rules: figure extraction, the traffic light, the
// summary that ignores the status filter, the calendar ordering of period
// buckets, the export windows and the dashboard formulas.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// ---------------------------------------------------------------------------
// Figures
// ---------------------------------------------------------------------------

func TestOracleTaxFinancialFigures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		taxType string
		data    map[string]any
		want    map[string]string
	}{
		{
			name: "VAT derives netVat from output minus input", taxType: "VAT",
			data: map[string]any{"outputVat": 1000, "inputVat": 400, "salesTotal": 9999},
			want: map[string]string{"outputVat": "1000", "inputVat": "400", "netVat": "600", "totalAmount": "600"},
		},
		{
			name: "a stated netVat wins over the derivation", taxType: "VAT",
			data: map[string]any{"outputVat": 2000, "inputVat": 500, "netVat": 1400},
			want: map[string]string{"netVat": "1400", "totalAmount": "1400"},
		},
		{
			name: "a zero netVat is not 'stated' and falls back to the derivation", taxType: "VAT",
			data: map[string]any{"outputVat": 800, "inputVat": 300, "netVat": 0},
			want: map[string]string{"netVat": "500", "totalAmount": "500"},
		},
		{
			name: "legacy VAT aliases", taxType: "VAT",
			data: map[string]any{"outputTax": 700, "purchaseVat": 200},
			want: map[string]string{"outputVat": "700", "inputVat": "200", "netVat": "500"},
		},
		{
			name: "CIT reports the liability", taxType: "CIT",
			data: map[string]any{"taxableIncome": 50000, "taxLiability": 12500, "outputVat": 999},
			want: map[string]string{
				"taxableIncome": "50000", "taxLiability": "12500",
				"totalAmount": "12500", "outputVat": "0", "netVat": "0",
			},
		},
		{
			name: "WHT reports the withheld amount", taxType: "WHT",
			data: map[string]any{"whtBase": 100000, "whtRate": 15, "whtAmount": 15000},
			want: map[string]string{"whtAmount": "15000", "totalAmount": "15000"},
		},
		{
			name: "any other tax type uses the single amount", taxType: "TP",
			data: map[string]any{"amount": 85000, "outputVat": 5},
			want: map[string]string{"totalAmount": "85000", "outputVat": "0", "netVat": "0"},
		},
		{
			name: "no obligation type is the other branch too", taxType: "",
			data: map[string]any{"totalAmount": 4200},
			want: map[string]string{"totalAmount": "4200"},
		},
		{
			name: "the engagement cost is read for every tax type", taxType: "WHT",
			data: map[string]any{"whtAmount": 10, "engagementCost": 750},
			want: map[string]string{"engagementCost": "750"},
		},
		{
			name: "a numeric string counts as a number", taxType: "VAT",
			data: map[string]any{"outputVat": "1000.50", "inputVat": 0.5},
			want: map[string]string{"outputVat": "1000.5", "inputVat": "0.5", "netVat": "1000"},
		},
		{
			name: "text and booleans count as zero", taxType: "VAT",
			data: map[string]any{"outputVat": "n/a", "inputVat": true, "netVat": ""},
			want: map[string]string{"outputVat": "0", "inputVat": "0", "netVat": "0", "totalAmount": "0"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			figures := oracleTaxFinancialFigures(tc.taxType, oracleTestNumbers(t, tc.data))
			for field, want := range tc.want {
				assert.Equal(t, want, oracleRatString(figures.field(field)), field)
			}
		})
	}
}

func TestOracleExportTaxDataFiguresAreUngated(t *testing.T) {
	t.Parallel()
	// The raw export reads every figure whatever the tax type, and never
	// derives netVat.
	figures := oracleExportTaxDataFigures(oracleTestNumbers(t, map[string]any{
		"outputVat": 1000, "inputVat": 400, "taxableIncome": 50, "penalty": 150, "interest": 12.5,
	}))
	assert.Equal(t, "1000", oracleRatString(&figures.OutputVat))
	assert.Equal(t, "400", oracleRatString(&figures.InputVat))
	assert.Equal(t, "0", oracleRatString(&figures.NetVat), "the export never derives netVat")
	assert.Equal(t, "50", oracleRatString(&figures.TaxableIncome), "no tax-type gating")
	assert.Equal(t, "150", oracleRatString(&figures.PenaltyAmount), "the legacy 'penalty' alias")
	assert.Equal(t, "12.5", oracleRatString(&figures.InterestAmount))
}

func TestOraclePenaltyInterestText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		taskType string
		notes    string
		data     map[string]any
		want     string
	}{
		{name: "no data, no notes", taskType: "preparation", want: ""},
		{name: "penalty only", data: map[string]any{"penaltyAmount": 1500}, want: "Penalty: 1500"},
		{name: "interest only", data: map[string]any{"interestAmount": 12.5}, want: "Interest: 12.5"},
		{
			name: "both, legacy keys",
			data: map[string]any{"penalty": 300, "interest": 20}, want: "Penalty: 300, Interest: 20",
		},
		{name: "a zero figure is not truthy", data: map[string]any{"penaltyAmount": 0}, want: ""},
		{
			name:     "a payment task falls back to its notes",
			taskType: "payment", notes: "paid late, waiting on the assessment", want: "paid late, waiting on the assessment",
		},
		{
			name:     "notes never override a figure",
			taskType: "payment", notes: "ignored", data: map[string]any{"penaltyAmount": 10},
			want: "Penalty: 10",
		},
		{name: "a non-payment task's notes are not shown", taskType: "review", notes: "n/a", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := &oracleInstance{TaskType: tc.taskType, Notes: tc.notes}
			if tc.data != nil {
				inst.TaxData = oracleTestNumbers(t, tc.data)
			}
			assert.Equal(t, tc.want, oraclePenaltyInterest(inst))
		})
	}
}

// ---------------------------------------------------------------------------
// A fixture with a full spread of classifications
// ---------------------------------------------------------------------------

// oracleTestReportTenant builds a tenant whose single participating workflow
// covers M1 (completed, one of them late), M2 (missed, one in progress) and
// M10 (still ahead) — plus an archived workflow and a project workflow, which
// must stay out of the compliance and financial reports.
func oracleTestReportTenant(t *testing.T) *oracleTenant {
	t.Helper()

	participating := spec.Workflow{
		Key: "w1", Name: "DE VAT 2026", Category: spec.CategoryRecurring,
		Entity: oracleTestString("e.de"), ObligationType: oracleTestString("o.vat"),
		EntityObligation: oracleTestString("eo.de-vat"),
		FinancialYear:    "2026", Periodicity: oracleTestString("monthly"),
		SelectedPeriods: []string{"M1", "M2", "M10"},
		DueDateRule: spec.DueDateRule{
			Reference: "period_end", OffsetUnit: "days", OffsetValue: 2,
			OffsetDirection: "after", WeekendAdjustment: "none",
		},
		StartDate: dateonly.New(2026, 1, 1), EndDate: dateonly.New(2026, 12, 31),
		Writer: "t.prep", Approver: "t.admin",
		Lifecycle: spec.Lifecycle{Start: true, FinalStatus: "active"},
		Templates: []spec.TaskTemplate{
			{Key: "collect", Name: "Collect data", TaskType: "data_request", OrderIndex: 0,
				DueDateReference: "period_end", DueDateOffsetValue: 2,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "after"},
			{Key: "prepare", Name: "Prepare return", TaskType: "preparation", OrderIndex: 1,
				DueDateReference: "period_end", DueDateOffsetValue: 2,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "after"},
		},
		Instances: []spec.Instance{
			{Period: "M1", Task: "collect", Status: oracleStatusCompleted, Via: "put",
				Assignee: "t.prep", CompletedOn: dateonly.New(2026, 2, 1)},
			{Period: "M1", Task: "prepare", Status: oracleStatusCompleted, Via: "put",
				Assignee: "t.prep", CompletedOn: dateonly.New(2026, 2, 5),
				TaxData:       map[string]any{"outputVat": 1000, "inputVat": 400},
				TaxDataStatus: "final"},
			{Period: "M2", Task: "collect", Status: oracleStatusInProgress, Via: "put", Assignee: "t.prep"},
			{Period: "M2", Task: "prepare", Status: oracleStatusNotStarted, Via: "put",
				TaxData:       map[string]any{"outputVat": 2000, "inputVat": 500, "netVat": 1400},
				TaxDataStatus: "draft"},
		},
	}
	// tax_data must decode the way spec.Load decodes it (json.Number).
	participating.Instances[1].TaxData = oracleTestNumbers(t, participating.Instances[1].TaxData)
	participating.Instances[3].TaxData = oracleTestNumbers(t, participating.Instances[3].TaxData)

	archived := spec.Workflow{
		Key: "w2", Name: "UK CIT 2026", Category: spec.CategoryRecurring,
		Entity: oracleTestString("e.uk"), ObligationType: oracleTestString("o.cit"),
		FinancialYear: "2026", Periodicity: oracleTestString("annual"),
		SelectedPeriods: []string{"Y1"},
		DueDateRule: spec.DueDateRule{
			Reference: "period_end", OffsetUnit: "days", OffsetValue: 0,
			OffsetDirection: "after", WeekendAdjustment: "none",
		},
		StartDate: dateonly.New(2025, 4, 1), EndDate: dateonly.New(2026, 3, 31),
		Writer: "t.prep", Approver: "t.admin",
		Lifecycle: spec.Lifecycle{Start: true, FinalStatus: "archived"},
		Templates: []spec.TaskTemplate{
			{Key: "file", Name: "File", TaskType: "submission", OrderIndex: 0,
				DueDateReference: "period_end", DueDateOffsetValue: 0,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "after"},
		},
		Instances: []spec.Instance{
			{Period: "Y1", Task: "file", Status: oracleStatusCompleted, Via: "put",
				Assignee: "t.prep", CompletedOn: dateonly.New(2026, 3, 31),
				TaxData:       map[string]any{"taxableIncome": 1, "taxLiability": 1},
				TaxDataStatus: "final"},
		},
	}
	archived.Instances[0].TaxData = oracleTestNumbers(t, archived.Instances[0].TaxData)

	project := spec.Workflow{
		Key: "w3", Name: "Dispute 2026", Category: spec.CategoryProject,
		ProjectType: oracleTestString("dispute"), Entity: oracleTestString("e.de"),
		FinancialYear: "2026",
		DueDateRule: spec.DueDateRule{
			Reference: "period_end", OffsetUnit: "days", OffsetValue: 0,
			OffsetDirection: "after", WeekendAdjustment: "none",
		},
		StartDate: dateonly.New(2026, 1, 1), EndDate: dateonly.New(2026, 9, 30),
		Writer: "t.prep", Approver: "t.admin",
		Lifecycle: spec.Lifecycle{Start: true, FinalStatus: "active"},
		Templates: []spec.TaskTemplate{
			{Key: "file", Name: "File the appeal", TaskType: "submission", OrderIndex: 0,
				DueDateReference: "period_end", DueDateOffsetValue: 0,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "after"},
		},
		Instances: []spec.Instance{
			{Period: spec.ProjectPeriodCode, Task: "file", Status: oracleStatusPendingApproval,
				Via: "submit", Assignee: "t.prep", SubmittedOn: dateonly.New(2026, 8, 1)},
		},
	}

	return oracleTestBuild(t, oracleTestSpec("Europe/Berlin", participating, archived, project), "2026-09-06")
}

func TestOracleHeatmapCellsColoursAndColumnOrder(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)
	// A selected year keys the columns by bare period code.
	heatmap := tenant.heatmap(oracleFilters{Year: "2026"}, oracleViewPeriod)

	require.Equal(t, 3, heatmap.Summary.TotalCells, "only the participating workflow's cells")
	assert.Equal(t, 0, heatmap.Summary.Green)
	assert.Equal(t, 1, heatmap.Summary.Amber)
	assert.Equal(t, 2, heatmap.Summary.Red)

	tests := []struct {
		cell            string
		total           int
		completed       int
		overdue         int
		completedLate   int
		inProgressTasks int
		status          string
	}{
		// Both completed, but one after its due date → red, not green.
		{cell: "e.de|M1", total: 2, completed: 2, overdue: 0, completedLate: 1, status: oracleCellRed},
		// Both due 2 March and neither completed → missed.
		{cell: "e.de|M2", total: 2, overdue: 2, inProgressTasks: 1, status: oracleCellRed},
		// Due 2 November, nothing done yet → amber, nothing missed.
		{cell: "e.de|M10", total: 2, status: oracleCellAmber},
	}
	for _, tc := range tests {
		t.Run(tc.cell, func(t *testing.T) {
			cell, ok := heatmap.Cells[tc.cell]
			require.True(t, ok, "cell %s", tc.cell)
			assert.Equal(t, tc.total, cell.TotalTasks, "totalTasks")
			assert.Equal(t, tc.completed, cell.CompletedTasks, "completedTasks")
			assert.Equal(t, tc.overdue, cell.OverdueTasks, "overdueTasks")
			assert.Equal(t, tc.completedLate, cell.CompletedLate, "completedLate")
			assert.Equal(t, tc.inProgressTasks, cell.InProgressTasks, "inProgressTasks")
			assert.Equal(t, tc.status, cell.status(), "status")
			assert.Equal(t, cell.ColKey, cell.ColLabel, "a selected year: bare period code, label = code")
		})
	}

	assert.Equal(t, []string{"M1", "M2", "M10"}, heatmap.ColOrder,
		"columns follow the calendar, not period-code text (which would put M10 before M2)")
	assert.Equal(t, []string{"e.de"}, heatmap.RowOrder)

	// No year selected: the same cells, keyed "<financialYear>:<periodCode>"
	// and labelled "M1 (FY2026)" (ADR-0026 decision 7).
	allYears := tenant.heatmap(oracleFilters{}, oracleViewPeriod)
	require.Equal(t, 3, allYears.Summary.TotalCells)
	assert.Equal(t, []string{"2026:M1", "2026:M2", "2026:M10"}, allYears.ColOrder)
	for _, code := range []string{"M1", "M2", "M10"} {
		require.Contains(t, allYears.Cells, "e.de|2026:"+code)
		cell := allYears.Cells["e.de|2026:"+code]
		assert.Equal(t, code+" (FY2026)", cell.ColLabel)
		assert.Equal(t, heatmap.Cells["e.de|"+code].TotalTasks, cell.TotalTasks)
		assert.Equal(t, heatmap.Cells["e.de|"+code].status(), cell.status())
	}
	assert.Equal(t, allYears.ColOrder, tenant.heatmap(oracleFilters{Year: "all"}, oracleViewPeriod).ColOrder,
		"the legacy 'all' is no year")
	assert.False(t, allYears.exceedsCap())

	byTaxType := tenant.heatmap(oracleFilters{}, oracleViewTaxType)
	assert.Equal(t, []string{"o.vat"}, byTaxType.ColOrder, "the tax-type view never qualifies by year")
	require.Contains(t, byTaxType.Cells, "e.de|o.vat")
	assert.Equal(t, "Value Added Tax (VAT)", byTaxType.Cells["e.de|o.vat"].ColLabel)
	assert.Equal(t, 6, byTaxType.Cells["e.de|o.vat"].TotalTasks)
}

// Two fiscal years with the same period code: with no year selected they are
// two columns in calendar order (FY2025 before FY2026), each fed by its own
// workflow; the legacy keying (the dataset's static tables) folds them into
// one cell whose counts are the sums and whose colour reads off the sums.
func TestOracleHeatmapQualifiesYearsAndTheLegacyKeyingFoldsThem(t *testing.T) {
	t.Parallel()
	tenant := oracleTestTwoYearTenant(t)

	qualified := tenant.heatmap(oracleFilters{}, oracleViewPeriod)
	assert.Equal(t, []string{"2025:M1", "2025:M2", "2026:M1", "2026:M2", "2026:M10"}, qualified.ColOrder)
	assert.Equal(t, 5, qualified.Summary.TotalCells)
	require.Contains(t, qualified.Cells, "e.de|2025:M1")
	require.Contains(t, qualified.Cells, "e.de|2026:M1")
	prior, current := qualified.Cells["e.de|2025:M1"], qualified.Cells["e.de|2026:M1"]
	assert.Equal(t, "M1 (FY2025)", prior.ColLabel)
	assert.Equal(t, "M1 (FY2026)", current.ColLabel)
	assert.Equal(t, 1, prior.TotalTasks)
	assert.Equal(t, 2, current.TotalTasks)
	assert.Equal(t, map[string]bool{"w4": true}, prior.WorkflowKeys)
	assert.Equal(t, map[string]bool{"w1": true}, current.WorkflowKeys)
	assert.Equal(t, oracleCellGreen, prior.status(), "FY2025 M1: completed on time")
	assert.Equal(t, oracleCellRed, current.status(), "FY2026 M1: one completed late")

	folded := tenant.heatmapKeyed(oracleFilters{}, oracleViewPeriod, false)
	assert.Equal(t, []string{"M1", "M2", "M10"}, folded.ColOrder, "bare codes, first-seen calendar order")
	assert.Equal(t, 3, folded.Summary.TotalCells)
	merged := folded.Cells["e.de|M1"]
	require.NotNil(t, merged)
	assert.Equal(t, 3, merged.TotalTasks, "the sums of both years")
	assert.Equal(t, 3, merged.CompletedTasks)
	assert.Equal(t, 1, merged.CompletedLate)
	assert.Equal(t, oracleCellRed, merged.status(), "the colour reads off the sums")
	assert.Equal(t, map[string]bool{"w1": true, "w4": true}, merged.WorkflowKeys)

	// A selected year: bare codes, that year only — unchanged by the decision.
	assert.Equal(t, []string{"M1", "M2"}, tenant.heatmap(oracleFilters{Year: "2025"}, oracleViewPeriod).ColOrder)
	assert.Equal(t, 2, tenant.heatmap(oracleFilters{Year: "2025"}, oracleViewPeriod).Summary.TotalCells)
}

// oracleTestTwoYearTenant is oracleTestReportTenant plus a second
// participating workflow for the same entity and obligation in FY2025, with
// periods M1 and M2 (both completed on time).
func oracleTestTwoYearTenant(t *testing.T) *oracleTenant {
	t.Helper()
	prior := spec.Workflow{
		Key: "w4", Name: "DE VAT 2025", Category: spec.CategoryRecurring,
		Entity: oracleTestString("e.de"), ObligationType: oracleTestString("o.vat"),
		EntityObligation: oracleTestString("eo.de-vat"),
		FinancialYear:    "2025", Periodicity: oracleTestString("monthly"),
		SelectedPeriods: []string{"M1", "M2"},
		DueDateRule: spec.DueDateRule{
			Reference: "period_end", OffsetUnit: "days", OffsetValue: 2,
			OffsetDirection: "after", WeekendAdjustment: "none",
		},
		StartDate: dateonly.New(2025, 1, 1), EndDate: dateonly.New(2025, 12, 31),
		Writer: "t.prep", Approver: "t.admin",
		Lifecycle: spec.Lifecycle{Start: true, FinalStatus: "active"},
		Templates: []spec.TaskTemplate{
			{Key: "prepare", Name: "Prepare return", TaskType: "preparation", OrderIndex: 0,
				DueDateReference: "period_end", DueDateOffsetValue: 2,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "after"},
		},
		Instances: []spec.Instance{
			{Period: "M1", Task: "prepare", Status: oracleStatusCompleted, Via: "put",
				Assignee: "t.prep", CompletedOn: dateonly.New(2025, 2, 1)},
			{Period: "M2", Task: "prepare", Status: oracleStatusCompleted, Via: "put",
				Assignee: "t.prep", CompletedOn: dateonly.New(2025, 3, 1)},
		},
	}
	base := oracleTestReportTenant(t)
	workflows := make([]spec.Workflow, 0, len(base.spec.Workflows)+1)
	workflows = append(workflows, base.spec.Workflows...)
	workflows = append(workflows, prior)
	return oracleTestBuild(t, oracleTestSpec("Europe/Berlin", workflows...), "2026-09-06")
}

func TestOracleHeatmapFilters(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)
	tests := []struct {
		name  string
		f     oracleFilters
		cells int
	}{
		{name: "unfiltered", f: oracleFilters{}, cells: 3},
		{name: "the legacy 'all' is not a filter", f: oracleFilters{Year: "all", EntityKey: "all"}, cells: 3},
		{name: "matching year", f: oracleFilters{Year: "2026"}, cells: 3},
		{name: "other year", f: oracleFilters{Year: "2025"}, cells: 0},
		{name: "matching entity", f: oracleFilters{EntityKey: "e.de"}, cells: 3},
		{name: "the archived workflow's entity", f: oracleFilters{EntityKey: "e.uk"}, cells: 0},
		{name: "matching obligation", f: oracleFilters{ObligationKey: "o.vat"}, cells: 3},
		{name: "other obligation", f: oracleFilters{ObligationKey: "o.cit"}, cells: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.cells, tenant.heatmap(tc.f, oracleViewPeriod).Summary.TotalCells)
		})
	}
}

func TestOracleComplianceSummaryIgnoresTheStatusFilter(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)
	compliance := tenant.compliance(oracleFilters{})

	// Six instances of the participating workflow; the archived and project
	// workflows contribute none.
	assert.Equal(t, 6, compliance.Summary.Total)
	assert.Equal(t, 1, compliance.Summary.OnTime)
	assert.Equal(t, 1, compliance.Summary.Late)
	assert.Equal(t, 2, compliance.Summary.Missed)
	assert.Equal(t, 2, compliance.Summary.NotDue)

	// The status filter narrows the rows, never the summary (product fix F5).
	tests := []struct {
		class string
		rows  int
	}{
		{class: "", rows: 6},
		{class: "all", rows: 6},
		{class: oracleOnTime, rows: 1},
		{class: oracleLate, rows: 1},
		{class: oracleMissed, rows: 2},
		{class: oracleNotDue, rows: 2},
	}
	for _, tc := range tests {
		t.Run("status="+tc.class, func(t *testing.T) {
			assert.Len(t, compliance.rowsWithClass(tc.class), tc.rows)
		})
	}

	// Rows are ordered entity name → obligation name → period code.
	require.Len(t, compliance.Rows, 6)
	for _, row := range compliance.Rows {
		assert.Equal(t, "DE GmbH", row.EntityName)
		assert.Equal(t, "VAT", row.TaxType)
		assert.Equal(t, "Value Added Tax", row.ObligationName)
	}
	assert.Equal(t, "2026-02-02", compliance.Rows[0].FilingDeadline.String())
}

func TestOracleFinancialAggregations(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)
	financial := tenant.financial(oracleFilters{}, oracleGroupEntity)

	// Only the two instances of the participating workflow that carry tax
	// data; the archived workflow's CIT figures never appear.
	require.Equal(t, 2, financial.RecordCount)
	assert.Equal(t, "3000", oracleRatString(&financial.Summary.OutputVat))
	assert.Equal(t, "900", oracleRatString(&financial.Summary.InputVat))
	assert.Equal(t, "2000", oracleRatString(&financial.Summary.NetVat), "600 derived + 1400 stated")
	assert.Equal(t, "2000", oracleRatString(&financial.Summary.TotalAmount))
	assert.Equal(t, "0", oracleRatString(&financial.Summary.TaxableIncome), "the archived CIT row is out")

	require.Contains(t, financial.Groups, "e.de")
	assert.Equal(t, 2, financial.Groups["e.de"].Count)
	assert.Equal(t, "DE GmbH", financial.Groups["e.de"].Label)

	byCountry := tenant.financial(oracleFilters{}, oracleGroupCountry)
	require.Contains(t, byCountry.Groups, "Germany")
	assert.Equal(t, "2000", oracleRatString(&byCountry.Groups["Germany"].Figures.TotalAmount))

	byTaxType := tenant.financial(oracleFilters{}, oracleGroupTaxType)
	require.Contains(t, byTaxType.Groups, "VAT")
	assert.Equal(t, 2, byTaxType.Groups["VAT"].Count)

	byObligation := tenant.financial(oracleFilters{}, oracleGroupObligation)
	require.Contains(t, byObligation.Groups, "o.vat")
	assert.Equal(t, "Value Added Tax", byObligation.Groups["o.vat"].Label)

	byPeriod := tenant.financial(oracleFilters{}, oracleGroupPeriod)
	assert.Equal(t, []string{"M1", "M2"}, byPeriod.GroupOrder, "period buckets follow the calendar")
	assert.Equal(t, "600", oracleRatString(&byPeriod.Groups["M1"].Figures.TotalAmount))
	assert.Equal(t, "1400", oracleRatString(&byPeriod.Groups["M2"].Figures.TotalAmount))

	assert.Equal(t, []string{"M1", "M2"}, financial.ChartOrder,
		"the chart is per period whatever the grouping")
}

func TestOracleFinancialChartOrderIsCalendarNotText(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)
	// Give M10 tax data so the chart has to order M1, M2, M10 — the case
	// text ordering gets wrong (product fix F4).
	for _, inst := range tenant.byWfKey["w1"].instances {
		if inst.PeriodCode == "M10" && inst.TemplateKey == "prepare" {
			inst.TaxData = oracleTestNumbers(t, map[string]any{"outputVat": 10, "inputVat": 4})
			inst.TaxDataStatus = "draft"
		}
	}
	assert.Equal(t, []string{"M1", "M2", "M10"},
		tenant.financial(oracleFilters{}, oracleGroupPeriod).ChartOrder)
}

func TestOracleExportIncludesEveryWorkflowAndWindowsOnDueDate(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)

	// The tasks dataset spans every workflow status and category: 6 + 1 + 1.
	assert.Len(t, tenant.exportInstances(oracleExportFilters{}, false), 8)
	// Only the instances that carry tax data.
	assert.Len(t, tenant.exportInstances(oracleExportFilters{}, true), 3)

	tests := []struct {
		name  string
		f     oracleExportFilters
		tasks int
	}{
		{name: "no window", f: oracleExportFilters{}, tasks: 8},
		{
			// The four M1 / M2 tasks plus the archived workflow's Y1 task,
			// due 31 March: the export spans every workflow status.
			name:  "a window that holds the M1, M2 and Y1 due dates",
			f:     oracleExportFilters{DateFrom: dateonly.New(2026, 1, 1), DateTo: dateonly.New(2026, 3, 31)},
			tasks: 5,
		},
		{
			name:  "the window is inclusive on both ends",
			f:     oracleExportFilters{DateFrom: dateonly.New(2026, 3, 2), DateTo: dateonly.New(2026, 3, 2)},
			tasks: 2,
		},
		{
			name:  "an open-ended window",
			f:     oracleExportFilters{DateFrom: dateonly.New(2026, 10, 1)},
			tasks: 2,
		},
		{name: "category=project", f: oracleExportFilters{Category: "project"}, tasks: 1},
		{name: "category=recurring", f: oracleExportFilters{Category: "recurring"}, tasks: 7},
		{name: "entity", f: oracleExportFilters{EntityKey: "e.uk"}, tasks: 1},
		{name: "obligation type", f: oracleExportFilters{ObligationKey: "o.cit"}, tasks: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Len(t, tenant.exportInstances(tc.f, false), tc.tasks)
		})
	}
}

func TestOracleExportWorkflowsWindowOnCreatedAt(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)
	created := map[string]dateonly.Date{
		"w1": dateonly.New(2026, 9, 6),
		"w2": dateonly.New(2026, 9, 6),
		"w3": dateonly.New(2026, 9, 6),
	}
	assert.Len(t, tenant.exportWorkflows(oracleExportFilters{}, created), 3,
		"every workflow status is exported, drafts included")
	assert.Len(t, tenant.exportWorkflows(oracleExportFilters{
		DateFrom: dateonly.New(2026, 9, 6), DateTo: dateonly.New(2026, 9, 6),
	}, created), 3, "the seed-date window holds them all")
	assert.Empty(t, tenant.exportWorkflows(oracleExportFilters{
		DateFrom: dateonly.New(2026, 1, 1), DateTo: dateonly.New(2026, 1, 31),
	}, created), "a window before the seed run holds none")
	assert.Len(t, tenant.exportWorkflows(oracleExportFilters{Category: "project"}, created), 1)
}

func TestOracleWorkflowStats(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)

	stats := tenant.byWfKey["w1"].stats()
	assert.Equal(t, 6, stats.TotalTasks)
	assert.Equal(t, 2, stats.CompletedTasks)
	assert.Equal(t, 33, stats.CompletionPercent, "2/6 rounds to 33")
	assert.Equal(t, "2026-03-02", stats.NextDueDate.String(),
		"the earliest due date among the not-yet-completed instances")

	done := tenant.byWfKey["w2"].stats()
	assert.Equal(t, 1, done.TotalTasks)
	assert.Equal(t, 100, done.CompletionPercent)
	assert.True(t, done.NextDueDate.IsZero(), "nothing outstanding renders as null")
}

func TestOracleWorkflowStatsRounding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		total     int
		completed int
		want      int
	}{
		{total: 0, completed: 0, want: 0},
		{total: 3, completed: 1, want: 33},
		{total: 3, completed: 2, want: 67},
		{total: 8, completed: 1, want: 13}, // 12.5 rounds up
		{total: 6, completed: 2, want: 33},
		{total: 4, completed: 4, want: 100},
	}
	for _, tc := range tests {
		w := &oracleWorkflow{}
		for i := 0; i < tc.total; i++ {
			inst := &oracleInstance{Status: oracleStatusNotStarted, DueDate: dateonly.New(2026, 1, 1)}
			if i < tc.completed {
				inst.Status = oracleStatusCompleted
			}
			w.instances = append(w.instances, inst)
		}
		assert.Equal(t, tc.want, w.stats().CompletionPercent, "%d/%d", tc.completed, tc.total)
	}
}

func TestOracleDashboard(t *testing.T) {
	t.Parallel()
	tenant := oracleTestReportTenant(t)
	dashboard := tenant.dashboard()

	assert.Equal(t, "2026-09-06", dashboard.Today.String())
	assert.Equal(t, "2026-09-12", dashboard.WeekEnd.String())
	assert.Equal(t, 8, dashboard.TotalInstances, "every workflow's instances, project included")
	assert.Equal(t, 3, dashboard.Completed)
	assert.Equal(t, 5, dashboard.Active)
	assert.Equal(t, 2, dashboard.Overdue,
		"the two M2 tasks; the project task is due 30 September, still ahead")
	assert.Equal(t, 0, dashboard.DueToday)
	assert.Equal(t, 0, dashboard.ThisWeek)
	assert.Equal(t, 1, dashboard.AwaitingApproval, "only pending_approval counts")
	assert.Equal(t, 38, dashboard.CompletionRate, "3 of 8 over every workflow's stats")
	assert.Equal(t, map[string]int{
		oracleStatusCompleted:       3,
		oracleStatusInProgress:      1,
		oracleStatusNotStarted:      3,
		oracleStatusPendingApproval: 1,
	}, dashboard.ByStatus)
}

func TestOracleDashboardBuckets(t *testing.T) {
	t.Parallel()
	// The three urgency buckets are exclusive and hang off the tenant's today:
	// yesterday is overdue, today is dueToday, anything up to Saturday is this
	// week, and the following Sunday is neither.
	tests := []struct {
		name     string
		due      string
		overdue  int
		dueToday int
		thisWeek int
	}{
		{name: "yesterday", due: "2026-09-05", overdue: 1},
		{name: "today", due: "2026-09-06", dueToday: 1},
		{name: "tomorrow", due: "2026-09-07", thisWeek: 1},
		{name: "the week's Saturday", due: "2026-09-12", thisWeek: 1},
		{name: "the next Sunday", due: "2026-09-13"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tenant := &oracleTenant{today: oracleTestDate(t, "2026-09-06")}
			tenant.instances = []*oracleInstance{
				{Status: oracleStatusNotStarted, DueDate: oracleTestDate(t, tc.due), tenant: tenant},
			}
			tenant.workflows = []*oracleWorkflow{{instances: tenant.instances}}
			dashboard := tenant.dashboard()
			assert.Equal(t, tc.overdue, dashboard.Overdue, "overdue")
			assert.Equal(t, tc.dueToday, dashboard.DueToday, "dueToday")
			assert.Equal(t, tc.thisWeek, dashboard.ThisWeek, "thisWeek")
			assert.Equal(t, 1, dashboard.Active)
		})
	}
}

func TestOracleCompletedInstancesAreNeverBucketed(t *testing.T) {
	t.Parallel()
	tenant := &oracleTenant{today: dateonly.New(2026, 9, 6)}
	tenant.instances = []*oracleInstance{
		{Status: oracleStatusCompleted, DueDate: dateonly.New(2026, 1, 1), tenant: tenant,
			CompletedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
	}
	tenant.workflows = []*oracleWorkflow{{instances: tenant.instances}}
	dashboard := tenant.dashboard()
	assert.Equal(t, 0, dashboard.Overdue, "a completed task is never overdue, however old")
	assert.Equal(t, 1, dashboard.Completed)
	assert.Equal(t, 100, dashboard.CompletionRate)
}
