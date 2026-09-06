package main

// Unit tests for the oracle's rules: the four dates it plans per instance and
// the classification that turns a deadline plus a completion into on_time /
// late / missed / not_due.
//
// These are the tests that keep the oracle honest. Everything above them
// (verify's diffing) can only compare; only here is it decided what the right
// answer IS.

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func oracleTestDate(t *testing.T, value string) dateonly.Date {
	t.Helper()
	d, err := dateonly.Parse(value)
	require.NoError(t, err)
	return d
}

func oracleTestString(v string) *string { return &v }

// oracleTestSpec is a minimal, hand-built dataset: one tenant, two entities
// (a calendar year and a 31 March fiscal year), a VAT obligation with a
// payment offset, a CIT obligation with a fixed payment date.
func oracleTestSpec(timezone string, workflows ...spec.Workflow) *spec.Spec {
	tenant := spec.Tenant{
		Key: "t", Slug: "t", Name: "Test", Timezone: timezone,
		Admin: spec.User{Key: "t.admin", Email: "admin@t.test", Name: "Admin",
			Role: "tenant_admin", Password: "password1234", Status: "active"},
		Users: []spec.User{
			{Key: "t.prep", Email: "prep@t.test", Name: "Prep", Role: "preparer",
				Password: "password1234", Status: "active"},
		},
		Entities: []spec.Entity{
			{Key: "e.de", Name: "DE GmbH", Country: "Germany",
				FiscalCalendarPattern: "standard", FinancialYearEnd: "12-31"},
			{Key: "e.uk", Name: "UK Ltd", Country: "United Kingdom",
				FiscalCalendarPattern: "standard", FinancialYearEnd: "03-31"},
		},
		ObligationTypes: []spec.ObligationType{
			{Key: "o.vat", Name: "Value Added Tax", Code: "VAT", Template: "VAT", Category: "predefined"},
			{Key: "o.cit", Name: "Corporate Income Tax", Code: "CIT", Template: "CIT", Category: "predefined"},
		},
		EntityObligations: []spec.EntityObligation{
			{
				Key: "eo.de-vat", Entity: "e.de", ObligationType: "o.vat", Periodicity: "monthly",
				DeadlineRule: spec.DeadlineRule{
					Type: "period_offset", Reference: "period_end", WeekendAdjustment: "next-business-day",
					PaymentOffset: &spec.Offset{Months: 1, Days: 10},
				},
			},
			{
				Key: "eo.de-cit", Entity: "e.de", ObligationType: "o.cit", Periodicity: "annual",
				DeadlineRule: spec.DeadlineRule{
					Type: "fixed", Reference: "period_end", WeekendAdjustment: "none",
					PaymentFixedDates: []string{"09-30"},
				},
			},
		},
		Workflows: workflows,
	}
	return &spec.Spec{
		AsOf:        dateonly.New(2026, 9, 6),
		Conventions: spec.Conventions{CompletionLocalTime: "10:00"},
		Tenants:     []spec.Tenant{tenant},
	}
}

// oracleTestWorkflow is a recurring workflow with the five standard templates
// referencing all three anchors.
func oracleTestWorkflow(periods ...string) spec.Workflow {
	return spec.Workflow{
		Key: "w1", Name: "DE VAT 2026", Category: spec.CategoryRecurring,
		Entity: oracleTestString("e.de"), ObligationType: oracleTestString("o.vat"),
		EntityObligation: oracleTestString("eo.de-vat"),
		FinancialYear:    "2026", Periodicity: oracleTestString("monthly"),
		SelectedPeriods: periods,
		DueDateRule: spec.DueDateRule{
			Reference: "period_end", OffsetUnit: "days", OffsetValue: 15,
			OffsetDirection: "after", WeekendAdjustment: "next-business-day",
		},
		StartDate: dateonly.New(2026, 1, 1), EndDate: dateonly.New(2026, 12, 31),
		Writer: "t.prep", Approver: "t.admin",
		Lifecycle: spec.Lifecycle{Start: true, FinalStatus: "active"},
		Templates: []spec.TaskTemplate{
			{Key: "collect", Name: "Collect data", TaskType: "data_request", OrderIndex: 0,
				DueDateReference: "period_end", DueDateOffsetValue: 2,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "after"},
			{Key: "prepare", Name: "Prepare return", TaskType: "preparation", OrderIndex: 1,
				DueDateReference: "filing_deadline", DueDateOffsetValue: 5,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "before"},
			{Key: "pay", Name: "Pay", TaskType: "payment", OrderIndex: 2,
				DueDateReference: "payment_deadline", DueDateOffsetValue: 0,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "before"},
		},
	}
}

// oracleTestBuild recomputes a one-tenant world at a fixed civil today.
func oracleTestBuild(t *testing.T, s *spec.Spec, today string) *oracleTenant {
	t.Helper()
	world, err := buildOracleWorld(s, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), oracleTestDate(t, today))
	require.NoError(t, err)
	require.Len(t, world.tenants, 1)
	return world.tenants[0]
}

// oracleTestInstance finds one planned instance.
func oracleTestInstance(t *testing.T, tenant *oracleTenant, workflowKey, period, template string) *oracleInstance {
	t.Helper()
	w, ok := tenant.byWfKey[workflowKey]
	require.True(t, ok, "workflow %s", workflowKey)
	for _, inst := range w.planned {
		if inst.PeriodCode == period && inst.TemplateKey == template {
			return inst
		}
	}
	t.Fatalf("no instance %s/%s/%s", workflowKey, period, template)
	return nil
}

// ---------------------------------------------------------------------------
// The four dates
// ---------------------------------------------------------------------------

func TestOraclePlansTheFourDates(t *testing.T) {
	t.Parallel()
	tenant := oracleTestBuild(t, oracleTestSpec("Europe/Berlin", oracleTestWorkflow("M1", "M2")), "2026-09-06")

	tests := []struct {
		name            string
		period          string
		template        string
		periodEnd       string
		filingDeadline  string
		paymentDeadline string
		dueDate         string
	}{
		{
			// 31 Jan + 15 days = 15 Feb, a Sunday → the next business day.
			// Payment: 31 Jan + 1 month clamps to 28 Feb, + 10 days = 10 Mar.
			name: "period end anchor", period: "M1", template: "collect",
			periodEnd: "2026-01-31", filingDeadline: "2026-02-16",
			paymentDeadline: "2026-03-10", dueDate: "2026-02-02",
		},
		{
			// The filing anchor is the ADJUSTED deadline: 16 Feb − 5 days.
			name: "filing anchor", period: "M1", template: "prepare",
			periodEnd: "2026-01-31", filingDeadline: "2026-02-16",
			paymentDeadline: "2026-03-10", dueDate: "2026-02-11",
		},
		{
			name: "payment anchor", period: "M1", template: "pay",
			periodEnd: "2026-01-31", filingDeadline: "2026-02-16",
			paymentDeadline: "2026-03-10", dueDate: "2026-03-10",
		},
		{
			// 28 Feb + 15 days = 15 Mar, a Sunday → 16 Mar.
			// Payment: 28 Feb + 1 month = 28 Mar, + 10 days = 7 Apr.
			name: "second period", period: "M2", template: "collect",
			periodEnd: "2026-02-28", filingDeadline: "2026-03-16",
			paymentDeadline: "2026-04-07", dueDate: "2026-03-02",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := oracleTestInstance(t, tenant, "w1", tc.period, tc.template)
			assert.Equal(t, tc.periodEnd, inst.PeriodEnd.String(), "period end")
			assert.Equal(t, tc.filingDeadline, inst.FilingDeadline.String(), "filing deadline")
			require.NotNil(t, inst.PaymentDeadline)
			assert.Equal(t, tc.paymentDeadline, inst.PaymentDeadline.String(), "payment deadline")
			assert.Equal(t, tc.dueDate, inst.DueDate.String(), "due date")
		})
	}
}

func TestOracleWeekendAdjustment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		periodEnd  string // the period whose end + 15 days lands where we want
		offset     int
		adjustment string
		want       string
	}{
		// 31 Jan 2026 is a Saturday.
		{name: "saturday moves to monday", periodEnd: "2026-01-31", offset: 0, adjustment: "next-business-day", want: "2026-02-02"},
		{name: "saturday untouched without a rule", periodEnd: "2026-01-31", offset: 0, adjustment: "none", want: "2026-01-31"},
		// 31 May 2026 is a Sunday.
		{name: "sunday moves to monday", periodEnd: "2026-05-31", offset: 0, adjustment: "next-business-day", want: "2026-06-01"},
		// 30 Apr 2026 is a Thursday.
		{name: "weekday untouched", periodEnd: "2026-04-30", offset: 0, adjustment: "next-business-day", want: "2026-04-30"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workflow := oracleTestWorkflow("M1")
			workflow.DueDateRule.OffsetValue = tc.offset
			workflow.DueDateRule.WeekendAdjustment = tc.adjustment
			// Point the workflow at the period whose end we want to test.
			switch tc.periodEnd {
			case "2026-05-31":
				workflow.SelectedPeriods = []string{"M5"}
			case "2026-04-30":
				workflow.SelectedPeriods = []string{"M4"}
			}
			tenant := oracleTestBuild(t, oracleTestSpec("Europe/Berlin", workflow), "2026-09-06")
			inst := oracleTestInstance(t, tenant, "w1", workflow.SelectedPeriods[0], "collect")
			assert.Equal(t, tc.periodEnd, inst.PeriodEnd.String())
			assert.Equal(t, tc.want, inst.FilingDeadline.String())
		})
	}
}

func TestOraclePaymentFixedDatesAreNotWeekendAdjusted(t *testing.T) {
	t.Parallel()
	// A CIT year ending 31 Dec 2025 with a fixed payment date of 30 August:
	// the first 08-30 on or after the period end is 30 Aug 2026, a SUNDAY —
	// and a fixed payment date is never moved off a weekend.
	s := oracleTestSpec("Europe/Berlin", spec.Workflow{
		Key: "cit", Name: "DE CIT 2025", Category: spec.CategoryRecurring,
		Entity: oracleTestString("e.de"), ObligationType: oracleTestString("o.cit"),
		EntityObligation: oracleTestString("eo.de-cit"),
		FinancialYear:    "2025", Periodicity: oracleTestString("annual"),
		SelectedPeriods: []string{"Y1"},
		DueDateRule: spec.DueDateRule{
			Reference: "period_end", OffsetUnit: "months", OffsetValue: 7,
			OffsetDirection: "after", WeekendAdjustment: "none",
		},
		StartDate: dateonly.New(2025, 1, 1), EndDate: dateonly.New(2025, 12, 31),
		Writer: "t.prep", Approver: "t.admin",
		Lifecycle: spec.Lifecycle{Start: true, FinalStatus: "active"},
		Templates: []spec.TaskTemplate{
			{Key: "pay", Name: "Pay", TaskType: "payment", OrderIndex: 0,
				DueDateReference: "payment_deadline", DueDateOffsetValue: 0,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "before"},
		},
	})
	s.Tenants[0].EntityObligations[1].DeadlineRule.PaymentFixedDates = []string{"08-30"}

	tenant := oracleTestBuild(t, s, "2026-09-06")
	inst := oracleTestInstance(t, tenant, "cit", "Y1", "pay")
	assert.Equal(t, "2025-12-31", inst.PeriodEnd.String())
	assert.Equal(t, "2026-07-31", inst.FilingDeadline.String(), "31 Dec + 7 months")
	require.NotNil(t, inst.PaymentDeadline)
	assert.Equal(t, "2026-08-30", inst.PaymentDeadline.String(), "a fixed payment date stays on its Sunday")
	assert.Equal(t, "2026-08-30", inst.DueDate.String())
}

func TestOracleUKFiscalYearMapping(t *testing.T) {
	t.Parallel()
	// A 31 March fiscal year: FY2027 runs 1 Apr 2026 – 31 Mar 2027, so Q1 ends
	// 30 Jun 2026 and Q4 ends 31 Mar 2027 (test plan AUTO-15).
	workflow := oracleTestWorkflow("Q1", "Q4")
	workflow.Key, workflow.Name = "uk", "UK VAT 2027"
	workflow.Entity = oracleTestString("e.uk")
	workflow.EntityObligation = nil
	workflow.FinancialYear = "2027"
	workflow.Periodicity = oracleTestString("quarterly")
	workflow.DueDateRule.WeekendAdjustment = "none"
	workflow.DueDateRule.OffsetValue = 0

	tenant := oracleTestBuild(t, oracleTestSpec("Europe/London", workflow), "2026-09-06")
	assert.Equal(t, "2026-06-30", oracleTestInstance(t, tenant, "uk", "Q1", "collect").PeriodEnd.String())
	assert.Equal(t, "2027-03-31", oracleTestInstance(t, tenant, "uk", "Q4", "collect").PeriodEnd.String())

	// No entity obligation for (e.uk, o.vat): the payment deadline is the
	// filing deadline, which is the period end here.
	q1 := oracleTestInstance(t, tenant, "uk", "Q1", "pay")
	require.NotNil(t, q1.PaymentDeadline)
	assert.Equal(t, "2026-06-30", q1.PaymentDeadline.String())
}

func TestOracleMonthEndClamping(t *testing.T) {
	t.Parallel()
	// 31 Jan + 1 month must clamp to 28 Feb (2026 is not a leap year) before
	// the days are added.
	tests := []struct {
		name    string
		period  string
		months  int
		days    int
		want    string
		periods []string
	}{
		{name: "31 Jan + 1 month + 0 days", period: "M1", months: 1, days: 0, want: "2026-02-28", periods: []string{"M1"}},
		{name: "31 Jan + 1 month + 10 days", period: "M1", months: 1, days: 10, want: "2026-03-10", periods: []string{"M1"}},
		{name: "31 Mar + 1 month", period: "M3", months: 1, days: 0, want: "2026-04-30", periods: []string{"M3"}},
		{name: "31 Dec + 2 months", period: "M12", months: 2, days: 0, want: "2027-02-28", periods: []string{"M12"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workflow := oracleTestWorkflow(tc.periods...)
			workflow.DueDateRule.WeekendAdjustment = "none"
			s := oracleTestSpec("Europe/Berlin", workflow)
			s.Tenants[0].EntityObligations[0].DeadlineRule.PaymentOffset =
				&spec.Offset{Months: tc.months, Days: tc.days}
			s.Tenants[0].EntityObligations[0].DeadlineRule.WeekendAdjustment = "none"

			tenant := oracleTestBuild(t, s, "2026-09-06")
			inst := oracleTestInstance(t, tenant, "w1", tc.period, "pay")
			require.NotNil(t, inst.PaymentDeadline)
			assert.Equal(t, tc.want, inst.PaymentDeadline.String())
		})
	}
}

func TestOracleProjectWorkflowPlan(t *testing.T) {
	t.Parallel()
	// Product fix F6: one instance per template under the PROJECT period, the
	// end date as period end AND filing deadline, no payment deadline, and
	// every template anchor resolving to that end date.
	project := spec.Workflow{
		Key: "p1", Name: "Dispute", Category: spec.CategoryProject,
		ProjectType: oracleTestString("dispute"), Entity: oracleTestString("e.de"),
		FinancialYear: "2026",
		DueDateRule: spec.DueDateRule{
			Reference: "period_end", OffsetUnit: "days", OffsetValue: 15,
			OffsetDirection: "after", WeekendAdjustment: "next-business-day",
		},
		StartDate: dateonly.New(2026, 1, 15), EndDate: dateonly.New(2026, 6, 30),
		Writer: "t.prep", Approver: "t.admin",
		Lifecycle: spec.Lifecycle{Start: true, FinalStatus: "active"},
		Templates: []spec.TaskTemplate{
			{Key: "file", Name: "File the appeal", TaskType: "submission", OrderIndex: 0,
				DueDateReference: "period_end", DueDateOffsetValue: 30,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "before"},
			{Key: "pay", Name: "Settle", TaskType: "payment", OrderIndex: 1,
				DueDateReference: "payment_deadline", DueDateOffsetValue: 0,
				DueDateOffsetUnit: "days", DueDateOffsetDirection: "after"},
		},
	}
	tenant := oracleTestBuild(t, oracleTestSpec("Europe/Berlin", project), "2026-09-06")
	require.Len(t, tenant.instances, 2)

	for _, inst := range tenant.instances {
		assert.Equal(t, spec.ProjectPeriodCode, inst.PeriodCode)
		assert.Equal(t, "2026-06-30", inst.PeriodEnd.String())
		assert.Equal(t, "2026-06-30", inst.FilingDeadline.String(),
			"a project's filing deadline is its end date — the workflow due-date rule plays no part")
		assert.Nil(t, inst.PaymentDeadline, "project workflows have no payment deadline")
	}
	assert.Equal(t, "2026-05-31", oracleTestInstance(t, tenant, "p1", "PROJECT", "file").DueDate.String())
	assert.Equal(t, "2026-06-30", oracleTestInstance(t, tenant, "p1", "PROJECT", "pay").DueDate.String(),
		"a payment_deadline reference falls back to the end date")
}

func TestOracleParticipationRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		category string
		status   string
		want     bool
	}{
		{name: "active recurring", category: spec.CategoryRecurring, status: "active", want: true},
		{name: "completed recurring", category: spec.CategoryRecurring, status: "completed", want: true},
		{name: "archived recurring", category: spec.CategoryRecurring, status: "archived", want: false},
		{name: "draft recurring", category: spec.CategoryRecurring, status: "draft", want: false},
		{name: "active project", category: spec.CategoryProject, status: "active", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workflow := oracleTestWorkflow("M1")
			workflow.Lifecycle.FinalStatus = tc.status
			if tc.category == spec.CategoryProject {
				workflow.Category = spec.CategoryProject
				workflow.ProjectType = oracleTestString("dispute")
				workflow.ObligationType, workflow.EntityObligation, workflow.Periodicity = nil, nil, nil
				workflow.SelectedPeriods = nil
			}
			tenant := oracleTestBuild(t, oracleTestSpec("Europe/Berlin", workflow), "2026-09-06")
			assert.Equal(t, tc.want, tenant.byWfKey["w1"].participates)
			assert.Equal(t, tc.want, len(tenant.participating(oracleFilters{})) > 0)
			assert.NotEmpty(t, tenant.instances, "instances exist whatever the workflow status")
		})
	}
}

func TestOracleDraftWorkflowIsPlannedButNotMaterialized(t *testing.T) {
	t.Parallel()
	workflow := oracleTestWorkflow("M1")
	workflow.Lifecycle = spec.Lifecycle{Start: false, FinalStatus: "draft"}
	tenant := oracleTestBuild(t, oracleTestSpec("Europe/Berlin", workflow), "2026-09-06")

	assert.Empty(t, tenant.instances, "an unstarted workflow has no instances")
	assert.Len(t, tenant.byWfKey["w1"].planned, 3, "but preview still plans them")
	assert.Equal(t, 0, tenant.byWfKey["w1"].stats().TotalTasks)
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

func TestOracleClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		deadline    string
		today       string
		completedOn string // "" = not completed
		want        string
	}{
		{name: "deadline in the future", deadline: "2026-09-30", today: "2026-09-06", want: oracleNotDue},
		{name: "deadline is today", deadline: "2026-09-06", today: "2026-09-06", want: oracleNotDue},
		{name: "deadline was yesterday", deadline: "2026-09-05", today: "2026-09-06", want: oracleMissed},
		{name: "completed before the deadline", deadline: "2026-09-30", today: "2026-09-06",
			completedOn: "2026-09-01", want: oracleOnTime},
		{name: "completed ON the deadline", deadline: "2026-09-01", today: "2026-09-06",
			completedOn: "2026-09-01", want: oracleOnTime},
		{name: "completed the day after", deadline: "2026-09-01", today: "2026-09-06",
			completedOn: "2026-09-02", want: oracleLate},
		{name: "completed late, deadline still ahead", deadline: "2026-09-30", today: "2026-09-06",
			completedOn: "2026-10-01", want: oracleLate},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tenant := oracleTestBuild(t, oracleTestSpec("Europe/Berlin", oracleTestWorkflow("M1")), tc.today)
			inst := tenant.instances[0]
			if tc.completedOn != "" {
				inst.Status = oracleStatusCompleted
				inst.CompletedOn = oracleTestDate(t, tc.completedOn)
				completedAt, ok := spec.Instance{CompletedOn: inst.CompletedOn}.
					CompletionInstant(tenant.zone, "10:00")
				require.True(t, ok)
				inst.CompletedAt = completedAt
			}
			assert.Equal(t, tc.want, inst.classify(oracleTestDate(t, tc.deadline)))
		})
	}
}

func TestOracleCompletionIsClassifiedInTheTenantZone(t *testing.T) {
	t.Parallel()
	// One instant, three tenants: 2026-04-16T03:30Z is still 15 April in New
	// York, already 16 April in Berlin and 16 April in Tokyo. Against a
	// deadline of 15 April that is on_time for the American tenant and late
	// for the other two — the whole point of classifying in the tenant's zone.
	instant := time.Date(2026, 4, 16, 3, 30, 0, 0, time.UTC)
	tests := []struct {
		zone      string
		civilDate string
		want      string
	}{
		{zone: "America/New_York", civilDate: "2026-04-15", want: oracleOnTime},
		{zone: "Europe/Berlin", civilDate: "2026-04-16", want: oracleLate},
		{zone: "Asia/Tokyo", civilDate: "2026-04-16", want: oracleLate},
	}
	for _, tc := range tests {
		t.Run(tc.zone, func(t *testing.T) {
			tenant := oracleTestBuild(t, oracleTestSpec(tc.zone, oracleTestWorkflow("M1")), "2026-09-06")
			inst := tenant.instances[0]
			inst.Status = oracleStatusCompleted
			inst.CompletedAt = instant
			assert.Equal(t, tc.civilDate, inst.completedCivilDate().String())
			assert.Equal(t, tc.want, inst.classify(dateonly.New(2026, 4, 15)))
		})
	}
}

func TestOracleCompletionInstantIsTheTenantZoneLocalTime(t *testing.T) {
	t.Parallel()
	// The dataset's own stamping rule, applied by the oracle when it overlays
	// a deviation: completedOn at the local completion time, read in the
	// tenant's zone. Tokyo is UTC+9, so 10:00 local on 29 May is 01:00Z.
	workflow := oracleTestWorkflow("M1")
	workflow.Instances = []spec.Instance{{
		Period: "M1", Task: "collect", Status: oracleStatusCompleted, Via: "put",
		Assignee: "t.prep", CompletedOn: dateonly.New(2026, 5, 29),
	}}
	tenant := oracleTestBuild(t, oracleTestSpec("Asia/Tokyo", workflow), "2026-09-06")
	inst := oracleTestInstance(t, tenant, "w1", "M1", "collect")
	assert.Equal(t, "2026-05-29T01:00:00Z", inst.CompletedAt.UTC().Format(time.RFC3339))
	assert.Equal(t, "2026-05-29", inst.completedCivilDate().String())
	assert.Equal(t, "Prep", inst.AssigneeName)
}

func TestOracleRejectsAContradictoryPinnedInstant(t *testing.T) {
	t.Parallel()
	// completedAtUtc is the dataset pinning the exact instant. When it does
	// not match completedOn at the local time in the tenant's zone, the
	// dataset contradicts itself and the oracle refuses to guess.
	workflow := oracleTestWorkflow("M1")
	workflow.Instances = []spec.Instance{{
		Period: "M1", Task: "collect", Status: oracleStatusCompleted, Via: "put",
		Assignee: "t.prep", CompletedOn: dateonly.New(2026, 5, 29),
		CompletedAtUtc: time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC), // the naive reading
	}}
	_, err := buildOracleWorld(oracleTestSpec("Asia/Tokyo", workflow), time.Now(), dateonly.New(2026, 9, 6))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "completedAtUtc")
}

func TestOracleEndOfWeekIsSaturday(t *testing.T) {
	t.Parallel()
	tests := []struct{ today, weekEnd string }{
		{today: "2026-09-06", weekEnd: "2026-09-12"}, // a Sunday: the week ends six days later
		{today: "2026-09-07", weekEnd: "2026-09-12"}, // Monday
		{today: "2026-09-12", weekEnd: "2026-09-12"}, // the Saturday itself
	}
	for _, tc := range tests {
		t.Run(tc.today, func(t *testing.T) {
			assert.Equal(t, tc.weekEnd, oracleEndOfWeek(oracleTestDate(t, tc.today)).String())
		})
	}
}

func TestOracleCompareDates(t *testing.T) {
	t.Parallel()
	assert.Equal(t, -1, oracleCompareDates(dateonly.New(2026, 1, 31), dateonly.New(2026, 2, 1)))
	assert.Equal(t, 0, oracleCompareDates(dateonly.New(2026, 2, 1), dateonly.New(2026, 2, 1)))
	assert.Equal(t, 1, oracleCompareDates(dateonly.New(2027, 1, 1), dateonly.New(2026, 12, 31)))
	assert.Equal(t, -1, oracleCompareDates(dateonly.New(2025, 12, 31), dateonly.New(2026, 1, 1)))
}

// ---------------------------------------------------------------------------
// The real dataset
// ---------------------------------------------------------------------------

// TestOracleRecomputesTheRealDataset is the guard on the dataset itself: it
// must load, validate and recompute without the oracle refusing anything (an
// unknown period code, a calendar it cannot build, a contradictory instant).
func TestOracleRecomputesTheRealDataset(t *testing.T) {
	t.Parallel()
	loaded := oracleTestLoadDataset(t)
	world, err := buildOracleWorld(loaded, time.Now(), loaded.AsOf)
	require.NoError(t, err)
	require.NotEmpty(t, world.tenants)

	for _, tenant := range world.tenants {
		assert.Equal(t, loaded.AsOf, tenant.today)
		for _, w := range tenant.workflows {
			if !w.started {
				assert.Empty(t, w.instances, "%s is not started", w.spec.Key)
				continue
			}
			assert.Len(t, w.instances, len(w.spec.PeriodCodes())*len(w.spec.Templates), w.spec.Key)
			for _, inst := range w.instances {
				assert.False(t, inst.DueDate.IsZero(), "%s has no due date", inst.ref())
				assert.False(t, inst.FilingDeadline.IsZero(), "%s has no filing deadline", inst.ref())
				if w.spec.IsProject() {
					assert.Nil(t, inst.PaymentDeadline, "%s is a project instance", inst.ref())
				} else {
					assert.NotNil(t, inst.PaymentDeadline, "%s has no payment deadline", inst.ref())
				}
				if inst.isCompleted() {
					assert.False(t, inst.CompletedAt.IsZero(), "%s is completed without an instant", inst.ref())
					assert.Equal(t, inst.CompletedOn, inst.completedCivilDate(),
						"%s: the stored instant must read as completedOn in %s", inst.ref(), tenant.spec.Timezone)
				}
			}
		}
	}
}

// oracleTestLoadDataset loads the repository's demo dataset, skipping when it
// is not on disk (the package must still test in a trimmed checkout).
func oracleTestLoadDataset(t *testing.T) *spec.Spec {
	t.Helper()
	loaded, err := spec.LoadAndValidate("../../seed/demo/dataset.json")
	if err != nil {
		t.Skipf("the demo dataset is not loadable here: %v", err)
	}
	return loaded
}

// oracleTestNumbers builds a tax_data object the way spec.Load decodes one
// (json.Number values, so a literal keeps its exact text).
func oracleTestNumbers(t *testing.T, pairs map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(pairs)
	require.NoError(t, err)
	decoded := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&decoded))
	return decoded
}
