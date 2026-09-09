package reports_test

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
)

// The heatmap with years of history (ADR-0026 decision 7).
//
// Period view, no year selected: every column is qualified by financial year
// (id "<financialYear>:<periodCode>", label "M1 (FY2025)"), so the same
// period code of two fiscal years is two columns; columns follow the calendar
// (first period end), then label. With a year selected the column id stays
// the bare period code. The tax-type view is untouched (its columns are
// obligation types, whatever the year).
func (s *ReportsSuite) TestHeatmapQualifiesPeriodColumnsByYearWhenNoYearIsSelected() {
	tenant := s.InsertTenant("hm-fy", "Heatmap Years").String()
	alpha := s.seedEntity(tenant, "Year Alpha", "Germany")
	beta := s.seedEntity(tenant, "Year Beta", "France")
	vat := s.seedObligationType(tenant, "VAT Return", "VAT-Y", "VAT")

	// The same period codes (M1, M2) in two fiscal years, for two entities:
	// one File task per period, so every cell is one instance of one workflow.
	wf := map[string]string{}
	for _, e := range []struct{ key, id string }{{"alpha", alpha}, {"beta", beta}} {
		for _, fy := range []string{"2025", "2026"} {
			id := s.seedWorkflowFor(tenant, e.key+" VAT "+fy, e.id, vat, fy, []string{"M1", "M2"})
			s.addTypedTemplate(tenant, id, "File", "submission", "filing_deadline", 5, "before", 0)
			s.start(tenant, id)
			wf[e.key+fy] = id
		}
	}

	qualified := []idLabel{
		{"2025:M1", "M1 (FY2025)"}, {"2025:M2", "M2 (FY2025)"},
		{"2026:M1", "M1 (FY2026)"}, {"2026:M2", "M2 (FY2026)"},
	}

	// ---- year=all: four distinct columns in calendar order, every
	// (entity × year × period) cell present and fed by that year's workflow.
	var hm heatmapData
	s.getJSON(tenant, "/reports/compliance-heatmap?year=all&viewMode=period", &hm)
	s.Require().Equal(qualified, hm.Cols)
	s.Require().Equal([]idLabel{{alpha, "Year Alpha"}, {beta, "Year Beta"}}, hm.Rows)
	s.Require().Len(hm.Cells, 8)
	s.Require().Equal(8, hm.Summary.TotalCells)
	for _, e := range []struct{ key, id string }{{"alpha", alpha}, {"beta", beta}} {
		for _, fy := range []string{"2025", "2026"} {
			for _, code := range []string{"M1", "M2"} {
				c := hm.cell(e.id, fy+":"+code)
				s.Require().NotNil(c, "cell %s × %s:%s", e.key, fy, code)
				s.Require().Equal(code+" (FY"+fy+")", c.ColLabel)
				s.Require().Equal(1, c.TotalTasks)
				s.Require().Equal([]string{wf[e.key+fy]}, c.WorkflowIDs)
			}
		}
	}
	// Cells arrive by row label, then calendar: FY2025 before FY2026 inside a row.
	s.Require().Equal("Year Alpha", hm.Cells[0].RowLabel)
	s.Require().Equal("2025:M1", hm.Cells[0].ColID)
	s.Require().Equal("2026:M2", hm.Cells[3].ColID)
	s.Require().Equal("Year Beta", hm.Cells[4].RowLabel)

	// A blank year is the same thing as "all".
	s.getJSON(tenant, "/reports/compliance-heatmap", &hm)
	s.Require().Equal(qualified, hm.Cols)
	s.Require().Len(hm.Cells, 8)
	s.getJSON(tenant, "/reports/compliance-heatmap?year=", &hm)
	s.Require().Equal(qualified, hm.Cols)

	// ---- a selected year keeps the bare period code (nothing changes for
	// year-scoped callers), and the cells belong to that year's workflows only.
	for _, fy := range []string{"2025", "2026"} {
		s.getJSON(tenant, "/reports/compliance-heatmap?year="+fy, &hm)
		s.Require().Equal([]idLabel{{"M1", "M1"}, {"M2", "M2"}}, hm.Cols, "year=%s", fy)
		s.Require().Len(hm.Cells, 4)
		c := hm.cell(alpha, "M1")
		s.Require().NotNil(c)
		s.Require().Equal("M1", c.ColLabel)
		s.Require().Equal([]string{wf["alpha"+fy]}, c.WorkflowIDs)
	}

	// ---- the entity filter without a year: still qualified.
	s.getJSON(tenant, "/reports/compliance-heatmap?entityId="+beta, &hm)
	s.Require().Equal(qualified, hm.Cols)
	s.Require().Equal([]idLabel{{beta, "Year Beta"}}, hm.Rows)
	s.Require().Len(hm.Cells, 4)

	// ---- tax-type view: unaffected — one obligation column, both years'
	// instances in one cell per entity.
	s.getJSON(tenant, "/reports/compliance-heatmap?viewMode=tax-type", &hm)
	s.Require().Equal([]idLabel{{vat, "VAT Return (VAT-Y)"}}, hm.Cols)
	s.Require().Len(hm.Cells, 2)
	c := hm.cell(alpha, vat)
	s.Require().NotNil(c)
	s.Require().Equal(4, c.TotalTasks)
	s.Require().Equal(sortedCopy([]string{wf["alpha2025"], wf["alpha2026"]}), sortedCopy(c.WorkflowIDs))
}

// The grid is bounded: above domain.MaxHeatmapCells the endpoint answers 400
// VALIDATION "narrow it by year or entity" — never a truncated grid — and
// narrowing by year or entity brings the grid back.
//
// The cap is exercised for real, against the shipped constant: two fiscal
// years of one entity are padded with synthetic periods written straight into
// task_instances (one INSERT … generate_series per year; seeding 5,000
// periods through the API would be hundreds of requests). The rows are
// ordinary participating instances of a started recurring workflow — the
// cap counts cells, and a cell is one (entity, year, period).
func (s *ReportsSuite) TestHeatmapAnswers400AboveTheCellCap() {
	tenant := s.InsertTenant("hm-cap", "Heatmap Cap").String()
	entity := s.seedEntity(tenant, "Cap Entity", "Germany")
	other := s.seedEntity(tenant, "Cap Other", "France")
	vat := s.seedObligationType(tenant, "VAT Return", "VAT-C", "VAT")

	half := domain.MaxHeatmapCells / 2
	// FY2024: M1 + (half−1) synthetic periods = half cells.
	wf2024 := s.seedWorkflowFor(tenant, "Cap 2024", entity, vat, "2024", []string{"M1"})
	tpl2024 := s.addTypedTemplate(tenant, wf2024, "File", "submission", "filing_deadline", 5, "before", 0)
	s.start(tenant, wf2024)
	s.padPeriods(tenant, wf2024, tpl2024, 1, half-1)
	// FY2025: M1 + (half−2) synthetic periods = half−1 cells.
	wf2025 := s.seedWorkflowFor(tenant, "Cap 2025", entity, vat, "2025", []string{"M1"})
	tpl2025 := s.addTypedTemplate(tenant, wf2025, "File", "submission", "filing_deadline", 5, "before", 0)
	s.start(tenant, wf2025)
	s.padPeriods(tenant, wf2025, tpl2025, 1, half-2)
	// The other entity: one FY2025 cell. Total = exactly the cap.
	otherWf := s.seedWorkflowFor(tenant, "Other 2025", other, vat, "2025", []string{"M1"})
	s.addTypedTemplate(tenant, otherWf, "File", "submission", "filing_deadline", 5, "before", 0)
	s.start(tenant, otherWf)

	// ---- exactly the cap is a full grid.
	var hm heatmapData
	s.getJSON(tenant, "/reports/compliance-heatmap?year=all", &hm)
	s.Require().Equal(domain.MaxHeatmapCells, hm.Summary.TotalCells)
	s.Require().Len(hm.Cells, domain.MaxHeatmapCells)
	s.Require().Len(hm.Rows, 2)
	s.Require().Len(hm.Cols, domain.MaxHeatmapCells-1, "the two entities share the 2025:M1 column")
	s.Require().NotNil(hm.cell(entity, "2024:M1"))
	s.Require().NotNil(hm.cell(entity, "2025:P"+strconv.Itoa(half-2)))
	s.Require().NotNil(hm.cell(other, "2025:M1"))

	// ---- one cell more: 400 with the documented code and message.
	s.padPeriods(tenant, wf2025, tpl2025, half-1, half-1)
	r := s.As(tenant).GET(s.T(), "/reports/compliance-heatmap?year=all")
	r.AssertStatus(s.T(), http.StatusBadRequest)
	r.AssertErrorCode(s.T(), "VALIDATION")
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(r.BodyString()), &env))
	s.Require().Equal(domain.ErrHeatmapTooLarge, env.Error.Message)
	s.Require().Equal("the heatmap has more than 5000 cells; narrow it by year or entity", env.Error.Message)
	// A blank year is the same request.
	s.As(tenant).GET(s.T(), "/reports/compliance-heatmap").AssertStatus(s.T(), http.StatusBadRequest)

	// ---- narrowing brings the grid back: by year …
	s.getJSON(tenant, "/reports/compliance-heatmap?year=2025", &hm)
	s.Require().Equal(half+1, hm.Summary.TotalCells) // half for the entity (M1 + half−1 synthetic) + the other's M1
	s.Require().Equal([]idLabel{{entity, "Cap Entity"}, {other, "Cap Other"}}, hm.Rows)
	s.Require().Equal("M1", hm.Cols[0].ID, "a selected year keeps bare period codes")
	s.getJSON(tenant, "/reports/compliance-heatmap?year=2024", &hm)
	s.Require().Equal(half, hm.Summary.TotalCells)
	// … or by entity (still every year, still qualified) …
	s.getJSON(tenant, "/reports/compliance-heatmap?entityId="+other, &hm)
	s.Require().Equal(1, hm.Summary.TotalCells)
	s.Require().Equal([]idLabel{{"2025:M1", "M1 (FY2025)"}}, hm.Cols)
	// … and the tax-type view never had more than entities × obligation types.
	s.getJSON(tenant, "/reports/compliance-heatmap?viewMode=tax-type", &hm)
	s.Require().Equal(2, hm.Summary.TotalCells)
	s.Require().Equal([]idLabel{{vat, "VAT Return (VAT-C)"}}, hm.Cols)
}

// padPeriods inserts synthetic instances "P<from>" … "P<to>" for a started
// workflow's template, one per period, straight into task_instances. The
// table is RLS'd, so the insert runs in a tenant-bound transaction like the
// app's Tx seam (tenant_id defaults from the setting); the far-future dates
// keep the rows out of every overdue window.
func (s *ReportsSuite) padPeriods(tenant, workflowID, templateID string, from, to int) {
	tx := s.DB.MustBegin()
	if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant); err != nil {
		_ = tx.Rollback()
		s.Require().NoError(err)
	}
	res, err := tx.Exec(`
		INSERT INTO task_instances
		    (workflow_id, workflow_task_id, period_code, name, task_type, due_date, period_end_date, filing_deadline)
		SELECT $1::uuid, $2::uuid, 'P' || n, 'File', 'submission',
		       DATE '2030-01-01' + n, DATE '2030-01-01' + n, DATE '2030-01-01' + n
		FROM generate_series($3::int, $4::int) AS n`, workflowID, templateID, from, to)
	if err != nil {
		_ = tx.Rollback()
		s.Require().NoError(err)
	}
	n, _ := res.RowsAffected()
	s.Require().EqualValues(to-from+1, n)
	s.Require().NoError(tx.Commit())
}
