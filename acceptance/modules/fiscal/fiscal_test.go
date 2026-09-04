package fiscal_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// FiscalSuite drives ADR-0023 end to end: every fiscal calendar pattern
// through the generator (POST /workflows/{id}/start) and the periods endpoint
// (GET /entities/{id}/periods), plus the payment deadline derived from the
// entity obligation.
type FiscalSuite struct {
	acceptance.Suite
}

func TestFiscalSuite(t *testing.T) {
	suite.Run(t, new(FiscalSuite))
}

type idOnly struct {
	ID string `json:"id"`
}

type periodRow struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type instanceRow struct {
	ID              string  `json:"id"`
	WorkflowTaskID  string  `json:"workflowTaskId"`
	PeriodCode      string  `json:"periodCode"`
	Name            string  `json:"name"`
	DueDate         string  `json:"dueDate"`
	PeriodEndDate   string  `json:"periodEndDate"`
	FilingDeadline  string  `json:"filingDeadline"`
	PaymentDeadline *string `json:"paymentDeadline"`
}

type previewRow struct {
	TemplateID      string `json:"templateId"`
	PeriodCode      string `json:"periodCode"`
	Name            string `json:"name"`
	DueDate         string `json:"dueDate"`
	PeriodEndDate   string `json:"periodEndDate"`
	FilingDeadline  string `json:"filingDeadline"`
	PaymentDeadline string `json:"paymentDeadline"`
}

func (s *FiscalSuite) createEntity(tenant string, body map[string]any) string {
	var e idOnly
	r := s.As(tenant).POST(s.T(), "/entities", body)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &e)
	return e.ID
}

func (s *FiscalSuite) createObligationType(tenant, code string) string {
	var ot idOnly
	r := s.As(tenant).POST(s.T(), "/obligation-types", map[string]any{"name": "VAT " + code, "code": code, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &ot)
	return ot.ID
}

// createWorkflow builds a recurring workflow whose filing deadline is period
// end + 15 days (no weekend adjustment).
func (s *FiscalSuite) createWorkflow(tenant, entityID, obligationTypeID, periodicity, financialYear string, periods []string) string {
	var wf idOnly
	r := s.As(tenant).POST(s.T(), "/workflows", map[string]any{
		"name": "WF " + periodicity + " " + financialYear, "workflowCategory": "recurring",
		"entityId": entityID, "obligationTypeId": obligationTypeID,
		"periodicity": periodicity, "financialYear": financialYear, "selectedPeriods": periods,
		"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)
	return wf.ID
}

func (s *FiscalSuite) addTask(tenant, workflowID, name, taskType, reference string, offsetDays int, orderIndex int) string {
	var t idOnly
	r := s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": workflowID, "name": name, "taskType": taskType,
		"dueDateReference": reference, "dueDateOffsetValue": offsetDays,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": orderIndex,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &t)
	return t.ID
}

func (s *FiscalSuite) start(tenant, workflowID string, body any) *acceptance.TestResponse {
	return s.As(tenant).POST(s.T(), "/workflows/"+workflowID+"/start", body)
}

func (s *FiscalSuite) instances(tenant, workflowID string) []instanceRow {
	var rows []instanceRow
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+workflowID+"&limit=100").DecodeData(s.T(), &rows)
	return rows
}

func (s *FiscalSuite) periods(tenant, entityID, periodicity string, financialYear int) *acceptance.TestResponse {
	return s.As(tenant).GET(s.T(), fmt.Sprintf("/entities/%s/periods?periodicity=%s&financialYear=%d", entityID, periodicity, financialYear))
}

func (s *FiscalSuite) periodRows(tenant, entityID, periodicity string, financialYear int) []periodRow {
	var rows []periodRow
	r := s.periods(tenant, entityID, periodicity, financialYear)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &rows)
	return rows
}

func codes(n int, prefix string) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("%s%d", prefix, i))
	}
	return out
}

// byPeriod indexes instances by period code (one template per workflow).
func byPeriod(rows []instanceRow) map[string]instanceRow {
	out := make(map[string]instanceRow, len(rows))
	for _, r := range rows {
		out[r.PeriodCode] = r
	}
	return out
}

// Test445MonthlyMatchesTheEngine: a 4-4-5 retailer (Saturday nearest 31 Jan)
// in fiscal 2024 (= NRF 2023, 29 Jan 2023 – 3 Feb 2024, 53 weeks) → a monthly
// workflow over M1..M12 generates 12 instances whose period ends are the
// engine's hand-verified table, and GET /entities/{id}/periods returns the
// same rows (plus W53 for the weekly view).
func (s *FiscalSuite) Test445MonthlyMatchesTheEngine() {
	tenant := s.InsertTenant("fiscal-445", "445 Tenant").String()
	entity := s.createEntity(tenant, map[string]any{
		"name": "Retail Corp", "country": "United States", "fiscalCalendarPattern": "445",
		"financialYearEnd": "01-31", "fiscalWeekEndDay": "saturday", "fiscalYearEndRule": "nearest",
	})
	ot := s.createObligationType(tenant, "VAT-445")
	wf := s.createWorkflow(tenant, entity, ot, "monthly", "2024", codes(12, "M"))
	s.addTask(tenant, wf, "File", "submission", "filing_deadline", 0, 0)

	var start struct {
		InstancesCreated int `json:"instancesCreated"`
	}
	r := s.start(tenant, wf, nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &start)
	s.Require().Equal(12, start.InstancesCreated)

	expectedEnds := map[string][2]string{
		"M1": {"2023-01-29", "2023-02-25"}, "M2": {"2023-02-26", "2023-03-25"}, "M3": {"2023-03-26", "2023-04-29"},
		"M4": {"2023-04-30", "2023-05-27"}, "M5": {"2023-05-28", "2023-06-24"}, "M6": {"2023-06-25", "2023-07-29"},
		"M7": {"2023-07-30", "2023-08-26"}, "M8": {"2023-08-27", "2023-09-23"}, "M9": {"2023-09-24", "2023-10-28"},
		"M10": {"2023-10-29", "2023-11-25"}, "M11": {"2023-11-26", "2023-12-23"}, "M12": {"2023-12-24", "2024-02-03"},
	}
	got := byPeriod(s.instances(tenant, wf))
	s.Require().Len(got, 12)
	for code, want := range expectedEnds {
		s.Require().Equal(want[1], got[code].PeriodEndDate, code)
	}
	// Filing = period end + 15d; the task is due on the filing deadline; no
	// obligation → payment = filing.
	s.Require().Equal("2024-02-18", got["M12"].FilingDeadline)
	s.Require().Equal("2024-02-18", got["M12"].DueDate)
	s.Require().NotNil(got["M12"].PaymentDeadline)
	s.Require().Equal("2024-02-18", *got["M12"].PaymentDeadline)

	// The periods endpoint is the same calendar.
	rows := s.periodRows(tenant, entity, "monthly", 2024)
	s.Require().Len(rows, 12)
	for i, row := range rows {
		code := fmt.Sprintf("M%d", i+1)
		s.Require().Equal(code, row.Code)
		s.Require().Equal(expectedEnds[code][0], row.StartDate, code)
		s.Require().Equal(expectedEnds[code][1], row.EndDate, code)
		s.Require().True(strings.HasPrefix(row.Label, code+" ("), row.Label)
	}
	s.Require().Equal("M12 (24 Dec 2023 – 3 Feb 2024)", rows[11].Label)

	weeks := s.periodRows(tenant, entity, "weekly", 2024)
	s.Require().Len(weeks, 53)
	s.Require().Equal("W53", weeks[52].Code)
	s.Require().Equal("2024-02-03", weeks[52].EndDate)

	quarters := s.periodRows(tenant, entity, "quarterly", 2024)
	s.Require().Len(quarters, 4)
	s.Require().Equal("2023-04-29", quarters[0].EndDate)
	s.Require().Equal("2024-02-03", quarters[3].EndDate)
}

// Test13PeriodQuarterly: a 13-period entity on the same anchor plans Q1..Q4
// (Q4 carries the 53rd week), exposes P1..P13 as its months, and refuses an
// M code with a 400 naming it.
func (s *FiscalSuite) Test13PeriodQuarterly() {
	tenant := s.InsertTenant("fiscal-13p", "13-period Tenant").String()
	entity := s.createEntity(tenant, map[string]any{
		"name": "Grocer", "country": "United States", "fiscalCalendarPattern": "13-period",
		"financialYearEnd": "01-31", "fiscalWeekEndDay": "saturday", "fiscalYearEndRule": "nearest",
	})
	ot := s.createObligationType(tenant, "VAT-13P")
	wf := s.createWorkflow(tenant, entity, ot, "quarterly", "2024", codes(4, "Q"))
	s.addTask(tenant, wf, "File", "submission", "filing_deadline", 0, 0)
	s.start(tenant, wf, nil).AssertStatus(s.T(), http.StatusCreated)

	got := byPeriod(s.instances(tenant, wf))
	s.Require().Len(got, 4)
	s.Require().Equal("2023-04-29", got["Q1"].PeriodEndDate)
	s.Require().Equal("2023-07-29", got["Q2"].PeriodEndDate)
	s.Require().Equal("2023-10-28", got["Q3"].PeriodEndDate)
	s.Require().Equal("2024-02-03", got["Q4"].PeriodEndDate)

	months := s.periodRows(tenant, entity, "monthly", 2024)
	s.Require().Len(months, 13)
	s.Require().Equal("P1", months[0].Code)
	s.Require().Equal("2023-02-25", months[0].EndDate)
	s.Require().Equal("P13", months[12].Code)
	s.Require().Equal("2023-12-31", months[12].StartDate)
	s.Require().Equal("2024-02-03", months[12].EndDate)

	// A code outside the pattern fails closed before anything is created.
	bad := s.createWorkflow(tenant, entity, ot, "monthly", "2024", []string{"M1"})
	s.addTask(tenant, bad, "File", "submission", "filing_deadline", 0, 0)
	r := s.start(tenant, bad, nil)
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), `\"M1\"`)
	s.Require().Empty(s.instances(tenant, bad))
	r = s.As(tenant).GET(s.T(), "/workflows/"+bad+"/preview")
	r.AssertStatus(s.T(), http.StatusBadRequest)
}

// TestCustomPeriods: a custom calendar's listed codes are the only periods
// (monthly / quarterly / bi-annual all return the same list), annual is Y1,
// weekly is refused.
func (s *FiscalSuite) TestCustomPeriods() {
	tenant := s.InsertTenant("fiscal-custom", "Custom Tenant").String()
	entity := s.createEntity(tenant, map[string]any{
		"name": "Trimester Co", "country": "France", "fiscalCalendarPattern": "custom", "financialYearEnd": "12-31",
		"customPeriods": []map[string]any{
			{"code": "T1", "name": "Trimester 1", "startDate": "01-01", "endDate": "04-30"},
			{"code": "T2", "name": "Trimester 2", "startDate": "05-01", "endDate": "08-31"},
			{"code": "T3", "name": "Trimester 3", "startDate": "09-01", "endDate": "12-31"},
		},
	})
	ot := s.createObligationType(tenant, "VAT-CUSTOM")
	wf := s.createWorkflow(tenant, entity, ot, "monthly", "2026", []string{"T1", "T2", "T3"})
	s.addTask(tenant, wf, "File", "submission", "filing_deadline", 0, 0)
	s.start(tenant, wf, nil).AssertStatus(s.T(), http.StatusCreated)

	got := byPeriod(s.instances(tenant, wf))
	s.Require().Len(got, 3)
	s.Require().Equal("2026-04-30", got["T1"].PeriodEndDate)
	s.Require().Equal("2026-08-31", got["T2"].PeriodEndDate)
	s.Require().Equal("2026-12-31", got["T3"].PeriodEndDate)
	s.Require().Equal("2026-05-15", got["T1"].FilingDeadline)

	for _, periodicity := range []string{"monthly", "quarterly", "bi-annual"} {
		rows := s.periodRows(tenant, entity, periodicity, 2026)
		s.Require().Len(rows, 3, periodicity)
		s.Require().Equal("T1", rows[0].Code)
		s.Require().Equal("2026-01-01", rows[0].StartDate)
		s.Require().Equal("2026-04-30", rows[0].EndDate)
		s.Require().Equal("Trimester 1 (1 Jan – 30 Apr 2026)", rows[0].Label)
	}
	annual := s.periodRows(tenant, entity, "annual", 2026)
	s.Require().Len(annual, 1)
	s.Require().Equal("Y1", annual[0].Code)
	s.Require().Equal("2026-12-31", annual[0].EndDate)

	r := s.periods(tenant, entity, "weekly", 2026)
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "weekly")

	// A code that is not listed → 400 naming it.
	bad := s.createWorkflow(tenant, entity, ot, "monthly", "2026", []string{"M1"})
	s.addTask(tenant, bad, "File", "submission", "filing_deadline", 0, 0)
	r = s.start(tenant, bad, nil)
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), `\"M1\"`)
}

// TestWeeklyUnderStandard: seven-day weeks from 1 Jan; W52 absorbs the
// remainder and ends on the fiscal-year end.
func (s *FiscalSuite) TestWeeklyUnderStandard() {
	tenant := s.InsertTenant("fiscal-weekly", "Weekly Tenant").String()
	entity := s.createEntity(tenant, map[string]any{
		"name": "Weekly Filer", "country": "Germany", "fiscalCalendarPattern": "standard", "financialYearEnd": "12-31",
	})
	ot := s.createObligationType(tenant, "VAT-WEEKLY")
	wf := s.createWorkflow(tenant, entity, ot, "weekly", "2025", codes(52, "W"))
	s.addTask(tenant, wf, "File", "submission", "filing_deadline", 0, 0)

	var start struct {
		InstancesCreated int `json:"instancesCreated"`
	}
	r := s.start(tenant, wf, nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &start)
	s.Require().Equal(52, start.InstancesCreated)

	got := byPeriod(s.instances(tenant, wf))
	s.Require().Len(got, 52)
	s.Require().Equal("2025-01-07", got["W1"].PeriodEndDate)
	s.Require().Equal("2025-01-14", got["W2"].PeriodEndDate)
	s.Require().Equal("2025-12-23", got["W51"].PeriodEndDate)
	s.Require().Equal("2025-12-31", got["W52"].PeriodEndDate)

	rows := s.periodRows(tenant, entity, "weekly", 2025)
	s.Require().Len(rows, 52)
	s.Require().Equal("2025-01-01", rows[0].StartDate)
	s.Require().Equal("2025-12-24", rows[51].StartDate)
	s.Require().Equal("2025-12-31", rows[51].EndDate)
}

// TestPaymentOffsetRule: an entity obligation with a payment offset (1 month
// + 10 days, next business day) drives paymentDeadline — M3 2025: 31 Mar → 30
// Apr → Sat 10 May → Mon 12 May — and a payment-type task referencing
// payment_deadline is due off it; the preview shows the same values, and a
// paymentDeadline override replaces them at start.
func (s *FiscalSuite) TestPaymentOffsetRule() {
	tenant := s.InsertTenant("fiscal-pay", "Payment Tenant").String()
	entity := s.createEntity(tenant, map[string]any{
		"name": "Payer", "country": "Germany", "fiscalCalendarPattern": "standard", "financialYearEnd": "12-31",
	})
	ot := s.createObligationType(tenant, "VAT-PAY")
	r := s.As(tenant).POST(s.T(), "/entity-obligations", map[string]any{
		"entityId": entity, "obligationTypeId": ot, "periodicity": "monthly",
		"deadlineRule": map[string]any{
			"type":              "period_offset",
			"filingOffset":      map[string]any{"months": 0, "days": 15},
			"paymentOffset":     map[string]any{"months": 1, "days": 10},
			"weekendAdjustment": "next-business-day",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)

	wf := s.createWorkflow(tenant, entity, ot, "monthly", "2025", []string{"M3"})
	fileID := s.addTask(tenant, wf, "File", "submission", "filing_deadline", 5, 0)
	// A payment task with no explicit reference defaults to payment_deadline.
	var pay struct {
		ID               string `json:"id"`
		DueDateReference string `json:"dueDateReference"`
	}
	r = s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf, "name": "Pay", "taskType": "payment",
		"dueDateOffsetValue": 3, "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": 1,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &pay)
	s.Require().Equal("payment_deadline", pay.DueDateReference)

	// Preview carries the derived payment deadline.
	var preview struct {
		Tasks []previewRow `json:"tasks"`
	}
	r = s.As(tenant).GET(s.T(), "/workflows/"+wf+"/preview")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &preview)
	s.Require().Len(preview.Tasks, 2)
	s.Require().Equal(fileID, preview.Tasks[0].TemplateID)
	s.Require().Equal("2025-03-31", preview.Tasks[0].PeriodEndDate)
	s.Require().Equal("2025-04-15", preview.Tasks[0].FilingDeadline)
	s.Require().Equal("2025-05-12", preview.Tasks[0].PaymentDeadline)
	s.Require().Equal("2025-04-10", preview.Tasks[0].DueDate)
	s.Require().Equal(pay.ID, preview.Tasks[1].TemplateID)
	s.Require().Equal("2025-05-12", preview.Tasks[1].PaymentDeadline)
	s.Require().Equal("2025-05-09", preview.Tasks[1].DueDate)

	// Start with a paymentDeadline override on the payment task only.
	r = s.start(tenant, wf, map[string]any{"taskOverrides": map[string]any{
		pay.ID + "_M3": map[string]any{"paymentDeadline": "2025-05-20"},
	}})
	r.AssertStatus(s.T(), http.StatusCreated)

	rows := s.instances(tenant, wf)
	s.Require().Len(rows, 2)
	byTemplate := map[string]instanceRow{}
	for _, row := range rows {
		byTemplate[row.WorkflowTaskID] = row
	}
	s.Require().Equal("2025-05-12", *byTemplate[fileID].PaymentDeadline)
	s.Require().Equal("2025-04-10", byTemplate[fileID].DueDate)
	s.Require().Equal("2025-05-20", *byTemplate[pay.ID].PaymentDeadline)
	s.Require().Equal("2025-05-17", byTemplate[pay.ID].DueDate)

	// A malformed override date is refused before anything is created.
	wf2 := s.createWorkflow(tenant, entity, ot, "monthly", "2025", []string{"M4"})
	id2 := s.addTask(tenant, wf2, "Pay", "payment", "payment_deadline", 0, 0)
	r = s.start(tenant, wf2, map[string]any{"taskOverrides": map[string]any{
		id2 + "_M4": map[string]any{"paymentDeadline": "2025-13-40"},
	}})
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "invalid paymentDeadline")
	s.Require().Empty(s.instances(tenant, wf2))
}

// TestPaymentFixedDates: fixed payment dates pick the first MM-DD on/after
// the period end (wrapping into the next year), with no weekend adjustment.
func (s *FiscalSuite) TestPaymentFixedDates() {
	tenant := s.InsertTenant("fiscal-fixed", "Fixed Tenant").String()
	entity := s.createEntity(tenant, map[string]any{
		"name": "Fixed Payer", "country": "Germany", "fiscalCalendarPattern": "standard", "financialYearEnd": "12-31",
	})
	ot := s.createObligationType(tenant, "CIT-FIXED")
	s.As(tenant).POST(s.T(), "/entity-obligations", map[string]any{
		"entityId": entity, "obligationTypeId": ot, "periodicity": "quarterly",
		"deadlineRule": map[string]any{
			"type": "fixed", "fixedDates": []string{"04-30", "10-31"}, "paymentFixedDates": []string{"05-31", "11-30"},
			"weekendAdjustment": "next-business-day",
		},
	}).AssertStatus(s.T(), http.StatusCreated)

	wf := s.createWorkflow(tenant, entity, ot, "quarterly", "2025", []string{"Q1", "Q4"})
	s.addTask(tenant, wf, "Pay", "payment", "payment_deadline", 0, 0)
	s.start(tenant, wf, nil).AssertStatus(s.T(), http.StatusCreated)

	got := byPeriod(s.instances(tenant, wf))
	s.Require().Len(got, 2)
	s.Require().Equal("2025-03-31", got["Q1"].PeriodEndDate)
	s.Require().Equal("2025-05-31", *got["Q1"].PaymentDeadline, "Saturday 31 May stays (no adjustment)")
	s.Require().Equal("2025-05-31", got["Q1"].DueDate)
	s.Require().Equal("2025-12-31", got["Q4"].PeriodEndDate)
	s.Require().Equal("2026-05-31", *got["Q4"].PaymentDeadline, "wraps into the next year")
}

// TestNoObligationPaymentEqualsFiling: without an entity obligation the
// payment deadline is the filing deadline; a row generated before ADR-0023
// (payment_deadline NULL) renders paymentDeadline: null.
func (s *FiscalSuite) TestNoObligationPaymentEqualsFiling() {
	tenant := s.InsertTenant("fiscal-none", "No Obligation Tenant").String()
	entity := s.createEntity(tenant, map[string]any{
		"name": "Plain", "country": "Germany", "financialYearEnd": "03-31",
	})
	ot := s.createObligationType(tenant, "VAT-NONE")
	wf := s.createWorkflow(tenant, entity, ot, "monthly", "2026", []string{"M1", "M12"})
	s.addTask(tenant, wf, "Pay", "payment", "payment_deadline", 2, 0)
	s.start(tenant, wf, nil).AssertStatus(s.T(), http.StatusCreated)

	got := byPeriod(s.instances(tenant, wf))
	s.Require().Len(got, 2)
	// April-start standard year: M1 = Apr 2025, M12 = Mar 2026.
	s.Require().Equal("2025-04-30", got["M1"].PeriodEndDate)
	s.Require().Equal("2026-03-31", got["M12"].PeriodEndDate)
	for code, row := range got {
		s.Require().NotNil(row.PaymentDeadline, code)
		s.Require().Equal(row.FilingDeadline, *row.PaymentDeadline, code)
	}
	s.Require().Equal("2025-05-13", got["M1"].DueDate, "filing 15 May − 2d")

	// Legacy row: NULL payment_deadline → null in the view (RLS'd table, so
	// the update runs under the tenant GUC).
	tx := s.DB.MustBegin()
	_, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant)
	s.Require().NoError(err)
	_, err = tx.Exec(`UPDATE task_instances SET payment_deadline = NULL WHERE id = $1`, got["M1"].ID)
	s.Require().NoError(err)
	s.Require().NoError(tx.Commit())

	var legacy instanceRow
	s.As(tenant).GET(s.T(), "/task-instances/"+got["M1"].ID).DecodeData(s.T(), &legacy)
	s.Require().Nil(legacy.PaymentDeadline)
}

// TestPeriodsEndpointGuards: the periods endpoint is tenant-scoped (404 for
// another tenant's entity), validates its query (400), and serves the
// standard calendar including an April-start (UK) year.
func (s *FiscalSuite) TestPeriodsEndpointGuards() {
	tenant := s.InsertTenant("fiscal-guard", "Guard Tenant").String()
	other := s.InsertTenant("fiscal-guard-b", "Guard Tenant B").String()
	entity := s.createEntity(tenant, map[string]any{"name": "Std", "country": "Germany", "financialYearEnd": "12-31"})
	uk := s.createEntity(tenant, map[string]any{"name": "UK Ltd", "country": "United Kingdom", "financialYearEnd": "03-31"})

	rows := s.periodRows(tenant, entity, "monthly", 2026)
	s.Require().Len(rows, 12)
	s.Require().Equal(periodRow{Code: "M1", Label: "M1 (1 Jan – 31 Jan 2026)", StartDate: "2026-01-01", EndDate: "2026-01-31"}, rows[0])
	s.Require().Equal("2026-02-28", rows[1].EndDate)
	s.Require().Equal("2026-12-31", rows[11].EndDate)

	rows = s.periodRows(tenant, uk, "monthly", 2026)
	s.Require().Equal(periodRow{Code: "M1", Label: "M1 (1 Apr – 30 Apr 2025)", StartDate: "2025-04-01", EndDate: "2025-04-30"}, rows[0])
	s.Require().Equal("2026-03-31", rows[11].EndDate)
	rows = s.periodRows(tenant, uk, "quarterly", 2026)
	s.Require().Equal("2025-06-30", rows[0].EndDate)
	s.Require().Equal("2026-03-31", rows[3].EndDate)
	rows = s.periodRows(tenant, uk, "consolidated-annual", 2026)
	s.Require().Equal(periodRow{Code: "CY1", Label: "CY1 (1 Apr 2025 – 31 Mar 2026)", StartDate: "2025-04-01", EndDate: "2026-03-31"}, rows[0])

	// Other tenant → 404; anonymous → 401; a viewer may read.
	s.periods(other, entity, "monthly", 2026).AssertStatus(s.T(), http.StatusNotFound)
	s.Client.External().GET(s.T(), "/entities/"+entity+"/periods?periodicity=monthly&financialYear=2026").
		AssertStatus(s.T(), http.StatusUnauthorized)
	s.AsRole(tenant, "viewer").GET(s.T(), "/entities/"+entity+"/periods?periodicity=monthly&financialYear=2026").
		AssertStatus(s.T(), http.StatusOK)

	// Query validation.
	s.As(tenant).GET(s.T(), "/entities/"+entity+"/periods?periodicity=fortnightly&financialYear=2026").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/entities/"+entity+"/periods?periodicity=monthly").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/entities/"+entity+"/periods?periodicity=monthly&financialYear=abc").AssertStatus(s.T(), http.StatusBadRequest)
}
