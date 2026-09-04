package reports_test

import (
	"net/http"
	"time"
)

// "Today" in the compliance classification is the TENANT's calendar date
// (ADR-0003 / ADR-0023 §6), not the server's. Two instances — filing deadline
// = today's UTC date and = yesterday's — are classified under UTC, UTC+14
// (Pacific/Kiritimati) and UTC−11 (Pacific/Pago_Pago); the expected outcome is
// computed from the current UTC time with Go's tz database, so the test is
// deterministic at any hour, and at every hour at least one zone's day differs
// from UTC's (Kiritimati is already tomorrow from 10:00 UTC, Pago Pago still
// yesterday until 11:00 UTC), so a flip is always asserted.
func (s *ReportsSuite) TestComplianceStatusUsesTenantDay() {
	tenant := s.InsertTenant("tz-r", "Zone Reports").String()
	entity := s.seedEntity(tenant, "Acme Zone", "Germany")
	vat := s.seedObligationType(tenant, "VAT Return", "VAT-Z", "VAT")
	wf := s.seedWorkflowFor(tenant, "Zone VAT", entity, vat, "2025", []string{"M1", "M2"})
	s.addTypedTemplate(tenant, wf, "Collect", "preparation", "period_end", 2, "after", 0)
	s.start(tenant, wf)
	todayTask := s.instanceID(tenant, wf, "M1", "Collect")
	yesterdayTask := s.instanceID(tenant, wf, "M2", "Collect")

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	s.setFilingDeadline(tenant, todayTask, today)
	s.setFilingDeadline(tenant, yesterdayTask, yesterday)

	// The engine's rule: missed when the deadline is before the tenant's day.
	expect := func(zone, deadline string) string {
		loc, err := time.LoadLocation(zone)
		s.Require().NoError(err)
		if deadline < now.In(loc).Format("2006-01-02") {
			return "missed"
		}
		return "not_due"
	}

	flips := 0
	for _, zone := range []string{"UTC", "Pacific/Kiritimati", "Pacific/Pago_Pago"} {
		s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "Zone Reports", "timezone": zone}).
			AssertStatus(s.T(), http.StatusOK)

		var cs complianceData
		s.getJSON(tenant, "/reports/compliance-status", &cs)
		s.Require().Equal(2, cs.TotalCount, zone)
		byInstance := map[string]string{}
		for _, row := range cs.Rows {
			byInstance[row.TaskInstanceID] = row.ComplianceStatus
		}
		wantToday, wantYesterday := expect(zone, today), expect(zone, yesterday)
		s.Require().Equal(wantToday, byInstance[todayTask], "%s: deadline %s", zone, today)
		s.Require().Equal(wantYesterday, byInstance[yesterdayTask], "%s: deadline %s", zone, yesterday)
		missed := 0
		for _, want := range []string{wantToday, wantYesterday} {
			if want == "missed" {
				missed++
			}
		}
		s.Require().Equal(missed, cs.Summary.Missed, zone)
		s.Require().Equal(2-missed, cs.Summary.NotDue, zone)
		if wantToday != expect("UTC", today) || wantYesterday != expect("UTC", yesterday) {
			flips++
		}

		// The heatmap shares the classification (overdue = missed on due_date,
		// which start set to period end + 2 days, long past → always overdue);
		// it must still answer under every zone.
		var hm heatmapData
		s.getJSON(tenant, "/reports/compliance-heatmap", &hm)
		s.Require().Len(hm.Cells, 2, zone)
	}
	s.Require().Positive(flips, "at any UTC hour at least one of UTC+14 / UTC-11 is on a different day than UTC")
}

// setFilingDeadline pins an instance's filing_deadline (a legal DATE) directly:
// task_instances is RLS'd, so the update runs in a tenant-bound transaction
// like the app's Tx seam.
func (s *ReportsSuite) setFilingDeadline(tenant, instanceID, date string) {
	tx := s.DB.MustBegin()
	if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant); err != nil {
		_ = tx.Rollback()
		s.Require().NoError(err)
	}
	res, err := tx.Exec(`UPDATE task_instances SET filing_deadline = $2::date WHERE id = $1`, instanceID, date)
	if err != nil {
		_ = tx.Rollback()
		s.Require().NoError(err)
	}
	n, _ := res.RowsAffected()
	s.Require().EqualValues(1, n)
	s.Require().NoError(tx.Commit())
}
