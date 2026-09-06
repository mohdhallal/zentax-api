package main

// The report checks: every endpoint the dataset has expectations for, called
// with every filter combination those expectations cover plus the unfiltered
// set, diffed against the oracle.
//
// The dataset's own numbers are never the yardstick here — the oracle is.
// They are compared separately (verifyStaticTables) as the dataset's internal
// consistency check, which is why that comparison needs no live API at all.

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// verifyReportLimit asks for the whole set in one page (the endpoints' cap).
const verifyReportLimit = 5000

// verifyDefaultRowLimit is the row cap the endpoints apply when none is given
// — what "rowsReturnedAtDefaultLimit" in the dataset means.
const verifyDefaultRowLimit = 1000

// verifyGroupBys are every tax-financial grouping; the dataset's static table
// carries four of the five, and the oracle knows all five.
var verifyGroupBys = []string{
	oracleGroupEntity, oracleGroupCountry, oracleGroupTaxType,
	oracleGroupPeriod, oracleGroupObligation,
}

// verifyFilterKeys merges the filter keys the dataset declares with the ones
// verify always runs, in a stable order.
func verifyFilterKeys(declared []string, always ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(declared)+len(always))
	for _, k := range append(append([]string{}, always...), declared...) {
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// compliance-heatmap
// ---------------------------------------------------------------------------

func (r *verifyRun) checkHeatmaps() {
	declared := []string{}
	if e, ok := r.expectations(); ok {
		declared = verifyExpectationKeys(e.ComplianceHeatmap)
	}
	for _, key := range verifyFilterKeys(declared, "viewMode=period", "viewMode=tax-type") {
		r.checkHeatmap(key)
	}
}

func (r *verifyRun) checkHeatmap(key string) {
	c := r.check("compliance-heatmap", key)
	fk := verifyParseFilterKey(key)
	viewMode := fk.Params["viewMode"]
	if viewMode == "" {
		viewMode = oracleViewPeriod
	}
	params, err := fk.query(r.ids, r.ids.seedDates())
	if err != nil {
		c.errf("%v", err)
		return
	}
	params["viewMode"] = viewMode

	var payload verifyHeatmapPayload
	path := verifyQuery("/reports/compliance-heatmap", params)
	if err := r.client.api.GET(r.ctx, path, nil, &payload); err != nil {
		c.errf("GET %s: %v", path, err)
		return
	}

	want := r.tenant.heatmap(fk.oracleFilters(), viewMode)
	c.equal("summary.totalCells", "", want.Summary.TotalCells, payload.Summary.TotalCells)
	c.equal("summary.green", "", want.Summary.Green, payload.Summary.Green)
	c.equal("summary.amber", "", want.Summary.Amber, payload.Summary.Amber)
	c.equal("summary.red", "", want.Summary.Red, payload.Summary.Red)
	c.equal("cellCount", "", len(want.Cells), len(payload.Cells))

	seen := map[string]bool{}
	for _, cell := range payload.Cells {
		rowKey := r.ids.entityKeyOf(cell.RowID)
		colKey := cell.ColID
		if viewMode == oracleViewTaxType {
			colKey = r.ids.obligationKeyOf(cell.ColID)
		}
		cellKey := oracleHeatmapCellKey(rowKey, colKey)
		expected, ok := want.Cells[cellKey]
		if !ok {
			c.diff("cell", cellKey, "no such cell", fmt.Sprintf("%d tasks", cell.TotalTasks))
			continue
		}
		seen[cellKey] = true
		c.equal("rowLabel", cellKey, expected.RowLabel, cell.RowLabel)
		c.equal("colLabel", cellKey, expected.ColLabel, cell.ColLabel)
		c.equal("totalTasks", cellKey, expected.TotalTasks, cell.TotalTasks)
		c.equal("completedTasks", cellKey, expected.CompletedTasks, cell.CompletedTasks)
		c.equal("overdueTasks", cellKey, expected.OverdueTasks, cell.OverdueTasks)
		c.equal("inProgressTasks", cellKey, expected.InProgressTasks, cell.InProgressTasks)
		c.equal("status", cellKey, expected.status(), cell.Status)

		gotWorkflows := make([]string, 0, len(cell.WorkflowIDs))
		for _, id := range cell.WorkflowIDs {
			gotWorkflows = append(gotWorkflows, r.ids.workflowKeyOf(id))
		}
		wantWorkflows := make([]string, 0, len(expected.WorkflowKeys))
		for k := range expected.WorkflowKeys {
			wantWorkflows = append(wantWorkflows, k)
		}
		c.equal("workflowIds", cellKey, verifySortedStrings(wantWorkflows), verifySortedStrings(gotWorkflows))
	}
	for cellKey := range want.Cells {
		if !seen[cellKey] {
			c.diff("cell", cellKey, "present", "missing")
		}
	}

	gotCols := make([]string, 0, len(payload.Cols))
	for _, col := range payload.Cols {
		if viewMode == oracleViewTaxType {
			gotCols = append(gotCols, r.ids.obligationKeyOf(col.ID))
			continue
		}
		gotCols = append(gotCols, col.ID)
	}
	c.equal("colOrder", "", want.ColOrder, gotCols)

	gotRows := make([]string, 0, len(payload.Rows))
	for _, row := range payload.Rows {
		gotRows = append(gotRows, r.ids.entityKeyOf(row.ID))
	}
	c.equal("rowOrder", "", want.RowOrder, gotRows)
}

// ---------------------------------------------------------------------------
// compliance-status
// ---------------------------------------------------------------------------

func (r *verifyRun) checkComplianceStatus() {
	declared := []string{}
	if e, ok := r.expectations(); ok {
		declared = verifyExpectationKeys(e.ComplianceStatus)
	}
	for _, key := range verifyFilterKeys(declared, "all") {
		r.checkComplianceStatusFilter(key)
	}
	r.checkCompliancePaging()
}

func (r *verifyRun) checkComplianceStatusFilter(key string) {
	c := r.check("compliance-status", key)
	fk := verifyParseFilterKey(key)
	params, err := fk.query(r.ids, r.ids.seedDates())
	if err != nil {
		c.errf("%v", err)
		return
	}
	params["limit"] = strconv.Itoa(verifyReportLimit)

	var payload verifyCompliancePayload
	path := verifyQuery("/reports/compliance-status", params)
	if err := r.client.api.GET(r.ctx, path, nil, &payload); err != nil {
		c.errf("GET %s: %v", path, err)
		return
	}

	want := r.tenant.compliance(fk.oracleFilters())
	// The summary describes the whole classified set whatever the status
	// filter narrows the rows to (product fix F5).
	c.equal("summary.total", "", want.Summary.Total, payload.Summary.Total)
	c.equal("summary.onTime", "", want.Summary.OnTime, payload.Summary.OnTime)
	c.equal("summary.late", "", want.Summary.Late, payload.Summary.Late)
	c.equal("summary.missed", "", want.Summary.Missed, payload.Summary.Missed)
	c.equal("summary.notDue", "", want.Summary.NotDue, payload.Summary.NotDue)

	status := fk.Params["status"]
	rows := want.rowsWithClass(status)
	c.equal("totalCount", "", len(rows), payload.TotalCount)
	c.equal("rowsReturned", "", len(rows), len(payload.Rows))

	byInstance := make(map[*oracleInstance]*oracleComplianceRow, len(rows))
	for _, row := range rows {
		byInstance[row.Instance] = row
	}
	seen := map[*oracleInstance]bool{}
	for _, got := range payload.Rows {
		inst, ok := r.byLiveID[got.TaskInstanceID]
		if !ok {
			c.diff("row", got.Period+"/"+got.ObligationName, "a known task instance", got.TaskInstanceID)
			continue
		}
		expected, ok := byInstance[inst]
		if !ok {
			c.diff("row", inst.ref(), "not in this filtered set", got.ComplianceStatus)
			continue
		}
		seen[inst] = true
		ref := inst.ref()
		c.equal("complianceStatus", ref, expected.Class, got.ComplianceStatus)
		c.equal("filingDeadline", ref, expected.FilingDeadline, got.FilingDeadline)
		c.equal("entityName", ref, expected.EntityName, got.EntityName)
		c.equal("obligationName", ref, expected.ObligationName, got.ObligationName)
		c.equal("obligationCode", ref, expected.ObligationCode, got.ObligationCode)
		c.equal("taxType", ref, expected.TaxType, got.TaxType)
		c.equal("period", ref, expected.Period, got.Period)
		c.equal("penaltyInterest", ref, expected.PenaltyInterest, got.PenaltyInterest)
		// filingDate is the completion instant; the civil date a tenant reads
		// it as is what "filed on time" means.
		if expected.CompletedOn.IsZero() {
			c.equal("filingDate", ref, nil, got.FilingDate)
		} else if got.FilingDate == nil {
			c.diff("filingDate", ref, expected.CompletedOn, nil)
		} else {
			c.equal("filingDate (tenant civil date)", ref,
				expected.CompletedOn, dateonly.FromTime(got.FilingDate.In(r.tenant.zone)))
		}
	}
	for inst := range byInstance {
		if !seen[inst] {
			c.diff("row", inst.ref(), "present", "missing")
		}
	}

	// Row order: entity name, then obligation name, then period code.
	order := r.check("compliance-status/order", key)
	prev := ""
	for _, got := range payload.Rows {
		current := got.EntityName + "\x00" + got.ObligationName + "\x00" + got.Period
		if prev != "" && current < prev {
			order.diff("order", got.TaskInstanceID, "not before "+strings.ReplaceAll(prev, "\x00", " / "),
				strings.ReplaceAll(current, "\x00", " / "))
			break
		}
		prev = current
	}
}

// checkCompliancePaging walks the unfiltered set in pages: the union must be
// the whole set, with no duplicates and a stable totalCount.
func (r *verifyRun) checkCompliancePaging() {
	c := r.check("compliance-status/paging", "limit=100")
	want := r.tenant.compliance(oracleFilters{})
	if len(want.Rows) == 0 {
		c.skip("the tenant has no classified instance to page")
		return
	}

	seen := map[string]bool{}
	total := -1
	for offset := 0; ; offset += 100 {
		var payload verifyCompliancePayload
		path := verifyQuery("/reports/compliance-status", map[string]string{
			"limit": "100", "offset": strconv.Itoa(offset),
		})
		if err := r.client.api.GET(r.ctx, path, nil, &payload); err != nil {
			c.errf("GET %s: %v", path, err)
			return
		}
		if total < 0 {
			total = payload.TotalCount
		}
		c.equal("totalCount@offset="+strconv.Itoa(offset), "", len(want.Rows), payload.TotalCount)
		for _, row := range payload.Rows {
			if seen[row.TaskInstanceID] {
				c.diff("row", row.TaskInstanceID, "once", "twice across pages")
			}
			seen[row.TaskInstanceID] = true
		}
		if len(payload.Rows) == 0 || offset+len(payload.Rows) >= payload.TotalCount {
			break
		}
	}
	c.equal("rowsAcrossPages", "", len(want.Rows), len(seen))
}

// ---------------------------------------------------------------------------
// tax-financial
// ---------------------------------------------------------------------------

func (r *verifyRun) checkTaxFinancial() {
	declared := []string{}
	if e, ok := r.expectations(); ok {
		declared = verifyExpectationKeys(e.TaxFinancial)
	}
	for _, key := range verifyFilterKeys(declared, "all") {
		for _, groupBy := range verifyGroupBys {
			r.checkTaxFinancialFilter(key, groupBy)
		}
	}
}

func (r *verifyRun) checkTaxFinancialFilter(key, groupBy string) {
	c := r.check("tax-financial", key+" groupBy="+groupBy)
	fk := verifyParseFilterKey(key)
	params, err := fk.query(r.ids, r.ids.seedDates())
	if err != nil {
		c.errf("%v", err)
		return
	}
	params["groupBy"] = groupBy
	params["limit"] = strconv.Itoa(verifyReportLimit)

	var payload verifyFinancialPayload
	path := verifyQuery("/reports/tax-financial", params)
	if err := r.client.api.GET(r.ctx, path, nil, &payload); err != nil {
		c.errf("GET %s: %v", path, err)
		return
	}

	want := r.tenant.financial(fk.oracleFilters(), groupBy)
	c.equal("summary.recordCount", "", want.RecordCount, payload.Summary.RecordCount)
	c.equal("totalCount", "", want.RecordCount, payload.TotalCount)
	for _, field := range oracleFigureFields {
		c.money("summary."+field, "", want.Summary.field(field), payload.summaryFigure(field))
	}

	// Aggregated buckets, translated back into dataset keys.
	seen := map[string]bool{}
	for _, group := range payload.Aggregated {
		bucket := r.financialGroupKey(groupBy, group.Key)
		expected, ok := want.Groups[bucket]
		if !ok {
			c.diff("group", bucket, "no such bucket", group.Label)
			continue
		}
		seen[bucket] = true
		c.equal("group.label", bucket, expected.Label, group.Label)
		c.equal("group.count", bucket, expected.Count, group.Count)
		for _, field := range oracleFigureFields {
			c.money("group."+field, bucket, expected.Figures.field(field), group.field(field))
		}
	}
	for bucket := range want.Groups {
		if !seen[bucket] {
			c.diff("group", bucket, "present", "missing")
		}
	}

	// The chart follows the calendar, not period-code text (product fix F4).
	gotPeriods := make([]string, 0, len(payload.ChartData))
	for _, point := range payload.ChartData {
		gotPeriods = append(gotPeriods, point.Period)
	}
	c.equal("chartPeriodOrder", "", want.ChartOrder, gotPeriods)
	for i, point := range payload.ChartData {
		if i >= len(want.Chart) {
			break
		}
		expected := want.Chart[i]
		if expected.Key != point.Period {
			continue // the order difference above already says it
		}
		c.money("chart.netVat", point.Period, &expected.Figures.NetVat, point.NetVat)
		c.money("chart.totalAmount", point.Period, &expected.Figures.TotalAmount, point.TotalAmount)
		c.money("chart.taxLiability", point.Period, &expected.Figures.TaxLiability, point.TaxLiability)
		c.money("chart.whtAmount", point.Period, &expected.Figures.WhtAmount, point.WhtAmount)
		c.money("chart.outputVat", point.Period, &expected.Figures.OutputVat, point.OutputVat)
		c.money("chart.inputVat", point.Period, &expected.Figures.InputVat, point.InputVat)
	}

	// The detail rows carry the same figures per instance; one grouping is
	// enough to prove them (the rows do not depend on groupBy).
	if groupBy != oracleGroupEntity {
		return
	}
	wantRows := make([]string, 0, len(want.Rows))
	for _, row := range want.Rows {
		wantRows = append(wantRows, verifyFinancialRowKey(
			row.EntityKey, row.Country, row.TaxType, row.ObligationCode, row.Period, row.FinancialYear,
			verifyFiguresText(row.Figures)))
	}
	gotRows := make([]string, 0, len(payload.Rows))
	for _, row := range payload.Rows {
		figures := make([]string, 0, len(oracleFigureFields))
		for _, field := range oracleFigureFields {
			figures = append(figures, verifyNumberText(row.field(field)))
		}
		gotRows = append(gotRows, verifyFinancialRowKey(
			r.ids.entityKeyOf(row.EntityID), row.Country, row.TaxType, row.ObligationCode,
			row.Period, row.FinancialYear, figures))
	}
	verifyCompareMultisets(c, "row", wantRows, gotRows)
}

// financialGroupKey translates a live bucket key into dataset-key space.
func (r *verifyRun) financialGroupKey(groupBy, key string) string {
	switch groupBy {
	case oracleGroupEntity:
		return r.ids.entityKeyOf(key)
	case oracleGroupObligation:
		return r.ids.obligationKeyOf(key)
	default:
		return key
	}
}

func verifyFinancialRowKey(entityKey, country, taxType, obligationCode, period, year string, figures []string) string {
	return strings.Join(append([]string{entityKey, country, taxType, obligationCode, period, year}, figures...), "|")
}

func verifyFiguresText(f *oracleFigures) []string {
	out := make([]string, 0, len(oracleFigureFields))
	for _, field := range oracleFigureFields {
		out = append(out, strconv.FormatFloat(oracleRatFloat(f.field(field)), 'f', -1, 64))
	}
	return out
}

// verifyNumberText renders a payload number the same way the oracle's side is
// rendered, so a row key compares like for like.
func verifyNumberText(n json.Number) string {
	if n == "" {
		return "0"
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// money diffs an exact amount against the payload's number. The comparison is
// done in float64 — the one cast Postgres itself makes when it serializes an
// exact numeric sum.
func (c *verifyCheck) money(field, key string, want *big.Rat, got json.Number) {
	gotFloat, err := got.Float64()
	if err != nil {
		c.diff(field, key, want, got.String())
		return
	}
	wantFloat := oracleRatFloat(want)
	if wantFloat != gotFloat {
		c.diff(field, key,
			strconv.FormatFloat(wantFloat, 'f', -1, 64),
			strconv.FormatFloat(gotFloat, 'f', -1, 64))
	}
}

// ---------------------------------------------------------------------------
// export-raw
// ---------------------------------------------------------------------------

func (r *verifyRun) checkExportRaw() {
	declared := []string{}
	if e, ok := r.expectations(); ok {
		declared = verifyExpectationKeys(e.ExportRaw)
	}
	for _, key := range verifyFilterKeys(declared,
		oracleDatasetWorkflows, oracleDatasetTasks, oracleDatasetTaxData) {
		r.checkExportRawFilter(key)
	}
}

func (r *verifyRun) checkExportRawFilter(key string) {
	c := r.check("export-raw", key)
	fk := verifyParseFilterKey(key)
	dataset := fk.dataset()
	seedDates := r.ids.seedDates()

	params, err := fk.query(r.ids, seedDates)
	if err != nil {
		c.errf("%v", err)
		return
	}
	delete(params, "viewMode")
	params["dataset"] = dataset
	params["limit"] = strconv.Itoa(verifyReportLimit)

	filters, err := fk.exportFilters(seedDates)
	if err != nil {
		c.errf("%v", err)
		return
	}

	var payload verifyExportPayload
	path := verifyQuery("/reports/export-raw", params)
	if err := r.client.api.GET(r.ctx, path, nil, &payload); err != nil {
		c.errf("GET %s: %v", path, err)
		return
	}
	c.equal("dataset", "", dataset, payload.Dataset)

	var wantRows []string
	switch dataset {
	case oracleDatasetWorkflows:
		for _, w := range r.tenant.exportWorkflows(filters, r.ids.workflowCreated) {
			wantRows = append(wantRows, verifyExportWorkflowKey(w))
		}
	case oracleDatasetTaxData:
		for _, inst := range r.tenant.exportInstances(filters, true) {
			wantRows = append(wantRows, verifyExportTaxDataKey(inst))
		}
	default:
		for _, inst := range r.tenant.exportInstances(filters, false) {
			wantRows = append(wantRows, verifyExportTaskKey(inst))
		}
	}

	c.equal("totalCount", "", len(wantRows), payload.TotalCount)
	c.equal("rowsReturned", "", len(wantRows), len(payload.Rows))

	gotRows := make([]string, 0, len(payload.Rows))
	for _, row := range payload.Rows {
		switch dataset {
		case oracleDatasetWorkflows:
			gotRows = append(gotRows, verifyExportRowKey(row,
				"workflowName", "category", "status", "financialYear", "entityName", "obligationCode",
				"periodicity", "projectType"))
		case oracleDatasetTaxData:
			gotRows = append(gotRows, verifyExportRowKey(row,
				"workflowName", "periodCode", "taskName", "taxType", "financialYear", "taxDataStatus",
				"outputVat", "inputVat", "netVat", "taxableIncome", "taxLiability", "whtAmount",
				"penaltyAmount", "interestAmount", "engagementCost"))
		default:
			gotRows = append(gotRows, verifyExportRowKey(row,
				"workflowName", "periodCode", "taskName", "status", "taxDataStatus",
				"dueDate", "filingDeadline", "assigneeName", "workflowCategory"))
		}
	}
	verifyCompareMultisets(c, "row", wantRows, gotRows)
}

func verifyExportWorkflowKey(w *oracleWorkflow) string {
	entity, obligation, periodicity, projectType := "", "", "", ""
	if w.entity != nil {
		entity = w.entity.Name
	}
	if w.obligation != nil {
		obligation = w.obligation.Code
	}
	if w.spec.Periodicity != nil {
		periodicity = *w.spec.Periodicity
	}
	if w.spec.ProjectType != nil {
		projectType = *w.spec.ProjectType
	}
	return strings.Join([]string{
		w.spec.Name, w.spec.Category, w.status, w.spec.FinancialYear,
		entity, obligation, periodicity, projectType,
	}, "|")
}

func verifyExportTaskKey(inst *oracleInstance) string {
	return strings.Join([]string{
		inst.workflow.spec.Name, inst.PeriodCode, inst.Name, inst.Status, inst.TaxDataStatus,
		inst.DueDate.String(), inst.FilingDeadline.String(), inst.AssigneeName, inst.workflow.spec.Category,
	}, "|")
}

func verifyExportTaxDataKey(inst *oracleInstance) string {
	f := oracleExportTaxDataFigures(inst.TaxData)
	amounts := []*big.Rat{
		&f.OutputVat, &f.InputVat, &f.NetVat, &f.TaxableIncome, &f.TaxLiability,
		&f.WhtAmount, &f.PenaltyAmount, &f.InterestAmount, &f.EngagementCost,
	}
	parts := []string{
		inst.workflow.spec.Name, inst.PeriodCode, inst.Name, inst.taxType(),
		inst.workflow.spec.FinancialYear, inst.TaxDataStatus,
	}
	for _, a := range amounts {
		parts = append(parts, strconv.FormatFloat(oracleRatFloat(a), 'f', -1, 64))
	}
	return strings.Join(parts, "|")
}

// verifyExportRowKey renders the named columns of one payload row, matching
// how the oracle's side is rendered (numbers as plain decimals, absent
// columns as "").
func verifyExportRowKey(row map[string]any, columns ...string) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		switch v := row[column].(type) {
		case nil:
			parts = append(parts, "")
		case string:
			parts = append(parts, v)
		case json.Number:
			parts = append(parts, verifyNumberText(v))
		case bool:
			parts = append(parts, strconv.FormatBool(v))
		default:
			parts = append(parts, fmt.Sprint(v))
		}
	}
	return strings.Join(parts, "|")
}

// verifyCompareMultisets diffs two bags of row keys, naming what is missing
// and what is unexpected.
func verifyCompareMultisets(c *verifyCheck, field string, want, got []string) {
	counts := map[string]int{}
	for _, key := range want {
		counts[key]++
	}
	for _, key := range got {
		counts[key]--
	}
	keys := make([]string, 0, len(counts))
	for key, delta := range counts {
		if delta != 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if counts[key] > 0 {
			c.diff(field, key, fmt.Sprintf("%d occurrence(s)", counts[key]), "missing")
			continue
		}
		c.diff(field, key, "not in the expected set", fmt.Sprintf("%d occurrence(s)", -counts[key]))
	}
}

// ---------------------------------------------------------------------------
// The dataset's own static tables
// ---------------------------------------------------------------------------

// verifyStaticTables compares the numbers written in the dataset's
// expectedAsOf block against the oracle recomputed for that same date. It
// needs no live API: it asks whether the dataset's human-facing tables still
// describe the dataset. Drift is INFO by default — the recomputed oracle is
// the authority — and a failure under --strict-static.
func verifyStaticTables(report *verifyReport, loaded *spec.Spec, tenantKey string, strict bool) {
	asOf := loaded.AsOf.String()
	byTenant, ok := loaded.ExpectedAsOf[asOf]
	if !ok {
		return
	}
	expectations, ok := byTenant[tenantKey]
	if !ok {
		return
	}
	c := report.check(tenantKey, "static-expectations", "asOf="+asOf)
	record := c.info
	if strict {
		record = c.diff
	}

	// The tables are written for one date; recompute the world at that date so
	// the comparison is about the dataset's arithmetic, not about today.
	world, err := buildOracleWorld(loaded, loaded.AsOf.Time(), loaded.AsOf)
	if err != nil {
		c.errf("recompute at %s: %v", asOf, err)
		return
	}
	tenant, ok := world.byKey[tenantKey]
	if !ok {
		c.errf("tenant %s is not in the spec", tenantKey)
		return
	}

	for _, key := range verifyExpectationKeys(expectations.ComplianceHeatmap) {
		want := expectations.ComplianceHeatmap[key]
		fk := verifyParseFilterKey(key)
		viewMode := fk.Params["viewMode"]
		if viewMode == "" {
			viewMode = oracleViewPeriod
		}
		got := tenant.heatmap(fk.oracleFilters(), viewMode)
		prefix := "heatmap[" + key + "]."
		verifyStaticInt(record, prefix+"summary.totalCells", "", want.Summary.TotalCells, got.Summary.TotalCells)
		verifyStaticInt(record, prefix+"summary.green", "", want.Summary.Green, got.Summary.Green)
		verifyStaticInt(record, prefix+"summary.amber", "", want.Summary.Amber, got.Summary.Amber)
		verifyStaticInt(record, prefix+"summary.red", "", want.Summary.Red, got.Summary.Red)
		if len(want.ColOrder) > 0 && verifyValue(want.ColOrder) != verifyValue(got.ColOrder) {
			record(prefix+"colOrder", "", want.ColOrder, got.ColOrder)
		}
		for cellKey, cell := range want.Cells {
			gotCell, ok := got.Cells[cellKey]
			if !ok {
				record(prefix+"cell", cellKey, "present", "missing")
				continue
			}
			verifyStaticInt(record, prefix+"totalTasks", cellKey, cell.TotalTasks, gotCell.TotalTasks)
			verifyStaticInt(record, prefix+"completedTasks", cellKey, cell.CompletedTasks, gotCell.CompletedTasks)
			verifyStaticInt(record, prefix+"overdueTasks", cellKey, cell.OverdueTasks, gotCell.OverdueTasks)
			verifyStaticInt(record, prefix+"completedLate", cellKey, cell.CompletedLate, gotCell.CompletedLate)
			verifyStaticInt(record, prefix+"inProgressTasks", cellKey, cell.InProgressTasks, gotCell.InProgressTasks)
			if cell.Status != "" && cell.Status != gotCell.status() {
				record(prefix+"status", cellKey, cell.Status, gotCell.status())
			}
		}
	}

	for _, key := range verifyExpectationKeys(expectations.ComplianceStatus) {
		want := expectations.ComplianceStatus[key]
		fk := verifyParseFilterKey(key)
		got := tenant.compliance(fk.oracleFilters())
		rows := got.rowsWithClass(fk.Params["status"])
		prefix := "compliance[" + key + "]."
		verifyStaticInt(record, prefix+"summary.total", "", want.Summary.Total, got.Summary.Total)
		verifyStaticInt(record, prefix+"summary.onTime", "", want.Summary.OnTime, got.Summary.OnTime)
		verifyStaticInt(record, prefix+"summary.late", "", want.Summary.Late, got.Summary.Late)
		verifyStaticInt(record, prefix+"summary.missed", "", want.Summary.Missed, got.Summary.Missed)
		verifyStaticInt(record, prefix+"summary.notDue", "", want.Summary.NotDue, got.Summary.NotDue)
		verifyStaticInt(record, prefix+"totalCount", "", want.TotalCount, len(rows))
		expectedRows := len(rows)
		if expectedRows > verifyDefaultRowLimit {
			expectedRows = verifyDefaultRowLimit
		}
		verifyStaticInt(record, prefix+"rowsReturnedAtDefaultLimit", "", want.RowsReturnedAtDefaultLimit, expectedRows)
	}

	for _, key := range verifyExpectationKeys(expectations.TaxFinancial) {
		want := expectations.TaxFinancial[key]
		fk := verifyParseFilterKey(key)
		prefix := "taxFinancial[" + key + "]."
		got := tenant.financial(fk.oracleFilters(), oracleGroupEntity)
		verifyStaticInt(record, prefix+"summary.recordCount", "", want.Summary.RecordCount, got.RecordCount)
		verifyStaticInt(record, prefix+"totalCount", "", want.TotalCount, got.RecordCount)
		verifyStaticMoney(record, prefix+"totalOutputVat", "", want.Summary.TotalOutputVat, &got.Summary.OutputVat)
		verifyStaticMoney(record, prefix+"totalInputVat", "", want.Summary.TotalInputVat, &got.Summary.InputVat)
		verifyStaticMoney(record, prefix+"totalNetVat", "", want.Summary.TotalNetVat, &got.Summary.NetVat)
		verifyStaticMoney(record, prefix+"totalTaxableIncome", "", want.Summary.TotalTaxableIncome, &got.Summary.TaxableIncome)
		verifyStaticMoney(record, prefix+"totalTaxLiability", "", want.Summary.TotalTaxLiability, &got.Summary.TaxLiability)
		verifyStaticMoney(record, prefix+"totalWht", "", want.Summary.TotalWht, &got.Summary.WhtAmount)
		verifyStaticMoney(record, prefix+"totalEngagementCost", "", want.Summary.TotalEngagementCost, &got.Summary.EngagementCost)
		verifyStaticMoney(record, prefix+"totalAmount", "", want.Summary.TotalAmount, &got.Summary.TotalAmount)
		if len(want.ChartPeriodOrder) > 0 && verifyValue(want.ChartPeriodOrder) != verifyValue(got.ChartOrder) {
			record(prefix+"chartPeriodOrder", "", want.ChartPeriodOrder, got.ChartOrder)
		}
		verifyStaticGroups(record, prefix+"byEntity", want.AggregatedByEntity, tenant.financial(fk.oracleFilters(), oracleGroupEntity))
		verifyStaticGroups(record, prefix+"byCountry", want.AggregatedByCountry, tenant.financial(fk.oracleFilters(), oracleGroupCountry))
		verifyStaticGroups(record, prefix+"byTaxType", want.AggregatedByTaxType, tenant.financial(fk.oracleFilters(), oracleGroupTaxType))
		verifyStaticGroups(record, prefix+"byPeriod", want.AggregatedByPeriod, tenant.financial(fk.oracleFilters(), oracleGroupPeriod))
	}

	for _, key := range verifyExpectationKeys(expectations.ExportRaw) {
		want := expectations.ExportRaw[key]
		fk := verifyParseFilterKey(key)
		dataset := fk.dataset()
		filters, err := fk.exportFilters(nil)
		if err != nil {
			// A SEED_DATE window cannot be evaluated without a live tenant to
			// read the workflows' creation dates from; verify checks it there.
			continue
		}
		windowed := !filters.DateFrom.IsZero() || !filters.DateTo.IsZero()
		var got int
		switch {
		case dataset == oracleDatasetWorkflows && windowed:
			continue // same reason: created_at is a fact about the seed run
		case dataset == oracleDatasetWorkflows:
			got = len(tenant.exportWorkflows(filters, nil))
		case dataset == oracleDatasetTaxData:
			got = len(tenant.exportInstances(filters, true))
		default:
			got = len(tenant.exportInstances(filters, false))
		}
		verifyStaticInt(record, "exportRaw["+key+"]", "", want, got)
	}

	for _, key := range verifyExpectationKeys(expectations.WorkflowStats) {
		want := expectations.WorkflowStats[key]
		w, ok := tenant.byWfKey[key]
		if !ok {
			record("workflowStats", key, "a workflow in the spec", "missing")
			continue
		}
		got := w.stats()
		verifyStaticInt(record, "workflowStats.totalTasks", key, want.TotalTasks, got.TotalTasks)
		verifyStaticInt(record, "workflowStats.completedTasks", key, want.CompletedTasks, got.CompletedTasks)
		verifyStaticInt(record, "workflowStats.completionPercent", key, want.CompletionPercent, got.CompletionPercent)
		if want.NextDueDate != got.NextDueDate {
			record("workflowStats.nextDueDate", key, want.NextDueDate, got.NextDueDate)
		}
	}

	dashboard := tenant.dashboard()
	want := expectations.Dashboard
	verifyStaticInt(record, "dashboard.totalInstances", "", want.TotalInstances, dashboard.TotalInstances)
	verifyStaticInt(record, "dashboard.overdue", "", want.Overdue, dashboard.Overdue)
	verifyStaticInt(record, "dashboard.dueToday", "", want.DueToday, dashboard.DueToday)
	verifyStaticInt(record, "dashboard.thisWeek", "", want.ThisWeek, dashboard.ThisWeek)
	verifyStaticInt(record, "dashboard.active", "", want.Active, dashboard.Active)
	verifyStaticInt(record, "dashboard.completed", "", want.Completed, dashboard.Completed)
	verifyStaticInt(record, "dashboard.awaitingApproval", "", want.AwaitingApproval, dashboard.AwaitingApproval)
	verifyStaticInt(record, "dashboard.completionRate", "", want.CompletionRate, dashboard.CompletionRate)
	if !want.WeekEnd.IsZero() && want.WeekEnd != dashboard.WeekEnd {
		record("dashboard.weekEnd", "", want.WeekEnd, dashboard.WeekEnd)
	}
	for status, n := range want.ByStatus {
		verifyStaticInt(record, "dashboard.byStatus", status, n, dashboard.ByStatus[status])
	}
}

type verifyRecordFunc func(field, key string, expected, actual any)

func verifyStaticInt(record verifyRecordFunc, field, key string, want, got int) {
	if want != got {
		record(field, key, want, got)
	}
}

func verifyStaticMoney(record verifyRecordFunc, field, key string, want json.Number, got *big.Rat) {
	wantRat, err := oracleParseRat(want)
	if err != nil {
		record(field, key, want.String(), oracleRatString(got))
		return
	}
	if wantRat.Cmp(got) != 0 {
		record(field, key, oracleRatString(wantRat), oracleRatString(got))
	}
}

func verifyStaticGroups(
	record verifyRecordFunc, field string,
	want map[string]spec.TaxFinancialAggregate, got *oracleFinancial,
) {
	for key, aggregate := range want {
		group, ok := got.Groups[key]
		if !ok {
			record(field, key, "present", "missing")
			continue
		}
		verifyStaticInt(record, field+".count", key, aggregate.Count, group.Count)
		verifyStaticMoney(record, field+".outputVat", key, aggregate.OutputVat, &group.Figures.OutputVat)
		verifyStaticMoney(record, field+".inputVat", key, aggregate.InputVat, &group.Figures.InputVat)
		verifyStaticMoney(record, field+".netVat", key, aggregate.NetVat, &group.Figures.NetVat)
		verifyStaticMoney(record, field+".taxableIncome", key, aggregate.TaxableIncome, &group.Figures.TaxableIncome)
		verifyStaticMoney(record, field+".taxLiability", key, aggregate.TaxLiability, &group.Figures.TaxLiability)
		verifyStaticMoney(record, field+".whtAmount", key, aggregate.WhtAmount, &group.Figures.WhtAmount)
		verifyStaticMoney(record, field+".engagementCost", key, aggregate.EngagementCost, &group.Figures.EngagementCost)
		verifyStaticMoney(record, field+".totalAmount", key, aggregate.TotalAmount, &group.Figures.TotalAmount)
	}
	for key := range got.Groups {
		if _, ok := want[key]; !ok && len(want) > 0 {
			record(field, key, "not in the dataset's table", "present in the recomputation")
		}
	}
}
