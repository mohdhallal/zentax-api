package reports_test

import (
	"net/http"
	"sort"
	"time"
)

// The four SQL-backed reporting endpoints (ADR-0021): compliance heatmap,
// compliance status, tax financial, raw export. Fixture (one tenant):
//
//	Acme Alpha (Germany) × VAT Return  FY2025 M1,M2 × {Collect, File}      = 4 instances
//	Acme Alpha (Germany) × Corporate Tax FY2025 M1,M2 × {Collect, Pay}     = 4
//	Acme Beta  (France)  × VAT Return  FY2025 M1,M2 × {Collect, File}      = 4
//	Acme Beta  (France)  × Corporate Tax FY2026 M12  × {Collect}           = 1
//
// FY2025 deadlines are in the past (missed / overdue); the FY2026 M12 one is
// in the future (not_due → on_time once completed).

const instantRegex = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`

type heatmapCell struct {
	RowID           string   `json:"rowId"`
	RowLabel        string   `json:"rowLabel"`
	ColID           string   `json:"colId"`
	ColLabel        string   `json:"colLabel"`
	Status          string   `json:"status"`
	TotalTasks      int      `json:"totalTasks"`
	CompletedTasks  int      `json:"completedTasks"`
	OverdueTasks    int      `json:"overdueTasks"`
	InProgressTasks int      `json:"inProgressTasks"`
	WorkflowIDs     []string `json:"workflowIds"`
}

type idLabel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type heatmapData struct {
	Rows    []idLabel     `json:"rows"`
	Cols    []idLabel     `json:"cols"`
	Cells   []heatmapCell `json:"cells"`
	Summary struct {
		TotalCells int `json:"totalCells"`
		Green      int `json:"green"`
		Amber      int `json:"amber"`
		Red        int `json:"red"`
	} `json:"summary"`
}

func (h heatmapData) cell(rowID, colID string) *heatmapCell {
	for i := range h.Cells {
		if h.Cells[i].RowID == rowID && h.Cells[i].ColID == colID {
			return &h.Cells[i]
		}
	}
	return nil
}

type complianceRow struct {
	EntityName       string  `json:"entityName"`
	EntityID         string  `json:"entityId"`
	TaxType          string  `json:"taxType"`
	ObligationName   string  `json:"obligationName"`
	ObligationCode   string  `json:"obligationCode"`
	Period           string  `json:"period"`
	FilingDeadline   string  `json:"filingDeadline"`
	FilingDate       *string `json:"filingDate"`
	ComplianceStatus string  `json:"complianceStatus"`
	PenaltyInterest  string  `json:"penaltyInterest"`
	WorkflowID       string  `json:"workflowId"`
	TaskInstanceID   string  `json:"taskInstanceId"`
}

type complianceData struct {
	Rows    []complianceRow `json:"rows"`
	Summary struct {
		Total  int `json:"total"`
		OnTime int `json:"onTime"`
		Late   int `json:"late"`
		Missed int `json:"missed"`
		NotDue int `json:"notDue"`
	} `json:"summary"`
	TotalCount int `json:"totalCount"`
}

// figures decode as float64: a string-typed number would fail to unmarshal.
type figures struct {
	OutputVat      float64 `json:"outputVat"`
	InputVat       float64 `json:"inputVat"`
	NetVat         float64 `json:"netVat"`
	TaxableIncome  float64 `json:"taxableIncome"`
	TaxLiability   float64 `json:"taxLiability"`
	WhtAmount      float64 `json:"whtAmount"`
	EngagementCost float64 `json:"engagementCost"`
	TotalAmount    float64 `json:"totalAmount"`
}

type financialRow struct {
	EntityName     string `json:"entityName"`
	EntityID       string `json:"entityId"`
	Country        string `json:"country"`
	TaxType        string `json:"taxType"`
	ObligationName string `json:"obligationName"`
	ObligationCode string `json:"obligationCode"`
	Period         string `json:"period"`
	FinancialYear  string `json:"financialYear"`
	figures
}

type aggregatedGroup struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
	figures
}

type chartPoint struct {
	Period       string  `json:"period"`
	OutputVat    float64 `json:"outputVat"`
	InputVat     float64 `json:"inputVat"`
	NetVat       float64 `json:"netVat"`
	TaxLiability float64 `json:"taxLiability"`
	WhtAmount    float64 `json:"whtAmount"`
	TotalAmount  float64 `json:"totalAmount"`
}

type financialData struct {
	Rows       []financialRow    `json:"rows"`
	Aggregated []aggregatedGroup `json:"aggregated"`
	ChartData  []chartPoint      `json:"chartData"`
	Summary    struct {
		TotalOutputVat      float64 `json:"totalOutputVat"`
		TotalInputVat       float64 `json:"totalInputVat"`
		TotalNetVat         float64 `json:"totalNetVat"`
		TotalTaxableIncome  float64 `json:"totalTaxableIncome"`
		TotalTaxLiability   float64 `json:"totalTaxLiability"`
		TotalWht            float64 `json:"totalWht"`
		TotalEngagementCost float64 `json:"totalEngagementCost"`
		TotalAmount         float64 `json:"totalAmount"`
		RecordCount         int     `json:"recordCount"`
	} `json:"summary"`
	TotalCount int `json:"totalCount"`
}

type exportData struct {
	Dataset    string           `json:"dataset"`
	Rows       []map[string]any `json:"rows"`
	TotalCount int              `json:"totalCount"`
}

// Column lists copied verbatim from export-raw-data.tsx.
var (
	workflowColumns = []string{
		"workflowName", "category", "projectType", "financialYear", "periodicity",
		"entityName", "country", "obligationName", "obligationCode", "taxType",
		"status", "startDate", "endDate", "tasksSequential", "createdAt",
	}
	taskColumns = []string{
		"taskName", "taskType", "status", "workflowName", "workflowCategory",
		"entityName", "country", "obligationName", "taxType", "periodCode",
		"financialYear", "assigneeName", "dueDate", "filingDeadline",
		"completedAt", "approvalRequired", "taxDataStatus",
	}
	taxDataColumns = []string{
		"taskName", "workflowName", "entityName", "country", "obligationName",
		"taxType", "periodCode", "financialYear", "taxDataStatus",
		"outputVat", "inputVat", "netVat", "taxableIncome", "taxLiability",
		"whtAmount", "penaltyAmount", "interestAmount", "engagementCost",
	}
)

// --- seeding helpers --------------------------------------------------------

func (s *ReportsSuite) postID(tenant, path string, body any) string {
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), path, body)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

func (s *ReportsSuite) seedEntity(tenant, name, country string) string {
	return s.postID(tenant, "/entities", map[string]any{
		"name": name, "country": country,
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
}

func (s *ReportsSuite) seedObligationType(tenant, name, code, template string) string {
	return s.postID(tenant, "/obligation-types", map[string]any{"name": name, "code": code, "template": template})
}

// seedWorkflowFor: recurring monthly workflow, filing = period end + 15 days.
func (s *ReportsSuite) seedWorkflowFor(tenant, name, entityID, obTypeID, fy string, periods []string) string {
	return s.postID(tenant, "/workflows", map[string]any{
		"name": name, "workflowCategory": "recurring",
		"entityId": entityID, "obligationTypeId": obTypeID,
		"periodicity": "monthly", "financialYear": fy, "selectedPeriods": periods,
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
}

func (s *ReportsSuite) addTypedTemplate(tenant, workflowID, name, taskType, reference string, offset int, direction string, orderIndex int) string {
	return s.postID(tenant, "/workflow-tasks", map[string]any{
		"workflowId": workflowID, "name": name, "taskType": taskType, "approvalRequired": false,
		"dueDateReference": reference, "dueDateOffsetValue": offset,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": direction, "orderIndex": orderIndex,
	})
}

func (s *ReportsSuite) start(tenant, workflowID string) {
	s.As(tenant).POST(s.T(), "/workflows/"+workflowID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
}

// instanceID finds the instance of a workflow by period + template name via
// the enriched report.
func (s *ReportsSuite) instanceID(tenant, workflowID, period, name string) string {
	var rows []reportRow
	s.decodePaginated(s.As(tenant).GET(s.T(), "/reports/task-instances?workflowId="+workflowID), &rows)
	for _, row := range rows {
		if row.PeriodCode == period && row.Name == name {
			return row.ID
		}
	}
	s.Require().Failf("instance not found", "%s %s of %s", period, name, workflowID)
	return ""
}

func (s *ReportsSuite) getJSON(tenant, path string, out any) {
	r := s.As(tenant).GET(s.T(), path)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), out)
}

// export decodes into a FRESH value each time: json.Unmarshal merges into an
// existing map, which would carry a previous dataset's keys over.
func (s *ReportsSuite) export(tenant, path string) exportData {
	var ex exportData
	s.getJSON(tenant, path, &ex)
	return ex
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

type fixture struct {
	alpha, beta               string // entity ids
	vat, cit                  string // obligation type ids
	alphaVAT, alphaCIT        string // workflow ids (FY2025)
	betaVAT, betaCIT          string // FY2025 / FY2026
	alphaVATM1Collect         string
	alphaVATM2Collect         string
	alphaCITM1Collect         string
	alphaCITM2Pay             string
	betaVATM2Collect          string
	betaCITM12Collect         string
	alphaVATName, betaVATName string
}

func (s *ReportsSuite) seedFixture(tenant string) fixture {
	f := fixture{alphaVATName: "Alpha VAT", betaVATName: "Beta VAT"}
	f.alpha = s.seedEntity(tenant, "Acme Alpha", "Germany")
	f.beta = s.seedEntity(tenant, "Acme Beta", "France")
	f.vat = s.seedObligationType(tenant, "VAT Return", "VAT-R", "VAT")
	f.cit = s.seedObligationType(tenant, "Corporate Tax", "CIT-R", "CIT")

	f.alphaVAT = s.seedWorkflowFor(tenant, f.alphaVATName, f.alpha, f.vat, "2025", []string{"M1", "M2"})
	s.addTypedTemplate(tenant, f.alphaVAT, "Collect", "preparation", "period_end", 2, "after", 0)
	s.addTypedTemplate(tenant, f.alphaVAT, "File", "submission", "filing_deadline", 5, "before", 1)

	f.alphaCIT = s.seedWorkflowFor(tenant, "Alpha CIT", f.alpha, f.cit, "2025", []string{"M1", "M2"})
	s.addTypedTemplate(tenant, f.alphaCIT, "Collect", "preparation", "period_end", 2, "after", 0)
	s.addTypedTemplate(tenant, f.alphaCIT, "Pay", "payment", "filing_deadline", 5, "before", 1)

	f.betaVAT = s.seedWorkflowFor(tenant, f.betaVATName, f.beta, f.vat, "2025", []string{"M1", "M2"})
	s.addTypedTemplate(tenant, f.betaVAT, "Collect", "preparation", "period_end", 2, "after", 0)
	s.addTypedTemplate(tenant, f.betaVAT, "File", "submission", "filing_deadline", 5, "before", 1)

	f.betaCIT = s.seedWorkflowFor(tenant, "Beta CIT", f.beta, f.cit, "2026", []string{"M12"})
	s.addTypedTemplate(tenant, f.betaCIT, "Collect", "preparation", "period_end", 2, "after", 0)

	for _, wf := range []string{f.alphaVAT, f.alphaCIT, f.betaVAT, f.betaCIT} {
		s.start(tenant, wf)
	}
	f.alphaVATM1Collect = s.instanceID(tenant, f.alphaVAT, "M1", "Collect")
	f.alphaVATM2Collect = s.instanceID(tenant, f.alphaVAT, "M2", "Collect")
	f.alphaCITM1Collect = s.instanceID(tenant, f.alphaCIT, "M1", "Collect")
	f.alphaCITM2Pay = s.instanceID(tenant, f.alphaCIT, "M2", "Pay")
	f.betaVATM2Collect = s.instanceID(tenant, f.betaVAT, "M2", "Collect")
	f.betaCITM12Collect = s.instanceID(tenant, f.betaCIT, "M12", "Collect")
	return f
}

// --- the test ---------------------------------------------------------------

func (s *ReportsSuite) TestComplianceReports() {
	tenant := s.InsertTenant("cr-a", "Compliance A").String()
	other := s.InsertTenant("cr-b", "Compliance B").String()
	f := s.seedFixture(tenant)

	// ---- heatmap before any completion: FY2025 cells overdue (red), M12 amber.
	var hm heatmapData
	s.getJSON(tenant, "/reports/compliance-heatmap", &hm)
	s.Require().Equal([]idLabel{{f.alpha, "Acme Alpha"}, {f.beta, "Acme Beta"}}, hm.Rows)
	s.Require().Equal([]idLabel{{"M1", "M1"}, {"M2", "M2"}, {"M12", "M12"}}, hm.Cols)
	s.Require().Len(hm.Cells, 5)
	s.Require().Equal(5, hm.Summary.TotalCells)
	s.Require().Equal(4, hm.Summary.Red)
	s.Require().Equal(1, hm.Summary.Amber)
	s.Require().Equal(0, hm.Summary.Green)
	c := hm.cell(f.alpha, "M1")
	s.Require().NotNil(c)
	s.Require().Equal("red", c.Status)
	s.Require().Equal(4, c.TotalTasks)
	s.Require().Equal(0, c.CompletedTasks)
	s.Require().Equal(4, c.OverdueTasks)
	s.Require().Equal(0, c.InProgressTasks)
	s.Require().Equal(sortedCopy([]string{f.alphaVAT, f.alphaCIT}), sortedCopy(c.WorkflowIDs))
	c = hm.cell(f.beta, "M12")
	s.Require().NotNil(c)
	s.Require().Equal("amber", c.Status)
	s.Require().Equal(1, c.TotalTasks)
	s.Require().Equal(0, c.OverdueTasks)
	s.Require().Equal([]string{f.betaCIT}, c.WorkflowIDs)
	// Cells arrive sorted by row label, then by the calendar (M2 before M12 —
	// never period-code text order).
	s.Require().Equal("Acme Alpha", hm.Cells[0].RowLabel)
	s.Require().Equal("M1", hm.Cells[0].ColID)
	s.Require().Equal("Acme Beta", hm.Cells[4].RowLabel)
	s.Require().Equal("M12", hm.Cells[4].ColID)

	// ---- completions + tax data (PUT /task-instances/{id}).
	put := func(id string, body map[string]any) {
		s.As(tenant).PUT(s.T(), "/task-instances/"+id, body).AssertStatus(s.T(), http.StatusOK)
	}
	// Late: completed today, deadline 2025-02-15. Carries VAT figures (string + number).
	put(f.alphaVATM1Collect, map[string]any{
		"status": "completed", "taxDataStatus": "final",
		"taxData": map[string]any{"outputVat": "1000", "inputVat": 400},
	})
	// On time: completed today, deadline 2027-01-15. CIT figures + engagement cost.
	put(f.betaCITM12Collect, map[string]any{
		"status":  "completed",
		"taxData": map[string]any{"taxLiability": 75, "engagementCost": "20"},
	})
	put(f.alphaCITM1Collect, map[string]any{
		"status":  "in_progress",
		"taxData": map[string]any{"taxLiability": 250, "taxableIncome": "1000"},
	})
	// Non-numeric → 0 (row still present: tax_data is non-empty, legacy rule).
	put(f.betaVATM2Collect, map[string]any{
		"status": "not_started", "taxData": map[string]any{"outputVat": "abc"},
	})
	// Penalty / interest figures → penaltyInterest text.
	put(f.alphaVATM2Collect, map[string]any{
		"status": "not_started", "taxData": map[string]any{"penaltyAmount": 50, "interest": "12.5"},
	})
	// Payment task's notes → penaltyInterest (no tax data).
	put(f.alphaCITM2Pay, map[string]any{"status": "not_started", "notes": "Late payment fee 3%"})

	// ---- heatmap after: late completion keeps Alpha×M1 red; Beta×M12 green.
	s.getJSON(tenant, "/reports/compliance-heatmap?year=all&entityId=all&obligationTypeId=all&viewMode=period", &hm)
	s.Require().Equal(5, hm.Summary.TotalCells)
	s.Require().Equal(4, hm.Summary.Red)
	s.Require().Equal(0, hm.Summary.Amber)
	s.Require().Equal(1, hm.Summary.Green)
	c = hm.cell(f.alpha, "M1")
	s.Require().Equal("red", c.Status)
	s.Require().Equal(4, c.TotalTasks)
	s.Require().Equal(1, c.CompletedTasks)
	s.Require().Equal(3, c.OverdueTasks)
	s.Require().Equal(1, c.InProgressTasks)
	c = hm.cell(f.beta, "M12")
	s.Require().Equal("green", c.Status)
	s.Require().Equal(1, c.CompletedTasks)
	s.Require().Equal(0, c.OverdueTasks)

	// tax-type view: columns are obligation types "<name> (<code>)".
	s.getJSON(tenant, "/reports/compliance-heatmap?viewMode=tax-type", &hm)
	s.Require().Equal([]idLabel{{f.alpha, "Acme Alpha"}, {f.beta, "Acme Beta"}}, hm.Rows)
	s.Require().Len(hm.Cols, 2)
	labels := map[string]string{}
	for _, col := range hm.Cols {
		labels[col.ID] = col.Label
	}
	s.Require().Equal(map[string]string{f.vat: "VAT Return (VAT-R)", f.cit: "Corporate Tax (CIT-R)"}, labels)
	s.Require().Len(hm.Cells, 4)
	c = hm.cell(f.alpha, f.vat)
	s.Require().NotNil(c)
	s.Require().Equal("red", c.Status)
	s.Require().Equal(4, c.TotalTasks)
	s.Require().Equal(1, c.CompletedTasks)
	s.Require().Equal(3, c.OverdueTasks)
	s.Require().Equal([]string{f.alphaVAT}, c.WorkflowIDs)
	c = hm.cell(f.beta, f.cit)
	s.Require().NotNil(c)
	s.Require().Equal("green", c.Status)
	s.Require().Equal(1, hm.Summary.Green)
	s.Require().Equal(3, hm.Summary.Red)

	// Filters.
	s.getJSON(tenant, "/reports/compliance-heatmap?year=2025", &hm)
	s.Require().Len(hm.Cells, 4)
	s.Require().Equal([]idLabel{{"M1", "M1"}, {"M2", "M2"}}, hm.Cols)
	s.getJSON(tenant, "/reports/compliance-heatmap?entityId="+f.beta, &hm)
	s.Require().Equal([]idLabel{{f.beta, "Acme Beta"}}, hm.Rows)
	s.Require().Len(hm.Cells, 3)
	s.getJSON(tenant, "/reports/compliance-heatmap?obligationTypeId="+f.cit+"&viewMode=tax-type", &hm)
	s.Require().Len(hm.Cells, 2)
	s.Require().Equal([]idLabel{{f.cit, "Corporate Tax (CIT-R)"}}, hm.Cols)
	s.getJSON(tenant, "/reports/compliance-heatmap?year=1999", &hm)
	s.Require().Empty(hm.Cells)
	s.Require().Empty(hm.Rows)
	s.Require().Equal(0, hm.Summary.TotalCells)

	// ---- compliance status.
	var cs complianceData
	s.getJSON(tenant, "/reports/compliance-status", &cs)
	s.Require().Equal(13, cs.TotalCount)
	s.Require().Len(cs.Rows, 13)
	s.Require().Equal(13, cs.Summary.Total)
	s.Require().Equal(1, cs.Summary.OnTime)
	s.Require().Equal(1, cs.Summary.Late)
	s.Require().Equal(11, cs.Summary.Missed)
	s.Require().Equal(0, cs.Summary.NotDue)
	// Sorted by entityName, obligationName, period.
	var order []string
	for _, row := range cs.Rows {
		order = append(order, row.EntityName+"|"+row.ObligationName+"|"+row.Period)
	}
	s.Require().Equal([]string{
		"Acme Alpha|Corporate Tax|M1", "Acme Alpha|Corporate Tax|M1", "Acme Alpha|Corporate Tax|M2", "Acme Alpha|Corporate Tax|M2",
		"Acme Alpha|VAT Return|M1", "Acme Alpha|VAT Return|M1", "Acme Alpha|VAT Return|M2", "Acme Alpha|VAT Return|M2",
		"Acme Beta|Corporate Tax|M12",
		"Acme Beta|VAT Return|M1", "Acme Beta|VAT Return|M1", "Acme Beta|VAT Return|M2", "Acme Beta|VAT Return|M2",
	}, order)
	byInstance := map[string]complianceRow{}
	for _, row := range cs.Rows {
		byInstance[row.TaskInstanceID] = row
	}
	late := byInstance[f.alphaVATM1Collect]
	s.Require().Equal("late", late.ComplianceStatus)
	s.Require().Equal("2025-02-15", late.FilingDeadline)
	s.Require().NotNil(late.FilingDate)
	s.Require().Regexp(instantRegex, *late.FilingDate)
	s.Require().Equal("Acme Alpha", late.EntityName)
	s.Require().Equal(f.alpha, late.EntityID)
	s.Require().Equal("VAT", late.TaxType)
	s.Require().Equal("VAT Return", late.ObligationName)
	s.Require().Equal("VAT-R", late.ObligationCode)
	s.Require().Equal(f.alphaVAT, late.WorkflowID)
	s.Require().Equal("", late.PenaltyInterest)
	onTime := byInstance[f.betaCITM12Collect]
	s.Require().Equal("on_time", onTime.ComplianceStatus)
	s.Require().Equal("2027-01-15", onTime.FilingDeadline)
	s.Require().NotNil(onTime.FilingDate)
	missed := byInstance[f.alphaCITM1Collect]
	s.Require().Equal("missed", missed.ComplianceStatus)
	s.Require().Nil(missed.FilingDate)
	s.Require().Equal("Penalty: 50, Interest: 12.5", byInstance[f.alphaVATM2Collect].PenaltyInterest)
	s.Require().Equal("Late payment fee 3%", byInstance[f.alphaCITM2Pay].PenaltyInterest)

	// Status filter: rows + totalCount describe the FILTERED set (paging
	// included); the summary keeps describing the whole classified set, so the
	// cards stay global while the table narrows.
	s.getJSON(tenant, "/reports/compliance-status?status=late", &cs)
	s.Require().Len(cs.Rows, 1)
	s.Require().Equal(f.alphaVATM1Collect, cs.Rows[0].TaskInstanceID)
	s.Require().Equal(1, cs.TotalCount)
	s.Require().Equal(13, cs.Summary.Total)
	s.Require().Equal(1, cs.Summary.OnTime)
	s.Require().Equal(1, cs.Summary.Late)
	s.Require().Equal(11, cs.Summary.Missed)
	s.Require().Equal(0, cs.Summary.NotDue)
	s.getJSON(tenant, "/reports/compliance-status?status=missed&limit=5&offset=10", &cs)
	s.Require().Len(cs.Rows, 1)
	s.Require().Equal(11, cs.TotalCount)
	s.Require().Equal(13, cs.Summary.Total)
	s.Require().Equal(11, cs.Summary.Missed)
	// The year / entity / obligation filters DO narrow the summary.
	s.getJSON(tenant, "/reports/compliance-status?status=all&year=2026", &cs)
	s.Require().Equal(1, cs.TotalCount)
	s.Require().Equal(1, cs.Summary.Total)
	s.Require().Equal(1, cs.Summary.OnTime)
	s.Require().Equal("M12", cs.Rows[0].Period)
	s.getJSON(tenant, "/reports/compliance-status?entityId="+f.beta+"&obligationTypeId="+f.vat, &cs)
	s.Require().Equal(4, cs.TotalCount)
	s.Require().Equal(4, cs.Summary.Total)
	s.Require().Equal(4, cs.Summary.Missed)
	s.getJSON(tenant, "/reports/compliance-status?status=not_due", &cs)
	s.Require().Empty(cs.Rows)
	s.Require().Equal(0, cs.TotalCount)
	s.Require().Equal(13, cs.Summary.Total)
	s.Require().Equal(0, cs.Summary.NotDue)

	// ---- tax financial.
	var tf financialData
	s.getJSON(tenant, "/reports/tax-financial", &tf)
	s.Require().Equal(5, tf.TotalCount)
	s.Require().Len(tf.Rows, 5)
	s.Require().Equal(5, tf.Summary.RecordCount)
	s.Require().Equal(financialRow{
		EntityName: "Acme Alpha", EntityID: f.alpha, Country: "Germany", TaxType: "CIT",
		ObligationName: "Corporate Tax", ObligationCode: "CIT-R", Period: "M1", FinancialYear: "2025",
		figures: figures{TaxableIncome: 1000, TaxLiability: 250, TotalAmount: 250},
	}, tf.Rows[0])
	s.Require().Equal(financialRow{
		EntityName: "Acme Alpha", EntityID: f.alpha, Country: "Germany", TaxType: "VAT",
		ObligationName: "VAT Return", ObligationCode: "VAT-R", Period: "M1", FinancialYear: "2025",
		figures: figures{OutputVat: 1000, InputVat: 400, NetVat: 600, TotalAmount: 600},
	}, tf.Rows[1])
	s.Require().Equal("M2", tf.Rows[2].Period) // penalty-only tax data → zero figures
	s.Require().Equal(figures{}, tf.Rows[2].figures)
	s.Require().Equal(financialRow{
		EntityName: "Acme Beta", EntityID: f.beta, Country: "France", TaxType: "CIT",
		ObligationName: "Corporate Tax", ObligationCode: "CIT-R", Period: "M12", FinancialYear: "2026",
		figures: figures{TaxLiability: 75, EngagementCost: 20, TotalAmount: 75},
	}, tf.Rows[3])
	s.Require().Equal("Acme Beta", tf.Rows[4].EntityName) // "abc" → 0
	s.Require().Equal("VAT", tf.Rows[4].TaxType)
	s.Require().Equal(figures{}, tf.Rows[4].figures)

	s.Require().Equal([]aggregatedGroup{
		{Key: f.alpha, Label: "Acme Alpha", Count: 3, figures: figures{
			OutputVat: 1000, InputVat: 400, NetVat: 600, TaxableIncome: 1000, TaxLiability: 250, TotalAmount: 850}},
		{Key: f.beta, Label: "Acme Beta", Count: 2, figures: figures{TaxLiability: 75, EngagementCost: 20, TotalAmount: 75}},
	}, tf.Aggregated)
	// Chart points follow the calendar (M2 before M12), never period-code text.
	s.Require().Equal([]chartPoint{
		{Period: "M1", OutputVat: 1000, InputVat: 400, NetVat: 600, TaxLiability: 250, TotalAmount: 850},
		{Period: "M2"},
		{Period: "M12", TaxLiability: 75, TotalAmount: 75},
	}, tf.ChartData)
	s.Require().Equal(float64(1000), tf.Summary.TotalOutputVat)
	s.Require().Equal(float64(400), tf.Summary.TotalInputVat)
	s.Require().Equal(float64(600), tf.Summary.TotalNetVat)
	s.Require().Equal(float64(1000), tf.Summary.TotalTaxableIncome)
	s.Require().Equal(float64(325), tf.Summary.TotalTaxLiability)
	s.Require().Equal(float64(0), tf.Summary.TotalWht)
	s.Require().Equal(float64(20), tf.Summary.TotalEngagementCost)
	s.Require().Equal(float64(925), tf.Summary.TotalAmount)

	// groupBy variants.
	s.getJSON(tenant, "/reports/tax-financial?groupBy=taxType", &tf)
	s.Require().Equal([]aggregatedGroup{
		{Key: "CIT", Label: "CIT", Count: 2, figures: figures{TaxableIncome: 1000, TaxLiability: 325, EngagementCost: 20, TotalAmount: 325}},
		{Key: "VAT", Label: "VAT", Count: 3, figures: figures{OutputVat: 1000, InputVat: 400, NetVat: 600, TotalAmount: 600}},
	}, tf.Aggregated)
	s.getJSON(tenant, "/reports/tax-financial?groupBy=country", &tf)
	s.Require().Len(tf.Aggregated, 2)
	s.Require().Equal("France", tf.Aggregated[0].Label)
	s.Require().Equal(2, tf.Aggregated[0].Count)
	s.Require().Equal("Germany", tf.Aggregated[1].Label)
	s.Require().Equal(3, tf.Aggregated[1].Count)
	s.getJSON(tenant, "/reports/tax-financial?groupBy=period", &tf)
	s.Require().Len(tf.Aggregated, 3)
	s.Require().Equal([]string{"M1", "M2", "M12"}, []string{tf.Aggregated[0].Key, tf.Aggregated[1].Key, tf.Aggregated[2].Key})
	s.getJSON(tenant, "/reports/tax-financial?groupBy=obligation", &tf)
	s.Require().Equal([]string{f.cit, f.vat}, []string{tf.Aggregated[0].Key, tf.Aggregated[1].Key})
	s.Require().Equal("Corporate Tax", tf.Aggregated[0].Label)

	// Filters + paging: the page is capped, aggregates stay exact.
	s.getJSON(tenant, "/reports/tax-financial?year=2025&entityId=all", &tf)
	s.Require().Equal(4, tf.TotalCount)
	s.Require().Equal(4, tf.Summary.RecordCount)
	s.Require().Equal(float64(250), tf.Summary.TotalTaxLiability)
	s.getJSON(tenant, "/reports/tax-financial?limit=2&offset=4", &tf)
	s.Require().Len(tf.Rows, 1)
	s.Require().Equal("M2", tf.Rows[0].Period)
	s.Require().Equal(5, tf.TotalCount)
	s.Require().Equal(5, tf.Summary.RecordCount)
	s.Require().Len(tf.Aggregated, 2)
	s.getJSON(tenant, "/reports/tax-financial?obligationTypeId="+f.vat, &tf)
	s.Require().Equal(3, tf.TotalCount)
	s.Require().Equal(float64(600), tf.Summary.TotalAmount)

	// ---- export raw.
	var ex exportData
	today := time.Now().UTC()
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }

	ex = s.export(tenant, "/reports/export-raw?dataset=workflows")
	s.Require().Equal("workflows", ex.Dataset)
	s.Require().Equal(4, ex.TotalCount)
	s.Require().Len(ex.Rows, 4)
	for _, row := range ex.Rows {
		s.Require().Equal(sortedCopy(workflowColumns), keysOf(row))
		s.Require().Equal("recurring", row["category"])
		s.Require().Equal("active", row["status"])
		s.Require().Equal("", row["projectType"])
		s.Require().Equal("monthly", row["periodicity"])
		s.Require().Equal(false, row["tasksSequential"])
		s.Require().Regexp(instantRegex, row["createdAt"])
	}
	wfByName := map[string]map[string]any{}
	for _, row := range ex.Rows {
		wfByName[row["workflowName"].(string)] = row
	}
	s.Require().Equal("Acme Beta", wfByName["Beta CIT"]["entityName"])
	s.Require().Equal("France", wfByName["Beta CIT"]["country"])
	s.Require().Equal("Corporate Tax", wfByName["Beta CIT"]["obligationName"])
	s.Require().Equal("CIT-R", wfByName["Beta CIT"]["obligationCode"])
	s.Require().Equal("CIT", wfByName["Beta CIT"]["taxType"])
	s.Require().Equal("2026", wfByName["Beta CIT"]["financialYear"])
	ex = s.export(tenant, "/reports/export-raw?dataset=workflows&category=project")
	s.Require().Equal(0, ex.TotalCount)
	s.Require().Empty(ex.Rows)
	ex = s.export(tenant, "/reports/export-raw?dataset=workflows&category=recurring&entityId="+f.beta)
	s.Require().Equal(2, ex.TotalCount)
	// Date window on workflows.created_at (UTC calendar date, inclusive).
	ex = s.export(tenant, "/reports/export-raw?dataset=workflows&dateFrom="+day(-1)+"&dateTo="+day(1))
	s.Require().Equal(4, ex.TotalCount)
	ex = s.export(tenant, "/reports/export-raw?dataset=workflows&dateTo="+day(-2))
	s.Require().Equal(0, ex.TotalCount)
	ex = s.export(tenant, "/reports/export-raw?dataset=workflows&dateFrom="+day(2))
	s.Require().Equal(0, ex.TotalCount)
	ex = s.export(tenant, "/reports/export-raw?dataset=workflows&limit=3&offset=3")
	s.Require().Len(ex.Rows, 1)
	s.Require().Equal(4, ex.TotalCount)

	// tasks (the default dataset).
	ex = s.export(tenant, "/reports/export-raw")
	s.Require().Equal("tasks", ex.Dataset)
	s.Require().Equal(13, ex.TotalCount)
	s.Require().Len(ex.Rows, 13)
	for _, row := range ex.Rows {
		s.Require().Equal(sortedCopy(taskColumns), keysOf(row))
		s.Require().Regexp(dateOnly, row["dueDate"])
		s.Require().Regexp(dateOnly, row["filingDeadline"])
		s.Require().Equal("", row["assigneeName"])
		s.Require().Equal(false, row["approvalRequired"])
		s.Require().Equal("recurring", row["workflowCategory"])
	}
	first := ex.Rows[0] // ordered by due date: Collect M1 (2025-02-02)
	s.Require().Equal("Collect", first["taskName"])
	s.Require().Equal("2025-02-02", first["dueDate"])
	s.Require().Equal("2025-02-15", first["filingDeadline"])
	completedRows, payRows := 0, 0
	for _, row := range ex.Rows {
		if row["status"] == "completed" {
			completedRows++
			s.Require().Regexp(instantRegex, row["completedAt"])
		} else {
			s.Require().Equal("", row["completedAt"])
		}
		if row["taskType"] == "payment" {
			payRows++
		}
	}
	s.Require().Equal(2, completedRows)
	s.Require().Equal(2, payRows)
	// Date window on due_date: March 2025 = the M2 instances of three workflows.
	ex = s.export(tenant, "/reports/export-raw?dataset=tasks&dateFrom=2025-03-01&dateTo=2025-03-31")
	s.Require().Equal(6, ex.TotalCount)
	ex = s.export(tenant, "/reports/export-raw?dataset=tasks&dateFrom=2025-03-10&dateTo=2025-03-10")
	s.Require().Equal(3, ex.TotalCount) // File / Pay M2 due 2025-03-10 (inclusive both ends)
	ex = s.export(tenant, "/reports/export-raw?dataset=tasks&obligationTypeId="+f.cit)
	s.Require().Equal(5, ex.TotalCount)
	ex = s.export(tenant, "/reports/export-raw?dataset=tasks&category=all&limit=5&offset=10")
	s.Require().Len(ex.Rows, 3)
	s.Require().Equal(13, ex.TotalCount)

	// tax-data: instances carrying tax_data, figures as numbers.
	ex = s.export(tenant, "/reports/export-raw?dataset=tax-data")
	s.Require().Equal("tax-data", ex.Dataset)
	s.Require().Equal(5, ex.TotalCount)
	s.Require().Len(ex.Rows, 5)
	tdByKey := map[string]map[string]any{}
	for _, row := range ex.Rows {
		s.Require().Equal(sortedCopy(taxDataColumns), keysOf(row))
		tdByKey[row["workflowName"].(string)+"|"+row["periodCode"].(string)] = row
	}
	r := tdByKey[f.alphaVATName+"|M1"]
	s.Require().Equal(float64(1000), r["outputVat"])
	s.Require().Equal(float64(400), r["inputVat"])
	s.Require().Equal(float64(0), r["netVat"]) // raw: no derivation
	s.Require().Equal("final", r["taxDataStatus"])
	s.Require().Equal("Acme Alpha", r["entityName"])
	s.Require().Equal("Germany", r["country"])
	s.Require().Equal("VAT", r["taxType"])
	r = tdByKey[f.alphaVATName+"|M2"]
	s.Require().Equal(float64(50), r["penaltyAmount"])
	s.Require().Equal(12.5, r["interestAmount"])
	r = tdByKey[f.betaVATName+"|M2"]
	s.Require().Equal(float64(0), r["outputVat"])
	r = tdByKey["Beta CIT|M12"]
	s.Require().Equal(float64(75), r["taxLiability"])
	s.Require().Equal(float64(20), r["engagementCost"])
	ex = s.export(tenant, "/reports/export-raw?dataset=tax-data&entityId="+f.alpha+"&limit=2&offset=1")
	s.Require().Len(ex.Rows, 2)
	s.Require().Equal(3, ex.TotalCount)

	// ---- validation.
	bad := func(path string) {
		s.As(tenant).GET(s.T(), path).AssertStatus(s.T(), http.StatusBadRequest)
	}
	bad("/reports/compliance-heatmap?viewMode=bogus")
	bad("/reports/compliance-heatmap?entityId=nope")
	bad("/reports/compliance-status?status=bogus")
	bad("/reports/compliance-status?limit=5001")
	bad("/reports/tax-financial?groupBy=bogus")
	bad("/reports/tax-financial?obligationTypeId=nope")
	bad("/reports/export-raw?dataset=bogus")
	bad("/reports/export-raw?category=bogus")
	bad("/reports/export-raw?dateFrom=nope")
	bad("/reports/export-raw?limit=5001")

	// ---- authz: any tenant member (task:read) may read; anonymous is rejected.
	for _, path := range []string{
		"/reports/compliance-heatmap", "/reports/compliance-status", "/reports/tax-financial", "/reports/export-raw",
	} {
		s.AsRole(tenant, "viewer").GET(s.T(), path).AssertStatus(s.T(), http.StatusOK)
		s.Client.External().GET(s.T(), path).AssertStatus(s.T(), http.StatusUnauthorized)
	}

	// ---- RLS: the other tenant sees empty reports, never null collections.
	s.getJSON(other, "/reports/compliance-heatmap", &hm)
	s.Require().NotNil(hm.Rows)
	s.Require().Empty(hm.Rows)
	s.Require().NotNil(hm.Cells)
	s.Require().Empty(hm.Cells)
	s.Require().Equal(0, hm.Summary.TotalCells)
	s.getJSON(other, "/reports/compliance-status", &cs)
	s.Require().NotNil(cs.Rows)
	s.Require().Empty(cs.Rows)
	s.Require().Equal(0, cs.TotalCount)
	s.Require().Equal(0, cs.Summary.Total)
	s.getJSON(other, "/reports/tax-financial", &tf)
	s.Require().NotNil(tf.Rows)
	s.Require().Empty(tf.Rows)
	s.Require().NotNil(tf.Aggregated)
	s.Require().NotNil(tf.ChartData)
	s.Require().Equal(0, tf.TotalCount)
	s.Require().Equal(0, tf.Summary.RecordCount)
	s.Require().Equal(float64(0), tf.Summary.TotalAmount)
	for _, ds := range []string{"workflows", "tasks", "tax-data"} {
		ex = s.export(other, "/reports/export-raw?dataset="+ds)
		s.Require().NotNil(ex.Rows)
		s.Require().Empty(ex.Rows)
		s.Require().Equal(0, ex.TotalCount)
	}
}
