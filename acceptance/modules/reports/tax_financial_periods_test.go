package reports_test

import "net/http"

// TestTaxFinancialPeriodsFollowTheCalendar: chartData and groupBy=period order
// their period buckets by the calendar (earliest period end, then code), never
// by period-code text — M9 before M10, M10 before M12, M1 first — whatever
// order the workflow listed its periods in and whatever the other groupings
// are sorted by.
func (s *ReportsSuite) TestTaxFinancialPeriodsFollowTheCalendar() {
	tenant := s.InsertTenant("tf-cal", "Calendar Order").String()
	entity := s.seedEntity(tenant, "Acme Calendar", "Germany")
	vat := s.seedObligationType(tenant, "VAT Return", "VAT-C", "VAT")
	// Listed out of calendar order on purpose; text order would be M1, M10, M11, M12, M9.
	wf := s.seedWorkflowFor(tenant, "Calendar VAT", entity, vat, "2025", []string{"M12", "M9", "M1", "M11", "M10"})
	s.addTypedTemplate(tenant, wf, "File", "submission", "filing_deadline", 5, "before", 0)
	s.start(tenant, wf)

	calendar := []string{"M1", "M9", "M10", "M11", "M12"}
	for i, code := range calendar {
		id := s.instanceID(tenant, wf, code, "File")
		s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{
			"status": "in_progress", "taxData": map[string]any{"outputVat": (i + 1) * 100, "inputVat": 10},
		}).AssertStatus(s.T(), http.StatusOK)
	}

	chartOrder := func(tf financialData) []string {
		out := make([]string, 0, len(tf.ChartData))
		for _, p := range tf.ChartData {
			out = append(out, p.Period)
		}
		return out
	}
	groupOrder := func(tf financialData) []string {
		out := make([]string, 0, len(tf.Aggregated))
		for _, g := range tf.Aggregated {
			out = append(out, g.Key)
		}
		return out
	}

	var tf financialData
	s.getJSON(tenant, "/reports/tax-financial?groupBy=period", &tf)
	s.Require().Equal(5, tf.TotalCount)
	s.Require().Equal(calendar, groupOrder(tf))
	s.Require().Equal(calendar, chartOrder(tf))
	s.Require().Equal(float64(100), tf.Aggregated[0].OutputVat, "M1")
	s.Require().Equal(float64(200), tf.Aggregated[1].OutputVat, "M9")
	s.Require().Equal(float64(500), tf.Aggregated[4].OutputVat, "M12")
	s.Require().Equal(float64(490), tf.ChartData[4].NetVat, "M12: 500 − 10")

	// Other groupings keep their label order; the chart still follows the calendar.
	s.getJSON(tenant, "/reports/tax-financial?groupBy=entity", &tf)
	s.Require().Equal([]string{entity}, groupOrder(tf))
	s.Require().Equal(calendar, chartOrder(tf))
	s.getJSON(tenant, "/reports/tax-financial?groupBy=taxType", &tf)
	s.Require().Equal([]string{"VAT"}, groupOrder(tf))
	s.Require().Equal(calendar, chartOrder(tf))
	s.Require().Equal(float64(1500), tf.Summary.TotalOutputVat)
}
