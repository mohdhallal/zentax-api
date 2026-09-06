package datatemplates_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// DataTemplatesSuite proves the data-templates module + the tax-data
// authority (ADR-0001) end to end against live Postgres: predefined seeding is
// idempotent, custom CRUD with strict field validation, predefined rows are
// immutable, a template attached to a workflow task is inherited by the
// generated instances and governs their tax data (types, ranges, decimals,
// mandatory-on-final, mandatory-on-submit), an in-use template cannot be
// deleted, tenant isolation, role gate, audit entries.
type DataTemplatesSuite struct {
	acceptance.Suite
}

func TestDataTemplatesSuite(t *testing.T) {
	suite.Run(t, new(DataTemplatesSuite))
}

type numericValidation struct {
	Min              *float64 `json:"min"`
	Max              *float64 `json:"max"`
	AllowDecimals    bool     `json:"allowDecimals"`
	DecimalPlaces    *int     `json:"decimalPlaces"`
	FormatAsCurrency bool     `json:"formatAsCurrency"`
}

type field struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	FieldType         string             `json:"fieldType"`
	Mandatory         bool               `json:"mandatory"`
	Description       *string            `json:"description"`
	NumericValidation *numericValidation `json:"numericValidation"`
}

type dataTemplate struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	TemplateType string  `json:"templateType"`
	Category     string  `json:"category"`
	Description  *string `json:"description"`
	Fields       []field `json:"fields"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

type taskInstance struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	DataTemplateID *string        `json:"dataTemplateId"`
	TaxData        map[string]any `json:"taxData"`
	TaxDataStatus  string         `json:"taxDataStatus"`
}

type idOnly struct {
	ID string `json:"id"`
}

func (s *DataTemplatesSuite) seedPredefined(tenant string) []dataTemplate {
	var list []dataTemplate
	r := s.As(tenant).POST(s.T(), "/data-templates/predefined", nil)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &list)
	return list
}

func (s *DataTemplatesSuite) list(tenant, query string) []dataTemplate {
	var list []dataTemplate
	r := s.As(tenant).GET(s.T(), "/data-templates"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &list)
	return list
}

func customBody(name string, fields []map[string]any) map[string]any {
	return map[string]any{
		"name": name, "templateType": "Custom", "description": "tenant-specific figures",
		"fields": fields,
	}
}

func numericField(id, name string, mandatory bool, nv map[string]any) map[string]any {
	f := map[string]any{"id": id, "name": name, "fieldType": "numeric", "mandatory": mandatory}
	if nv != nil {
		f["numericValidation"] = nv
	}
	return f
}

// seedRecurringWorkflow builds entity -> obligation type -> recurring workflow
// (monthly, FY2025, one period) and returns the workflow id.
func (s *DataTemplatesSuite) seedRecurringWorkflow(tenant, name string) string {
	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), path, body)
	}
	var entity, obType, wf idOnly
	r := post("/entities", map[string]any{
		"name": "Acme " + name, "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = post("/obligation-types", map[string]any{"name": "VAT " + name, "code": "VAT-" + name, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = post("/workflows", map[string]any{
		"name": name, "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025",
		"selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)
	return wf.ID
}

// errorMessage decodes the error envelope's message (the raw body JSON-escapes
// quotes and "<"/">" — compare the decoded text).
func errorMessage(s *DataTemplatesSuite, r *acceptance.TestResponse) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(r.BodyString()), &env))
	return env.Error.Message
}

func (s *DataTemplatesSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

// TestPredefinedSeedingAndListing: seeding twice yields the same 3 rows
// (ids stable), the list is name-sorted and filterable.
func (s *DataTemplatesSuite) TestPredefinedSeedingAndListing() {
	tenant := s.InsertTenant("dt-seed", "Seed Tenant").String()

	first := s.seedPredefined(tenant)
	s.Require().Len(first, 3)
	s.Require().Equal([]string{"Corporate Income Tax", "VAT Return", "Withholding Tax"},
		[]string{first[0].Name, first[1].Name, first[2].Name})
	for _, t := range first {
		s.Require().Equal("predefined", t.Category)
		s.Require().NotEmpty(t.Fields)
		s.Require().NotNil(t.Description)
	}

	second := s.seedPredefined(tenant)
	s.Require().Len(second, 3)
	for i := range first {
		s.Require().Equal(first[i].ID, second[i].ID, "seeding must be idempotent")
	}

	// Field ids are the canonical tax-data keys; VAT currency validation, CIT rate is not a currency.
	vat := first[1]
	s.Require().Equal("VAT", vat.TemplateType)
	s.Require().Equal("Standard VAT/GST return figures", *vat.Description)
	s.Require().Len(vat.Fields, 4)
	s.Require().Equal("salesTotal", vat.Fields[0].ID)
	s.Require().Equal([]string{"salesTotal", "outputVat", "inputVat", "netVat"},
		[]string{vat.Fields[0].ID, vat.Fields[1].ID, vat.Fields[2].ID, vat.Fields[3].ID})
	s.Require().Equal("Total Sales (net)", vat.Fields[0].Name)
	s.Require().True(vat.Fields[0].Mandatory)
	s.Require().NotNil(vat.Fields[0].NumericValidation)
	s.Require().True(vat.Fields[0].NumericValidation.AllowDecimals)
	s.Require().True(vat.Fields[0].NumericValidation.FormatAsCurrency)
	s.Require().Equal(2, *vat.Fields[0].NumericValidation.DecimalPlaces)
	s.Require().Nil(vat.Fields[0].NumericValidation.Min)
	s.Require().False(vat.Fields[3].Mandatory)
	cit := first[0]
	s.Require().Equal("taxRate", cit.Fields[3].ID)
	s.Require().Equal("taxLiability", cit.Fields[4].ID)
	s.Require().False(cit.Fields[3].NumericValidation.FormatAsCurrency)
	s.Require().Equal("whtAmount", first[2].Fields[2].ID)

	// List + filters (name-sorted, unpaginated).
	all := s.list(tenant, "")
	s.Require().Len(all, 3)
	s.Require().Len(s.list(tenant, "?templateType=VAT"), 1)
	s.Require().Len(s.list(tenant, "?category=custom"), 0)
	s.Require().Len(s.list(tenant, "?category=predefined"), 3)
	s.As(tenant).GET(s.T(), "/data-templates?templateType=GST").AssertStatus(s.T(), http.StatusBadRequest)

	// GET by id.
	var one dataTemplate
	r := s.As(tenant).GET(s.T(), "/data-templates/"+vat.ID)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &one)
	s.Require().Equal(vat, one)

	// Every role reads; only the write roles seed.
	s.AsRole(tenant, "viewer").GET(s.T(), "/data-templates").AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "viewer").POST(s.T(), "/data-templates/predefined", nil).AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "preparer").POST(s.T(), "/data-templates/predefined", nil).AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "manager").POST(s.T(), "/data-templates/predefined", nil).AssertStatus(s.T(), http.StatusOK)

	// Exactly one seeding audit entry (the no-op second call records nothing).
	var actions []string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&actions, `SELECT action FROM audit_log WHERE action LIKE 'data_template.%' ORDER BY seq`))
	})
	s.Require().Equal([]string{"data_template.predefined_seeded"}, actions)
	var inserted string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&inserted, `SELECT details->>'inserted' FROM audit_log WHERE action = 'data_template.predefined_seeded'`))
	})
	s.Require().Equal("3", inserted)
}

// TestCustomTemplateCRUD: strict field validation, category never accepted,
// duplicate name 409, PUT/DELETE on custom OK and 403 on predefined.
func (s *DataTemplatesSuite) TestCustomTemplateCRUD() {
	tenant := s.InsertTenant("dt-crud", "CRUD Tenant").String()
	post := func(body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), "/data-templates", body)
	}
	goodFields := []map[string]any{
		numericField("amount", "Amount", true, map[string]any{"min": 0, "decimalPlaces": 2, "formatAsCurrency": true}),
		{"id": "note", "name": "Note", "fieldType": "text", "mandatory": false},
	}

	// Validation errors → 400.
	post(customBody("No fields", []map[string]any{})).AssertStatus(s.T(), http.StatusBadRequest)
	post(customBody("Dup ids", []map[string]any{
		{"id": "x", "name": "X", "fieldType": "text"}, {"id": "x", "name": "Y", "fieldType": "text"},
	})).AssertStatus(s.T(), http.StatusBadRequest)
	r := post(customBody("NV on text", []map[string]any{
		{"id": "x", "name": "X", "fieldType": "text", "numericValidation": map[string]any{"min": 1}},
	}))
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "only allowed on numeric fields")
	post(customBody("Min gt max", []map[string]any{
		numericField("x", "X", false, map[string]any{"min": 10, "max": 1}),
	})).AssertStatus(s.T(), http.StatusBadRequest)
	post(customBody("Bad type", []map[string]any{{"id": "x", "name": "X", "fieldType": "money"}})).AssertStatus(s.T(), http.StatusBadRequest)
	// category is not part of the create contract (unknown field).
	body := customBody("Sneaky", goodFields)
	body["category"] = "predefined"
	post(body).AssertStatus(s.T(), http.StatusBadRequest)

	// Create → 201, category custom, allowDecimals defaulted to true, field order kept.
	var created dataTemplate
	r = post(customBody("Local Levy", goodFields))
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &created)
	s.Require().Equal("custom", created.Category)
	s.Require().Equal("Custom", created.TemplateType)
	s.Require().Equal([]string{"amount", "note"}, []string{created.Fields[0].ID, created.Fields[1].ID})
	s.Require().True(created.Fields[0].NumericValidation.AllowDecimals)
	s.Require().Equal(2, *created.Fields[0].NumericValidation.DecimalPlaces)
	s.Require().Equal(0.0, *created.Fields[0].NumericValidation.Min)
	s.Require().Nil(created.Fields[1].NumericValidation)

	// Duplicate name → 409 (case-sensitive exact match on the unique key).
	post(customBody("Local Levy", goodFields)).AssertStatus(s.T(), http.StatusConflict)

	// PUT replaces.
	var updated dataTemplate
	r = s.As(tenant).PUT(s.T(), "/data-templates/"+created.ID, map[string]any{
		"name": "Local Levy v2", "templateType": "WHT",
		"fields": []map[string]any{
			numericField("count", "Count", true, map[string]any{"allowDecimals": false}),
		},
	})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &updated)
	s.Require().Equal("Local Levy v2", updated.Name)
	s.Require().Equal("WHT", updated.TemplateType)
	s.Require().Nil(updated.Description)
	s.Require().Len(updated.Fields, 1)
	s.Require().False(updated.Fields[0].NumericValidation.AllowDecimals)

	// Predefined rows are immutable.
	pre := s.seedPredefined(tenant)[1]
	s.As(tenant).PUT(s.T(), "/data-templates/"+pre.ID, map[string]any{
		"name": "Hacked", "templateType": "VAT", "fields": goodFields,
	}).AssertStatus(s.T(), http.StatusForbidden)
	r = s.As(tenant).DELETE(s.T(), "/data-templates/"+pre.ID)
	r.AssertStatus(s.T(), http.StatusForbidden)
	s.Require().Contains(r.BodyString(), "predefined templates are immutable")
	// A custom template cannot take a predefined name either (unique per tenant).
	post(customBody("VAT Return", goodFields)).AssertStatus(s.T(), http.StatusConflict)

	// DELETE custom → 204, then 404.
	s.As(tenant).DELETE(s.T(), "/data-templates/"+created.ID).AssertStatus(s.T(), http.StatusNoContent)
	s.As(tenant).GET(s.T(), "/data-templates/"+created.ID).AssertStatus(s.T(), http.StatusNotFound)
	s.Require().Len(s.list(tenant, "?category=custom"), 0)

	// Role gate: viewer cannot create.
	s.AsRole(tenant, "viewer").POST(s.T(), "/data-templates", customBody("Nope", goodFields)).AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "preparer").POST(s.T(), "/data-templates", customBody("Nope", goodFields)).AssertStatus(s.T(), http.StatusForbidden)

	// Audit trail of the custom lifecycle.
	var actions []string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&actions,
			`SELECT action FROM audit_log WHERE resource_id = $1 ORDER BY seq`, created.ID))
	})
	s.Require().Equal([]string{"data_template.created", "data_template.updated", "data_template.deleted"}, actions)
	var details struct {
		Fields       string `db:"fields"`
		TemplateType string `db:"template_type"`
	}
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&details,
			`SELECT details->>'fields' AS fields, details->>'templateType' AS template_type
			 FROM audit_log WHERE resource_id = $1 AND action = 'data_template.created'`, created.ID))
	})
	s.Require().Equal("2", details.Fields)
	s.Require().Equal("Custom", details.TemplateType)
}

// TestTaxDataAuthority: a template attached to a workflow task is inherited by
// the generated instance and the server validates tax data against it (PUT +
// submit), and an in-use template cannot be deleted.
func (s *DataTemplatesSuite) TestTaxDataAuthority() {
	tenant := s.InsertTenant("dt-tax", "Tax Data Tenant").String()
	other := s.InsertTenant("dt-tax-b", "Other Tenant").String()

	// A custom template with every rule the validator enforces.
	var tpl dataTemplate
	r := s.As(tenant).POST(s.T(), "/data-templates", customBody("VAT figures", []map[string]any{
		numericField("sales", "Total Sales (net)", true, map[string]any{"min": 0, "decimalPlaces": 2, "formatAsCurrency": true}),
		numericField("invoices", "Invoice Count", false, map[string]any{"allowDecimals": false}),
		numericField("rate", "Rate (%)", true, map[string]any{"min": 0, "max": 100}),
		{"id": "filed", "name": "Filing Date", "fieldType": "date"},
		{"id": "nilReturn", "name": "Nil Return", "fieldType": "boolean"},
		{"id": "note", "name": "Note", "fieldType": "text"},
		{"id": "evidence", "name": "Evidence", "fieldType": "file"},
	}))
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &tpl)

	// The other tenant cannot see or use it.
	s.As(other).GET(s.T(), "/data-templates/"+tpl.ID).AssertStatus(s.T(), http.StatusNotFound)
	s.As(other).DELETE(s.T(), "/data-templates/"+tpl.ID).AssertStatus(s.T(), http.StatusNotFound)
	s.Require().Empty(s.list(other, ""))

	// Attach it to a workflow task (approval required so submit is exercised).
	wfID := s.seedRecurringWorkflow(tenant, "VAT")
	var task idOnly
	r = s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wfID, "name": "Prepare VAT", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
		"dataTemplateId": tpl.ID,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &task)

	// A template of another tenant on a workflow task is refused (composite FK).
	otherWf := s.seedRecurringWorkflow(other, "Other VAT")
	r = s.As(other).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": otherWf, "name": "Prepare", "taskType": "preparation",
		"dataTemplateId": tpl.ID,
	})
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Contains(r.BodyString(), "data template not found in this tenant")

	// Start → the instance carries the template.
	s.As(tenant).POST(s.T(), "/workflows/"+wfID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
	var instances []taskInstance
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wfID).DecodeData(s.T(), &instances)
	s.Require().Len(instances, 1)
	s.Require().NotNil(instances[0].DataTemplateID)
	s.Require().Equal(tpl.ID, *instances[0].DataTemplateID)
	id := instances[0].ID

	put := func(body map[string]any) *acceptance.TestResponse {
		if _, ok := body["status"]; !ok {
			body["status"] = "in_progress"
		}
		return s.As(tenant).PUT(s.T(), "/task-instances/"+id, body)
	}
	expect400 := func(body map[string]any, msg string) {
		r := put(body)
		r.AssertStatus(s.T(), http.StatusBadRequest)
		s.Require().Contains(errorMessage(s, r), msg)
	}

	// Server-side tax-data rules (ADR-0001).
	expect400(map[string]any{"taxData": map[string]any{"bogus": 1}}, `tax data field "bogus" is not in the template`)
	expect400(map[string]any{"taxData": map[string]any{"sales": "5000"}}, `must be a number`)
	expect400(map[string]any{"taxData": map[string]any{"invoices": 2.5}}, `must be a whole number`)
	expect400(map[string]any{"taxData": map[string]any{"rate": 101}}, `must be <= 100`)
	expect400(map[string]any{"taxData": map[string]any{"sales": -1}}, `must be >= 0`)
	expect400(map[string]any{"taxData": map[string]any{"sales": 1.005}}, `at most 2 decimal places`)
	expect400(map[string]any{"taxData": map[string]any{"filed": "31/01/2025"}}, `must be a YYYY-MM-DD date`)
	expect400(map[string]any{"taxData": map[string]any{"nilReturn": "yes"}}, `must be a boolean`)
	expect400(map[string]any{"taxData": map[string]any{"note": 42}}, `must be a string`)
	expect400(map[string]any{"taxData": map[string]any{"evidence": map[string]any{"id": 1}}}, `must be a file reference`)
	expect400(map[string]any{"taxDataStatus": "final", "taxData": map[string]any{"sales": 100}},
		`tax data is missing mandatory fields: Rate (%)`)
	expect400(map[string]any{"taxDataStatus": "final"}, `tax data is missing mandatory fields: Total Sales (net), Rate (%)`)

	// A template id of another tenant on the instance → 400 (resolver + composite FK).
	var foreign dataTemplate
	r = s.As(other).POST(s.T(), "/data-templates", customBody("Foreign", []map[string]any{{"id": "x", "name": "X", "fieldType": "text"}}))
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &foreign)
	expect400(map[string]any{"dataTemplateId": foreign.ID}, "data template not found in this tenant")
	// ... and the composite FK itself refuses it at the database (bypassing the resolver).
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		_, err := tx.Exec(`UPDATE task_instances SET data_template_id = $2 WHERE id = $1`, id, foreign.ID)
		s.Require().Error(err)
		s.Require().Contains(err.Error(), "task_instances_data_template_fk")
	})

	// Valid draft: partial data, null clears, stored as validated.
	var ti taskInstance
	r = put(map[string]any{"taxData": map[string]any{"sales": 1234.56, "invoices": 12, "filed": "2025-02-10", "nilReturn": false, "note": nil}})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &ti)
	s.Require().Equal("draft", ti.TaxDataStatus)
	s.Require().Equal(1234.56, ti.TaxData["sales"])
	s.Require().Equal(float64(12), ti.TaxData["invoices"])
	s.Require().NotContains(ti.TaxData, "note")
	s.Require().Equal(tpl.ID, *ti.DataTemplateID) // omitted dataTemplateId keeps the inherited one

	// Submit without the mandatory rate → 400; with it (still draft) → 200.
	r = s.As(tenant).POST(s.T(), "/task-instances/"+id+"/submit-for-approval", nil)
	r.AssertStatus(s.T(), http.StatusBadRequest)
	s.Require().Equal("tax data is missing mandatory fields: Rate (%)", errorMessage(s, r))

	put(map[string]any{"taxData": map[string]any{"sales": 1234.56, "rate": 19}}).AssertStatus(s.T(), http.StatusOK)
	r = s.As(tenant).POST(s.T(), "/task-instances/"+id+"/submit-for-approval", nil)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &ti)
	s.Require().Equal("pending_approval", ti.Status)

	// The template is in use (workflow task + instance) → 409; unattached custom still deletable.
	r = s.As(tenant).DELETE(s.T(), "/data-templates/"+tpl.ID)
	r.AssertStatus(s.T(), http.StatusConflict)
	s.Require().Contains(r.BodyString(), "template is in use")

	// A final record with every mandatory field on a fresh instance.
	wf2 := s.seedRecurringWorkflow(tenant, "VAT2")
	s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf2, "name": "Prepare", "taskType": "preparation",
	}).AssertStatus(s.T(), http.StatusCreated)
	s.As(tenant).POST(s.T(), "/workflows/"+wf2+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wf2).DecodeData(s.T(), &instances)
	s.Require().Nil(instances[0].DataTemplateID)
	// No template: free-form tax data is accepted as before.
	s.As(tenant).PUT(s.T(), "/task-instances/"+instances[0].ID, map[string]any{
		"status": "in_progress", "taxData": map[string]any{"anything": "goes"},
	}).AssertStatus(s.T(), http.StatusOK)
	// Attaching the template on PUT makes it authoritative from then on.
	r = s.As(tenant).PUT(s.T(), "/task-instances/"+instances[0].ID, map[string]any{
		"status": "in_progress", "dataTemplateId": tpl.ID, "taxDataStatus": "final",
		"taxData": map[string]any{"sales": 10, "rate": 0},
	})
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), &ti)
	s.Require().Equal("final", ti.TaxDataStatus)
	s.Require().Equal(tpl.ID, *ti.DataTemplateID)
	s.As(tenant).PUT(s.T(), "/task-instances/"+instances[0].ID, map[string]any{
		"status": "in_progress", "taxData": map[string]any{"anything": "goes"},
	}).AssertStatus(s.T(), http.StatusBadRequest)
}
