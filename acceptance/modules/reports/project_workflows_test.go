package reports_test

import "net/http"

// TestProjectWorkflowsStayOutOfTheComplianceReports: a started project
// workflow's instances (period "PROJECT") show up in the enriched task list,
// the per-workflow stats and every raw export dataset — but never in the
// three compliance / financial reports, which classify obligation periods of
// RECURRING workflows only. A recurring workflow sits alongside so the
// exclusion is a proper subset assertion.
func (s *ReportsSuite) TestProjectWorkflowsStayOutOfTheComplianceReports() {
	tenant := s.InsertTenant("pj-r", "Project Reports").String()
	entity := s.seedEntity(tenant, "Acme Project", "Germany")
	vat := s.seedObligationType(tenant, "VAT Return", "VAT-P", "VAT")

	recurring := s.seedWorkflowFor(tenant, "Recurring VAT", entity, vat, "2025", []string{"M1"})
	s.addTypedTemplate(tenant, recurring, "File", "submission", "filing_deadline", 5, "before", 0)
	s.start(tenant, recurring)

	// The project shares the entity and the financial year, so no filter can
	// tell it apart from the recurring one — only the category does.
	project := s.postID(tenant, "/workflows", map[string]any{
		"name": "Restructuring 2025", "workflowCategory": "project", "projectType": "restructuring",
		"entityId": entity, "financialYear": "2025", "startDate": "2025-01-15", "endDate": "2025-03-31",
	})
	s.addTypedTemplate(tenant, project, "Draft memo", "preparation", "filing_deadline", 10, "before", 0)
	s.addTypedTemplate(tenant, project, "Settle fees", "payment", "payment_deadline", 0, "before", 1)
	s.start(tenant, project)
	memo := s.instanceID(tenant, project, "PROJECT", "Draft memo")
	s.As(tenant).PUT(s.T(), "/task-instances/"+memo, map[string]any{
		"status": "in_progress", "taxData": map[string]any{"engagementCost": 900, "amount": 500},
	}).AssertStatus(s.T(), http.StatusOK)

	// ---- enriched task list: the project's two instances are there.
	var rows []reportRow
	pg := s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances"), &rows)
	s.Require().Equal(3, pg.Total)
	rows = nil
	pg = s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?workflowId="+project), &rows)
	s.Require().Equal(2, pg.Total)
	for _, row := range rows {
		s.Require().Equal("PROJECT", row.PeriodCode)
		s.Require().Equal("project", row.WorkflowCategory)
		s.Require().NotNil(row.ProjectType)
		s.Require().Equal("restructuring", *row.ProjectType)
		s.Require().Equal("2025-03-31", row.PeriodEndDate)
		s.Require().Equal("2025-03-31", row.FilingDeadline)
		s.Require().Nil(row.PaymentDeadline)
	}
	s.Require().Equal("2025-03-21", rows[0].DueDate, "Draft memo: end date − 10d")
	s.Require().Equal("2025-03-31", rows[1].DueDate, "Settle fees: payment_deadline → end date")

	// ---- workflow-stats: the project counts like any workflow.
	stats := map[string]workflowStat{}
	s.As(tenant).GET(s.T(), "/reports/workflow-stats").DecodeData(s.T(), &stats)
	s.Require().Len(stats, 2)
	s.Require().Equal(2, stats[project].TotalTasks)
	s.Require().NotNil(stats[project].NextDueDate)
	s.Require().Equal("2025-03-21", *stats[project].NextDueDate)

	// ---- raw export: every dataset carries the project.
	ex := s.export(tenant, "/reports/export-raw?dataset=workflows&category=project")
	s.Require().Equal(1, ex.TotalCount)
	s.Require().Equal("Restructuring 2025", ex.Rows[0]["workflowName"])
	s.Require().Equal("active", ex.Rows[0]["status"])
	s.Require().Equal("2025-03-31", ex.Rows[0]["endDate"])
	ex = s.export(tenant, "/reports/export-raw?dataset=tasks")
	s.Require().Equal(3, ex.TotalCount)
	ex = s.export(tenant, "/reports/export-raw?dataset=tasks&category=project")
	s.Require().Equal(2, ex.TotalCount)
	for _, row := range ex.Rows {
		s.Require().Equal("PROJECT", row["periodCode"])
		s.Require().Equal("project", row["workflowCategory"])
	}
	ex = s.export(tenant, "/reports/export-raw?dataset=tasks&category=recurring")
	s.Require().Equal(1, ex.TotalCount)
	ex = s.export(tenant, "/reports/export-raw?dataset=tax-data")
	s.Require().Equal(1, ex.TotalCount)
	s.Require().Equal("Draft memo", ex.Rows[0]["taskName"])
	s.Require().Equal(float64(900), ex.Rows[0]["engagementCost"])

	// ---- the three compliance / financial reports: recurring only.
	var hm heatmapData
	s.getJSON(tenant, "/reports/compliance-heatmap", &hm)
	s.Require().Equal([]idLabel{{"2025:M1", "M1 (FY2025)"}}, hm.Cols) // no year selected: FY-qualified (ADR-0026 §7)
	s.Require().Len(hm.Cells, 1)
	s.Require().Equal([]string{recurring}, hm.Cells[0].WorkflowIDs)
	s.getJSON(tenant, "/reports/compliance-heatmap?entityId="+entity+"&year=2025&viewMode=tax-type", &hm)
	s.Require().Len(hm.Cells, 1)
	s.Require().Equal([]string{recurring}, hm.Cells[0].WorkflowIDs)

	var cs complianceData
	s.getJSON(tenant, "/reports/compliance-status", &cs)
	s.Require().Equal(1, cs.TotalCount)
	s.Require().Equal(1, cs.Summary.Total)
	s.Require().Equal("M1", cs.Rows[0].Period)
	s.Require().Equal(recurring, cs.Rows[0].WorkflowID)
	s.getJSON(tenant, "/reports/compliance-status?year=2025&entityId="+entity, &cs)
	s.Require().Equal(1, cs.TotalCount)

	// The project's tax data never feeds the financial report; the recurring
	// instance's does once it carries some.
	var tf financialData
	s.getJSON(tenant, "/reports/tax-financial", &tf)
	s.Require().Equal(0, tf.TotalCount)
	s.Require().Equal(float64(0), tf.Summary.TotalEngagementCost)
	s.Require().Empty(tf.ChartData)
	file := s.instanceID(tenant, recurring, "M1", "File")
	s.As(tenant).PUT(s.T(), "/task-instances/"+file, map[string]any{
		"status": "in_progress", "taxData": map[string]any{"outputVat": 100},
	}).AssertStatus(s.T(), http.StatusOK)
	s.getJSON(tenant, "/reports/tax-financial?year=2025", &tf)
	s.Require().Equal(1, tf.TotalCount)
	s.Require().Equal("M1", tf.Rows[0].Period)
	s.Require().Equal(float64(0), tf.Summary.TotalEngagementCost)
	s.Require().Equal(float64(100), tf.Summary.TotalOutputVat)
}
