package datatemplates_test

import "net/http"

type financialFigures struct {
	OutputVat    float64 `json:"outputVat"`
	InputVat     float64 `json:"inputVat"`
	NetVat       float64 `json:"netVat"`
	TaxLiability float64 `json:"taxLiability"`
	WhtAmount    float64 `json:"whtAmount"`
	TotalAmount  float64 `json:"totalAmount"`
}

type financialReport struct {
	Rows []struct {
		Period  string `json:"period"`
		TaxType string `json:"taxType"`
		financialFigures
	} `json:"rows"`
	Summary struct {
		TotalOutputVat    float64 `json:"totalOutputVat"`
		TotalInputVat     float64 `json:"totalInputVat"`
		TotalNetVat       float64 `json:"totalNetVat"`
		TotalTaxLiability float64 `json:"totalTaxLiability"`
		TotalWht          float64 `json:"totalWht"`
		TotalAmount       float64 `json:"totalAmount"`
		RecordCount       int     `json:"recordCount"`
	} `json:"summary"`
}

// TestPredefinedTemplatesFeedTheFinancialReport: figures recorded under a
// predefined template's fields (validated strictly against it) are exactly
// what the tax-financial report and the raw tax-data export read — the
// template ids ARE the canonical tax-data keys, so a template-bound instance
// contributes real figures instead of a zero row.
func (s *DataTemplatesSuite) TestPredefinedTemplatesFeedTheFinancialReport() {
	tenant := s.InsertTenant("dt-rep", "Report Tenant").String()
	templates := map[string]string{}
	for _, tpl := range s.seedPredefined(tenant) {
		templates[tpl.TemplateType] = tpl.ID
	}

	var entity, cit, wht idOnly
	r := s.As(tenant).POST(s.T(), "/entities", map[string]any{
		"name": "Acme Figures", "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)
	r = s.As(tenant).POST(s.T(), "/obligation-types", map[string]any{"name": "Corporate Tax", "code": "CIT-F", "template": "CIT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &cit)
	r = s.As(tenant).POST(s.T(), "/obligation-types", map[string]any{"name": "Withholding", "code": "WHT-F", "template": "WHT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wht)

	// One recurring workflow per tax type, its task bound to that type's
	// predefined template; VAT reuses the suite's seeder.
	bind := func(name, obTypeID, templateID string) string {
		var wf, task idOnly
		r := s.As(tenant).POST(s.T(), "/workflows", map[string]any{
			"name": name, "workflowCategory": "recurring", "entityId": entity.ID, "obligationTypeId": obTypeID,
			"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
			"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
		})
		r.AssertStatus(s.T(), http.StatusCreated)
		r.DecodeData(s.T(), &wf)
		r = s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
			"workflowId": wf.ID, "name": "Prepare " + name, "taskType": "preparation", "dataTemplateId": templateID,
		})
		r.AssertStatus(s.T(), http.StatusCreated)
		r.DecodeData(s.T(), &task)
		s.As(tenant).POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
		var instances []taskInstance
		s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &instances)
		s.Require().Len(instances, 1)
		s.Require().Equal(templateID, *instances[0].DataTemplateID)
		return instances[0].ID
	}
	vatWf := s.seedRecurringWorkflow(tenant, "VAT")
	var vatTask idOnly
	r = s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": vatWf, "name": "Prepare VAT", "taskType": "preparation", "dataTemplateId": templates["VAT"],
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &vatTask)
	s.As(tenant).POST(s.T(), "/workflows/"+vatWf+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
	var vatInstances []taskInstance
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+vatWf).DecodeData(s.T(), &vatInstances)
	vatID := vatInstances[0].ID
	citID := bind("CIT", cit.ID, templates["CIT"])
	whtID := bind("WHT", wht.ID, templates["WHT"])

	put := func(id string, taxData map[string]any) {
		s.As(tenant).PUT(s.T(), "/task-instances/"+id, map[string]any{
			"status": "in_progress", "taxDataStatus": "final", "taxData": taxData,
		}).AssertStatus(s.T(), http.StatusOK)
	}
	// The template validates the record (a fixture-era id is unknown), then the report reads it.
	s.As(tenant).PUT(s.T(), "/task-instances/"+vatID, map[string]any{
		"status": "in_progress", "taxData": map[string]any{"f-vat-output": 1000},
	}).AssertStatus(s.T(), http.StatusBadRequest)
	put(vatID, map[string]any{"salesTotal": 5000, "outputVat": 1000, "inputVat": 400})
	put(citID, map[string]any{"profitBeforeTax": 2000, "taxableIncome": 1800, "taxRate": 25, "taxLiability": 450})
	put(whtID, map[string]any{"whtBase": 3000, "whtRate": 10, "whtAmount": 300})

	var tf financialReport
	r = s.As(tenant).GET(s.T(), "/reports/tax-financial?groupBy=taxType")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &tf)
	s.Require().Equal(3, tf.Summary.RecordCount)
	s.Require().Equal(float64(1000), tf.Summary.TotalOutputVat)
	s.Require().Equal(float64(400), tf.Summary.TotalInputVat)
	s.Require().Equal(float64(600), tf.Summary.TotalNetVat, "derived output − input when netVat is not recorded")
	s.Require().Equal(float64(450), tf.Summary.TotalTaxLiability)
	s.Require().Equal(float64(300), tf.Summary.TotalWht)
	s.Require().Equal(float64(1350), tf.Summary.TotalAmount)
	byType := map[string]financialFigures{}
	for _, row := range tf.Rows {
		byType[row.TaxType] = row.financialFigures
	}
	s.Require().Equal(financialFigures{OutputVat: 1000, InputVat: 400, NetVat: 600, TotalAmount: 600}, byType["VAT"])
	s.Require().Equal(financialFigures{TaxLiability: 450, TotalAmount: 450}, byType["CIT"])
	s.Require().Equal(financialFigures{WhtAmount: 300, TotalAmount: 300}, byType["WHT"])

	// The raw export reads the same keys.
	var ex struct {
		Rows       []map[string]any `json:"rows"`
		TotalCount int              `json:"totalCount"`
	}
	r = s.As(tenant).GET(s.T(), "/reports/export-raw?dataset=tax-data")
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &ex)
	s.Require().Equal(3, ex.TotalCount)
	exported := map[string]map[string]any{}
	for _, row := range ex.Rows {
		exported[row["taxType"].(string)] = row
	}
	s.Require().Equal(float64(1000), exported["VAT"]["outputVat"])
	s.Require().Equal(float64(450), exported["CIT"]["taxLiability"])
	s.Require().Equal(float64(300), exported["WHT"]["whtAmount"])
}
