package pg

import (
	"strings"
	"testing"
)

// "Today" in the compliance classification is the TENANT's calendar date
// (ADR-0003 / ADR-0023 §6), never the session's or the server's.
func TestComplianceClass_TodayIsTheTenantsDay(t *testing.T) {
	for _, col := range []string{"ti.filing_deadline", "ti.due_date"} {
		sql := complianceClass(col)
		if strings.Contains(strings.ToUpper(sql), "CURRENT_DATE") {
			t.Fatalf("%s: CURRENT_DATE (server day) must not appear:\n%s", col, sql)
		}
		if !strings.Contains(sql, col+" < "+tenantToday) {
			t.Fatalf("%s: the missed rule must compare against tenantToday:\n%s", col, sql)
		}
	}
}

func TestTenantToday_ReadsTheRegistryRowOfTheRequestTenant(t *testing.T) {
	for _, want := range []string{
		"NOW() AT TIME ZONE",
		"FROM tenants t",
		"current_setting('app.tenant_id', true)::uuid",
		"::date",
	} {
		if !strings.Contains(tenantToday, want) {
			t.Fatalf("tenantToday lacks %q:\n%s", want, tenantToday)
		}
	}
	// No bind parameter and no interpolation slot: the zone comes from the
	// registry row only.
	if strings.Contains(tenantToday, "$") || strings.Contains(tenantToday, "%") {
		t.Fatalf("tenantToday must not take caller data:\n%s", tenantToday)
	}
}

// The due-window tiles (overdue / due today / due this week) are evaluated
// against the tenant's civil day too — the same tenantToday the classification
// uses, never the server's CURRENT_DATE — and they only ever count open work.
func TestDueWindows_UseTheTenantsDayAndOpenWorkOnly(t *testing.T) {
	for name, sql := range map[string]string{
		"dueOverdue": dueOverdue, "dueToday": dueToday, "dueThisWeek": dueThisWeek, "tenantWeekEnd": tenantWeekEnd,
	} {
		if strings.Contains(strings.ToUpper(sql), "CURRENT_DATE") {
			t.Fatalf("%s: CURRENT_DATE (server day) must not appear:\n%s", name, sql)
		}
		if !strings.Contains(sql, tenantToday) {
			t.Fatalf("%s must be evaluated against tenantToday:\n%s", name, sql)
		}
		if strings.Contains(sql, "$") || strings.Contains(sql, "%") {
			t.Fatalf("%s must not take caller data:\n%s", name, sql)
		}
	}
	for name, sql := range map[string]string{"dueOverdue": dueOverdue, "dueToday": dueToday, "dueThisWeek": dueThisWeek} {
		if !strings.HasPrefix(sql, taskOpen+" AND ") {
			t.Fatalf("%s must be guarded by the open-work predicate (a completed instance is never due):\n%s", name, sql)
		}
	}
	if !strings.Contains(dueOverdue, "ti.due_date < "+tenantToday) {
		t.Fatalf("overdue is strictly before today:\n%s", dueOverdue)
	}
	if !strings.Contains(dueToday, "ti.due_date = "+tenantToday) {
		t.Fatalf("due today is equality with today:\n%s", dueToday)
	}
	if !strings.Contains(dueThisWeek, "ti.due_date > "+tenantToday) || !strings.Contains(dueThisWeek, "ti.due_date <= "+tenantWeekEnd) {
		t.Fatalf("due this week is (today, week end]:\n%s", dueThisWeek)
	}
}

// The week ends on SATURDAY (Sunday-first weeks, DOW 0–6), matching the UI's
// task-metrics.ts: today + (6 - DOW).
func TestTenantWeekEnd_EndsOnSaturday(t *testing.T) {
	if !strings.Contains(tenantWeekEnd, "6 - EXTRACT(DOW FROM "+tenantToday+")") {
		t.Fatalf("week end must be today + (6 - DOW):\n%s", tenantWeekEnd)
	}
	if !strings.HasPrefix(tenantWeekEnd, "("+tenantToday+" + ") {
		t.Fatalf("week end is an offset from tenantToday:\n%s", tenantWeekEnd)
	}
}

// statusRank orders the six stored statuses as the task board reads them.
func TestStatusRank_OrdersTheSixStatuses(t *testing.T) {
	order := []string{"not_started", "in_progress", "in_review", "pending_approval", "completed", "blocked"}
	last := -1
	for rank, status := range order {
		idx := strings.Index(statusRank, "WHEN '"+status+"' THEN "+string(rune('0'+rank)))
		if idx < 0 {
			t.Fatalf("statusRank lacks %s at rank %d:\n%s", status, rank, statusRank)
		}
		if idx < last {
			t.Fatalf("statusRank lists %s out of order:\n%s", status, statusRank)
		}
		last = idx
	}
	if !strings.Contains(statusRank, "ELSE 6") {
		t.Fatalf("an unknown status must sort last:\n%s", statusRank)
	}
}
