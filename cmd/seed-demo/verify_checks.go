package main

// The plumbing checks: the ground the reports stand on.
//
// A report can only be right if the instances underneath it are right, so
// verify first proves the tenant registry row, the key→id resolution, every
// instance's dates and state, and the planner behind them (GET
// /workflows/{id}/preview and GET /entities/{id}/periods — the engine as the
// API exposes it). The report checks in verify_reports.go then diff the
// aggregations over exactly those instances.

import (
	"fmt"
	"math"
	"strconv"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// checkTenantRegistry proves the tenant row the report SQL reads "today" from.
func (r *verifyRun) checkTenantRegistry() {
	c := r.check("tenant-registry", "")
	tenant := r.client.identity.Tenant
	c.equal("slug", "", r.tenant.spec.Slug, tenant.Slug)
	c.equal("name", "", r.tenant.spec.Name, tenant.Name)
	// The zone decides every classification: a wrong one moves whole reports.
	c.equal("timezone", "", r.tenant.spec.Timezone, tenant.Timezone)
	c.equal("adminEmail", "", r.tenant.spec.Admin.Email, r.client.identity.Email)
}

// checkKeyResolution reports dataset keys the live tenant does not carry, and
// cross-checks the seeder's key → id file when one is on disk.
func (r *verifyRun) checkKeyResolution(idMap *seedOutput) {
	c := r.check("key-resolution", "")
	for _, problem := range r.ids.problems {
		c.diff("resolution", "", "resolvable", problem)
	}
	c.equal("entities", "", len(r.tenant.spec.Entities), len(r.ids.entities))
	c.equal("obligationTypes", "", len(r.tenant.spec.ObligationTypes), len(r.ids.obligations))
	c.equal("workflows", "", len(r.tenant.workflows), len(r.ids.workflows))

	if idMap == nil {
		return
	}
	var written *tenantOutput
	for i := range idMap.Tenants {
		if idMap.Tenants[i].Key == r.tenant.spec.Key {
			written = &idMap.Tenants[i]
			break
		}
	}
	s := r.check("seeder-id-map", "")
	if written == nil {
		s.skip("the key → id map does not describe tenant %s", r.tenant.spec.Key)
		return
	}

	s.equal("tenantId", "", r.client.identity.Tenant.ID, written.TenantID)
	s.equal("slug", "", r.tenant.spec.Slug, written.Slug)
	s.equal("timezone", "", r.tenant.spec.Timezone, written.Timezone)
	for key, id := range r.ids.entityID {
		s.equal("entityId", key, id, verifyMapValue(written.Entities, key))
	}
	for key, id := range r.ids.obligationID {
		s.equal("obligationTypeId", key, id, verifyMapValue(written.ObligationTypes, key))
	}
	for _, w := range r.tenant.workflows {
		liveID, ok := r.ids.workflowID[w.spec.Key]
		if !ok {
			continue
		}
		record, ok := written.Workflows[w.spec.Key]
		if !ok {
			s.diff("workflowId", w.spec.Key, liveID, nil)
			continue
		}
		s.equal("workflowId", w.spec.Key, liveID, record.ID)
		s.equal("workflowStarted", w.spec.Key, w.started, record.Started)
		s.equal("workflowStatus", w.spec.Key, w.status, record.Status)
		// The instance ids are what a browser-driving agent deep-links with:
		// they must be the rows verify just read.
		for _, inst := range w.instances {
			ref := verifyInstanceRef(w.spec.Key, inst.PeriodCode, inst.OrderIndex)
			row, ok := r.liveByRef[ref]
			if !ok {
				continue
			}
			s.equal("instanceId", ref, row.ID,
				verifyMapValue(record.Instances, inst.PeriodCode+"|"+inst.TemplateKey))
		}
	}
	s.equal("counts.instances", "", len(r.instances), written.Counts.Instances)
	s.equal("counts.workflows", "", len(r.tenant.workflows), written.Counts.Workflows)
}

// verifyMapValue reads a key out of a written map, rendering an absent one as
// a JSON null rather than an empty string.
func verifyMapValue(m map[string]string, key string) any {
	value, ok := m[key]
	if !ok {
		return nil
	}
	return value
}

// verifyInstanceRef is how an instance is addressed on both sides: its
// workflow, its period and its position in the workflow's task order.
func verifyInstanceRef(workflowKey, period string, orderIndex int) string {
	return workflowKey + "/" + period + "/#" + strconv.Itoa(orderIndex)
}

// checkInstances is the backbone check (and the one that catches an engine
// regression first): every generated instance's dates and state.
func (r *verifyRun) checkInstances() {
	expected := map[string]*oracleInstance{}
	for _, inst := range r.tenant.instances {
		expected[verifyInstanceRef(inst.workflowKey(), inst.PeriodCode, inst.OrderIndex)] = inst
	}

	c := r.check("task-instances", "")
	c.equal("instanceCount", "", len(r.tenant.instances), len(r.instances))

	seen := map[string]bool{}
	for _, row := range r.instances {
		workflowKey := r.ids.workflowKeyOf(row.WorkflowID)
		ref := verifyInstanceRef(workflowKey, row.PeriodCode, row.OrderIndex)
		r.liveByRef[ref] = row
		inst, ok := expected[ref]
		if !ok {
			c.diff("instance", ref, "no such instance in the dataset", row.Name)
			continue
		}
		if seen[ref] {
			c.diff("instance", ref, "one instance", "a duplicate row")
			continue
		}
		seen[ref] = true
		r.byLiveID[row.ID] = inst
		r.compareInstance(c, ref, inst, row)
	}
	for ref := range expected {
		if !seen[ref] {
			c.diff("instance", ref, "present", "missing")
		}
	}

	// Per-workflow counts, so a workflow that generated the wrong number of
	// instances is named rather than inferred from a pile of misses.
	counts := r.check("task-instances/per-workflow", "")
	live := map[string]int{}
	for _, row := range r.instances {
		live[r.ids.workflowKeyOf(row.WorkflowID)]++
	}
	for _, w := range r.tenant.workflows {
		counts.equal("instanceCount", w.spec.Key, len(w.instances), live[w.spec.Key])
	}
}

// compareInstance diffs one instance: the four engine dates, then the state
// the seed run left it in — including the exact completion instant, because a
// completion written as the civil date at midnight UTC still reads as the
// right day in some zones and the wrong one in others.
func (r *verifyRun) compareInstance(c *verifyCheck, ref string, inst *oracleInstance, row verifyInstanceRow) {
	c.equal("name", ref, inst.Name, row.Name)
	c.equal("taskType", ref, inst.TaskType, row.TaskType)
	c.equal("approvalRequired", ref, inst.ApprovalRequired, row.ApprovalRequired)

	c.equal("dueDate", ref, inst.DueDate, row.DueDate)
	c.equal("periodEndDate", ref, inst.PeriodEnd, row.PeriodEndDate)
	c.equal("filingDeadline", ref, inst.FilingDeadline, row.FilingDeadline)
	c.equal("paymentDeadline", ref, inst.PaymentDeadline, row.PaymentDeadline)

	c.equal("status", ref, inst.Status, row.Status)
	c.equal("taxDataStatus", ref, inst.TaxDataStatus, row.TaxDataStatus)
	c.equal("assigneeName", ref, verifyOptional(inst.AssigneeName), row.AssigneeName)
	if inst.AssigneeKey != "" && row.AssigneeID == nil {
		c.diff("assigneeId", ref, "set", "null")
	}
	if inst.Notes != "" {
		c.equal("notes", ref, inst.Notes, row.Notes)
	}

	// Completion: presence, the exact instant, and the civil date the tenant
	// reads it as (the date every classification uses).
	if inst.CompletedAt.IsZero() {
		c.equal("completedAt", ref, nil, row.CompletedAt)
	} else {
		if c.equal("completedAt", ref, inst.CompletedAt, row.CompletedAt) && row.CompletedAt != nil {
			c.equal("completedAt (tenant civil date)", ref,
				inst.CompletedOn, dateonly.FromTime(row.CompletedAt.In(r.tenant.zone)))
		}
	}
	if !inst.SubmittedOn.IsZero() {
		if row.SubmittedAt == nil {
			c.diff("submittedAt", ref, inst.SubmittedOn, nil)
		} else {
			c.equal("submittedAt (tenant civil date)", ref,
				inst.SubmittedOn, dateonly.FromTime(row.SubmittedAt.In(r.tenant.zone)))
		}
	}
	switch inst.Via {
	case "approve":
		if row.ApprovedBy == nil {
			c.diff("approvedBy", ref, "set (via=approve)", "null")
		}
		// The escape hatch back-dates approved_at to the completion instant.
		c.equal("approvedAt", ref, row.CompletedAt, row.ApprovedAt)
	case "submit":
		if row.SubmittedBy == nil {
			c.diff("submittedBy", ref, "set (via=submit)", "null")
		}
	}
}

// verifyOptional renders "" as a JSON null, which is what the API returns for
// an absent optional string.
func verifyOptional(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// checkPreviews diffs the planner itself: GET /workflows/{id}/preview against
// the oracle's plan, for every workflow — started or not. This is the check
// that fails when the date engine changes under the dataset.
func (r *verifyRun) checkPreviews() {
	for _, w := range r.tenant.workflows {
		c := r.check("workflow-preview", w.spec.Key)
		id, ok := r.ids.workflowID[w.spec.Key]
		if !ok {
			c.skip("workflow %s does not resolve to a live id", w.spec.Key)
			continue
		}
		var preview verifyPreview
		if err := r.client.api.GET(r.ctx, "/workflows/"+id+"/preview", nil, &preview); err != nil {
			c.errf("GET /workflows/%s/preview: %v", id, err)
			continue
		}
		expectedPeriods := len(w.spec.SelectedPeriods)
		if w.spec.IsProject() {
			expectedPeriods = 1
		}
		c.equal("totalPeriods", "", expectedPeriods, preview.TotalPeriods)
		c.equal("taskTemplates", "", len(w.spec.Templates), preview.TaskTemplates)
		c.equal("totalTasks", "", len(w.planned), preview.TotalTasks)

		planned := map[string]*oracleInstance{}
		for _, inst := range w.planned {
			planned[inst.PeriodCode+"/#"+strconv.Itoa(inst.OrderIndex)] = inst
		}
		seen := map[string]bool{}
		for _, task := range preview.Tasks {
			ref := task.PeriodCode + "/#" + strconv.Itoa(task.OrderIndex)
			inst, ok := planned[ref]
			if !ok {
				c.diff("task", ref, "no such planned task", task.Name)
				continue
			}
			seen[ref] = true
			c.equal("name", ref, inst.Name, task.Name)
			c.equal("taskType", ref, inst.TaskType, task.TaskType)
			c.equal("dueDate", ref, inst.DueDate, task.DueDate)
			c.equal("periodEndDate", ref, inst.PeriodEnd, task.PeriodEndDate)
			c.equal("filingDeadline", ref, inst.FilingDeadline, task.FilingDeadline)
			c.equal("paymentDeadline", ref, inst.PaymentDeadline, task.PaymentDeadline)
		}
		for ref := range planned {
			if !seen[ref] {
				c.diff("task", ref, "present in the preview", "missing")
			}
		}
	}
}

// checkPeriods diffs GET /entities/{id}/periods against the fiscal calendar
// for every (entity, periodicity, financial year) the dataset uses — the
// period list the UI offers and the generator consumes must be one list.
func (r *verifyRun) checkPeriods() {
	seen := map[verifyPeriodCombo]bool{}
	for _, w := range r.tenant.workflows {
		if w.spec.IsProject() || w.entity == nil || w.spec.Periodicity == nil {
			continue
		}
		seen[verifyPeriodCombo{w.entity.Key, *w.spec.Periodicity, w.spec.FinancialYear}] = true
	}
	combos := make([]verifyPeriodCombo, 0, len(seen))
	for c := range seen {
		combos = append(combos, c)
	}
	// Deterministic order: the report must read the same on every run.
	for i := 1; i < len(combos); i++ {
		for j := i; j > 0 && verifyComboLess(combos[j], combos[j-1]); j-- {
			combos[j], combos[j-1] = combos[j-1], combos[j]
		}
	}

	for _, cb := range combos {
		filter := fmt.Sprintf("%s %s %s", cb.entity, cb.periodicity, cb.year)
		c := r.check("entity-periods", filter)
		id, ok := r.ids.entityID[cb.entity]
		if !ok {
			c.skip("entity %s does not resolve to a live id", cb.entity)
			continue
		}
		entity, ok := r.tenant.spec.Entity(cb.entity)
		if !ok {
			c.skip("entity %s is not in the spec", cb.entity)
			continue
		}
		year, err := strconv.Atoi(cb.year)
		if err != nil {
			c.errf("financial year %q is not numeric", cb.year)
			continue
		}
		cal, err := oracleCalendarFor(entity, year)
		if err != nil {
			c.errf("calendar: %v", err)
			continue
		}
		want, err := cal.Periods(cb.periodicity)
		if err != nil {
			c.errf("periods: %v", err)
			continue
		}

		var got []struct {
			Code      string        `json:"code"`
			Label     string        `json:"label"`
			StartDate dateonly.Date `json:"startDate"`
			EndDate   dateonly.Date `json:"endDate"`
		}
		path := verifyQuery("/entities/"+id+"/periods", map[string]string{
			"periodicity":   cb.periodicity,
			"financialYear": cb.year,
		})
		if err := r.client.api.GET(r.ctx, path, nil, &got); err != nil {
			c.errf("GET %s: %v", path, err)
			continue
		}
		if !c.equal("periodCount", "", len(want), len(got)) {
			continue
		}
		for i, p := range want {
			c.equal("code", strconv.Itoa(i), p.Code, got[i].Code)
			c.equal("startDate", p.Code, p.Start, got[i].StartDate)
			c.equal("endDate", p.Code, p.End, got[i].EndDate)
		}
	}
}

// verifyPeriodCombo is one (entity, periodicity, financial year) the dataset
// asks the calendar for.
type verifyPeriodCombo struct {
	entity      string
	periodicity string
	year        string
}

func verifyComboLess(a, b verifyPeriodCombo) bool {
	if a.entity != b.entity {
		return a.entity < b.entity
	}
	if a.periodicity != b.periodicity {
		return a.periodicity < b.periodicity
	}
	return a.year < b.year
}

// checkWorkflowStats diffs GET /reports/workflow-stats: every workflow of the
// tenant, drafts and projects included, at zero when never started.
func (r *verifyRun) checkWorkflowStats() {
	c := r.check("workflow-stats", "")
	stats, err := r.workflowStats()
	if err != nil {
		c.errf("GET /reports/workflow-stats: %v", err)
		return
	}
	c.equal("workflowCount", "", len(r.tenant.workflows), len(stats))

	for _, w := range r.tenant.workflows {
		id, ok := r.ids.workflowID[w.spec.Key]
		if !ok {
			c.diff("workflow", w.spec.Key, "present", "does not resolve to a live id")
			continue
		}
		got, ok := stats[id]
		if !ok {
			c.diff("workflow", w.spec.Key, "present in workflow-stats", "missing")
			continue
		}
		want := w.stats()
		c.equal("totalTasks", w.spec.Key, want.TotalTasks, got.TotalTasks)
		c.equal("completedTasks", w.spec.Key, want.CompletedTasks, got.CompletedTasks)
		c.equal("completionPercent", w.spec.Key, want.CompletionPercent, got.CompletionPercent)
		c.equal("nextDueDate", w.spec.Key, want.NextDueDate, verifyDatePointer(got.NextDueDate))
	}
}

// verifyDatePointer renders an optional YYYY-MM-DD from the payload.
func verifyDatePointer(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	d, err := dateonly.Parse(*s)
	if err != nil {
		return *s
	}
	return d
}

// checkDashboard applies the client-side dashboard formulas (task-metrics.ts)
// to the live rows and diffs them against the oracle — the numbers a person
// sees on the landing page.
func (r *verifyRun) checkDashboard() {
	c := r.check("dashboard", "")
	want := r.tenant.dashboard()

	var (
		overdue, dueToday, thisWeek, active, completed, awaiting int
	)
	byStatus := map[string]int{}
	weekEnd := oracleEndOfWeek(r.tenant.today)
	for _, row := range r.instances {
		byStatus[row.Status]++
		if row.Status == oracleStatusPendingApproval {
			awaiting++
		}
		if row.Status == oracleStatusCompleted {
			completed++
			continue
		}
		active++
		switch {
		case oracleCompareDates(row.DueDate, r.tenant.today) < 0:
			overdue++
		case oracleCompareDates(row.DueDate, r.tenant.today) == 0:
			dueToday++
		case oracleCompareDates(row.DueDate, weekEnd) <= 0:
			thisWeek++
		}
	}

	c.equal("totalInstances", "", want.TotalInstances, len(r.instances))
	c.equal("overdue", "", want.Overdue, overdue)
	c.equal("dueToday", "", want.DueToday, dueToday)
	c.equal("thisWeek", "", want.ThisWeek, thisWeek)
	c.equal("active", "", want.Active, active)
	c.equal("completed", "", want.Completed, completed)
	c.equal("awaitingApproval", "", want.AwaitingApproval, awaiting)
	for status, n := range want.ByStatus {
		c.equal("byStatus", status, n, byStatus[status])
	}
	for status, n := range byStatus {
		if _, ok := want.ByStatus[status]; !ok {
			c.diff("byStatus", status, 0, n)
		}
	}

	// The completion rate comes from workflow-stats, exactly as the UI reads it.
	stats, err := r.workflowStats()
	if err != nil {
		c.errf("GET /reports/workflow-stats: %v", err)
		return
	}
	total, done := 0, 0
	for _, s := range stats {
		total += s.TotalTasks
		done += s.CompletedTasks
	}
	rate := 0
	if total > 0 {
		rate = int(math.Round(100 * float64(done) / float64(total)))
	}
	c.equal("completionRate", "", want.CompletionRate, rate)
}

// workflowStats reads (and caches) GET /reports/workflow-stats — two checks
// need it and it is one aggregate for the whole tenant.
func (r *verifyRun) workflowStats() (map[string]verifyWorkflowStat, error) {
	if r.stats != nil {
		return r.stats, nil
	}
	stats := map[string]verifyWorkflowStat{}
	if err := r.client.api.GET(r.ctx, "/reports/workflow-stats", nil, &stats); err != nil {
		return nil, err
	}
	r.stats = stats
	return stats, nil
}

// checkProjectParticipation proves product fix F6's other half: a project
// workflow's instances exist and are exported, but never reach the three
// compliance / financial reports.
func (r *verifyRun) checkProjectParticipation() {
	c := r.check("project-participation", "")
	projectIDs := map[string]bool{}
	for _, w := range r.tenant.workflows {
		if !w.spec.IsProject() {
			continue
		}
		id, ok := r.ids.workflowID[w.spec.Key]
		if !ok {
			continue
		}
		projectIDs[id] = true

		// Present in the instance list, with the PROJECT period and no
		// payment deadline.
		found := 0
		for _, row := range r.instances {
			if row.WorkflowID != id {
				continue
			}
			found++
			c.equal("periodCode", w.spec.Key, "PROJECT", row.PeriodCode)
			c.equal("paymentDeadline", w.spec.Key+"/"+row.Name, nil, row.PaymentDeadline)
		}
		c.equal("instanceCount", w.spec.Key, len(w.instances), found)
	}
	if len(projectIDs) == 0 {
		c.skip("the tenant has no project workflow")
		return
	}

	// Absent from compliance-status.
	var compliance verifyCompliancePayload
	if err := r.client.api.GET(r.ctx, verifyQuery("/reports/compliance-status",
		map[string]string{"limit": "5000"}), nil, &compliance); err != nil {
		c.errf("GET /reports/compliance-status: %v", err)
		return
	}
	for _, row := range compliance.Rows {
		if projectIDs[row.WorkflowID] {
			c.diff("compliance-status", r.ids.workflowKeyOf(row.WorkflowID)+"/"+row.Period,
				"excluded (project workflow)", "present")
		}
	}

	// Absent from the heatmap, which names the workflows feeding each cell.
	var heatmap verifyHeatmapPayload
	if err := r.client.api.GET(r.ctx, verifyQuery("/reports/compliance-heatmap",
		map[string]string{"viewMode": oracleViewPeriod}), nil, &heatmap); err != nil {
		c.errf("GET /reports/compliance-heatmap: %v", err)
		return
	}
	for _, cell := range heatmap.Cells {
		for _, id := range cell.WorkflowIDs {
			if projectIDs[id] {
				c.diff("compliance-heatmap", oracleHeatmapCellKey(r.ids.entityKeyOf(cell.RowID), cell.ColID),
					"no project workflow", r.ids.workflowKeyOf(id))
			}
		}
	}
}
