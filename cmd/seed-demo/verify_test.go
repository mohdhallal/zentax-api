package main

// End-to-end tests for `verify` itself, against a fake API.
//
// The fake answers every endpoint FROM THE ORACLE, so a correct server is one
// verify must accept with zero differences. Each test then breaks exactly one
// thing — a heatmap count, a due date, a summary the status filter should not
// have narrowed, a payment deadline on a project instance — and asserts that
// verify fails and says which field, on which key, with which two values.
//
// What this proves is the verifier, not the oracle: the oracle's own rules are
// tested in oracle_test.go and oracle_reports_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// verifyTestDataset is a complete, valid one-tenant dataset: two entities, two
// obligation types, a monthly VAT workflow across three periods (one completed
// on time, one completed late, one still ahead), an archived workflow and a
// project workflow. It is written to disk because verify loads its spec from a
// path — the same way the command does.
const verifyTestDataset = `{
  "asOf": "2026-09-06",
  "validityWindow": {"from": "2026-09-03", "to": "2026-09-10", "note": "test fixture"},
  "conventions": {
    "completionLocalTime": "10:00",
    "emailPattern": "<role>@<slug>.test",
    "passwordPolicy": ">= 12 chars, demo only"
  },
  "productFixes": ["F1", "F6"],
  "tenants": [
    {
      "key": "acme",
      "slug": "acme-test",
      "name": "Acme Test",
      "timezone": "Europe/Berlin",
      "admin": {
        "key": "acme.admin", "email": "admin@acme-test.test", "name": "Ada Admin",
        "role": "tenant_admin", "scopeEntity": null, "password": "demo-password-1", "status": "active"
      },
      "users": [
        {
          "key": "acme.prep", "email": "prep@acme-test.test", "name": "Piet Preparer",
          "role": "preparer", "scopeEntity": null, "password": "demo-password-2", "status": "active"
        },
        {
          "key": "acme.rev", "email": "rev@acme-test.test", "name": "Rita Reviewer",
          "role": "reviewer", "scopeEntity": null, "password": "demo-password-3", "status": "active"
        }
      ],
      "entities": [
        {
          "key": "acme.de", "name": "Acme DE GmbH", "legalName": "Acme DE GmbH",
          "country": "Germany", "taxResidency": "Germany", "parent": null,
          "fiscalCalendarPattern": "standard", "financialYearEnd": "12-31"
        },
        {
          "key": "acme.uk", "name": "Acme UK Ltd", "legalName": "Acme UK Ltd",
          "country": "United Kingdom", "taxResidency": "United Kingdom", "parent": null,
          "fiscalCalendarPattern": "standard", "financialYearEnd": "03-31"
        }
      ],
      "obligationTypes": [
        {"key": "acme.vat", "name": "Value Added Tax", "code": "VAT", "template": "VAT",
         "category": "predefined", "description": "VAT"},
        {"key": "acme.cit", "name": "Corporate Income Tax", "code": "CIT", "template": "CIT",
         "category": "predefined", "description": "CIT"}
      ],
      "entityObligations": [
        {
          "key": "acme.de-vat", "entity": "acme.de", "obligationType": "acme.vat",
          "periodicity": "monthly", "currency": "EUR", "jurisdiction": "Germany",
          "taxReferenceNumber": "DE811234567",
          "deadlineRule": {
            "type": "period_offset", "reference": "period_end", "weekendAdjustment": "next-business-day",
            "filingOffset": {"months": 0, "days": 10},
            "paymentOffset": {"months": 1, "days": 10}
          }
        },
        {
          "key": "acme.uk-cit", "entity": "acme.uk", "obligationType": "acme.cit",
          "periodicity": "annual", "currency": "GBP", "jurisdiction": "United Kingdom",
          "taxReferenceNumber": null,
          "deadlineRule": {
            "type": "fixed", "reference": "period_end", "weekendAdjustment": "none",
            "fixedDates": ["12-31"], "paymentFixedDates": ["01-31"]
          }
        }
      ],
      "workflows": [
        {
          "key": "acme.w1", "name": "DE VAT 2026", "description": "DE VAT 2026",
          "category": "recurring", "projectType": null,
          "entity": "acme.de", "obligationType": "acme.vat", "entityObligation": "acme.de-vat",
          "financialYear": "2026", "periodicity": "monthly",
          "selectedPeriods": ["M1", "M2", "M10"],
          "dueDateRule": {
            "reference": "period_end", "offsetUnit": "days", "offsetValue": 15,
            "offsetDirection": "after", "weekendAdjustment": "next-business-day"
          },
          "startDate": "2026-01-01", "endDate": "2026-12-31", "tasksSequential": false,
          "writer": "acme.prep", "approver": "acme.rev",
          "lifecycle": {"start": true, "finalStatus": "active"},
          "templates": [
            {"key": "collect", "name": "Collect data", "taskType": "data_request", "roleLabel": "Preparer",
             "approvalRequired": false, "dueDateReference": "period_end", "dueDateOffsetValue": 2,
             "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "after", "orderIndex": 0,
             "dataTemplate": null},
            {"key": "prepare", "name": "Prepare return", "taskType": "preparation", "roleLabel": "Preparer",
             "approvalRequired": false, "dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
             "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": 1,
             "dataTemplate": "VAT"},
            {"key": "pay", "name": "Pay", "taskType": "payment", "roleLabel": "Finance",
             "approvalRequired": false, "dueDateReference": "payment_deadline", "dueDateOffsetValue": 0,
             "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": 2,
             "dataTemplate": null}
          ],
          "instances": [
            {"period": "M1", "task": "collect", "status": "completed", "via": "put",
             "assignee": "acme.prep", "completedOn": "2026-02-01"},
            {"period": "M1", "task": "prepare", "status": "completed", "via": "put",
             "assignee": "acme.prep", "completedOn": "2026-02-20",
             "taxData": {"salesTotal": 400000, "outputVat": 76000, "inputVat": 50000, "netVat": 26000},
             "taxDataStatus": "final"},
            {"period": "M1", "task": "pay", "status": "completed", "via": "approve",
             "assignee": "acme.prep", "submittedOn": "2026-03-08", "completedOn": "2026-03-09",
             "notes": "paid, penalty assessed"},
            {"period": "M2", "task": "collect", "status": "in_progress", "via": "put",
             "assignee": "acme.prep"},
            {"period": "M2", "task": "prepare", "status": "pending_approval", "via": "submit",
             "assignee": "acme.prep", "submittedOn": "2026-03-10",
             "taxData": {"outputVat": 51000, "inputVat": 21000, "penaltyAmount": 250},
             "taxDataStatus": "draft"},
            {"period": "M10", "task": "collect", "status": "blocked", "via": "put",
             "assignee": "acme.prep", "notes": "waiting on the bank"}
          ],
          "documents": []
        },
        {
          "key": "acme.w2", "name": "UK CIT 2026", "description": "UK CIT 2026",
          "category": "recurring", "projectType": null,
          "entity": "acme.uk", "obligationType": "acme.cit", "entityObligation": "acme.uk-cit",
          "financialYear": "2026", "periodicity": "annual",
          "selectedPeriods": ["Y1"],
          "dueDateRule": {
            "reference": "period_end", "offsetUnit": "months", "offsetValue": 9,
            "offsetDirection": "after", "weekendAdjustment": "none"
          },
          "startDate": "2025-04-01", "endDate": "2026-03-31", "tasksSequential": false,
          "writer": "acme.prep", "approver": "acme.rev",
          "lifecycle": {"start": true, "finalStatus": "archived"},
          "templates": [
            {"key": "file", "name": "File", "taskType": "submission", "roleLabel": "Preparer",
             "approvalRequired": false, "dueDateReference": "filing_deadline", "dueDateOffsetValue": 0,
             "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": 0,
             "dataTemplate": "CIT"}
          ],
          "instances": [
            {"period": "Y1", "task": "file", "status": "completed", "via": "put",
             "assignee": "acme.prep", "completedOn": "2026-12-30",
             "taxData": {"taxableIncome": 900000, "taxLiability": 171000},
             "taxDataStatus": "final"}
          ],
          "documents": []
        },
        {
          "key": "acme.w3", "name": "VAT dispute 2026", "description": "A dispute",
          "category": "project", "projectType": "dispute",
          "entity": "acme.de", "obligationType": null, "entityObligation": null,
          "financialYear": "2026", "periodicity": null, "selectedPeriods": [],
          "dueDateRule": {
            "reference": "period_end", "offsetUnit": "days", "offsetValue": 0,
            "offsetDirection": "after", "weekendAdjustment": "none"
          },
          "startDate": "2026-02-01", "endDate": "2026-06-30", "tasksSequential": true,
          "writer": "acme.prep", "approver": "acme.rev",
          "lifecycle": {"start": true, "finalStatus": "active"},
          "templates": [
            {"key": "appeal", "name": "File the appeal", "taskType": "submission", "roleLabel": "Preparer",
             "approvalRequired": false, "dueDateReference": "period_end", "dueDateOffsetValue": 30,
             "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": 0,
             "dataTemplate": null}
          ],
          "instances": [
            {"period": "PROJECT", "task": "appeal", "status": "completed", "via": "put",
             "assignee": "acme.prep", "completedOn": "2026-05-20"}
          ],
          "documents": []
        },
        {
          "key": "acme.w4", "name": "DE VAT 2027 (draft)", "description": "Not started",
          "category": "recurring", "projectType": null,
          "entity": "acme.de", "obligationType": "acme.vat", "entityObligation": "acme.de-vat",
          "financialYear": "2027", "periodicity": "monthly", "selectedPeriods": ["M1"],
          "dueDateRule": {
            "reference": "period_end", "offsetUnit": "days", "offsetValue": 15,
            "offsetDirection": "after", "weekendAdjustment": "next-business-day"
          },
          "startDate": "2027-01-01", "endDate": "2027-12-31", "tasksSequential": false,
          "writer": "acme.prep", "approver": "acme.rev",
          "lifecycle": {"start": false, "finalStatus": "draft"},
          "templates": [
            {"key": "collect", "name": "Collect data", "taskType": "data_request", "roleLabel": "Preparer",
             "approvalRequired": false, "dueDateReference": "period_end", "dueDateOffsetValue": 2,
             "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "after", "orderIndex": 0,
             "dataTemplate": null}
          ],
          "instances": [],
          "documents": []
        }
      ]
    }
  ],
  "expectedAsOf": {}
}`

// ---------------------------------------------------------------------------
// The fake API
// ---------------------------------------------------------------------------

// verifyFake serves the endpoints verify calls, answering from the oracle.
// Corrupt lets a test break one response before it is written.
type verifyFake struct {
	t       *testing.T
	world   *oracleWorld
	tenant  *oracleTenant
	ids     map[string]string // dataset key → fabricated id
	instIDs map[*oracleInstance]string
	created time.Time

	// Corrupt is called with the request path and the payload about to be
	// written; it may mutate the payload in place.
	Corrupt func(path string, payload any)
}

func verifyNewFake(t *testing.T, world *oracleWorld) *verifyFake {
	t.Helper()
	require.Len(t, world.tenants, 1)
	f := &verifyFake{
		t: t, world: world, tenant: world.tenants[0],
		ids: map[string]string{}, instIDs: map[*oracleInstance]string{},
		created: time.Date(2026, 9, 6, 8, 30, 0, 0, time.UTC),
	}
	n := 0
	id := func(key string) string {
		n++
		value := fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
		f.ids[key] = value
		return value
	}
	id("tenant:" + f.tenant.spec.Key)
	for _, e := range f.tenant.spec.Entities {
		id("entity:" + e.Key)
	}
	for _, o := range f.tenant.spec.ObligationTypes {
		id("obligation:" + o.Key)
	}
	for _, w := range f.tenant.workflows {
		id("workflow:" + w.spec.Key)
		for _, inst := range w.instances {
			f.instIDs[inst] = id("instance:" + inst.ref())
		}
	}
	return f
}

func (f *verifyFake) entityID(key string) string     { return f.ids["entity:"+key] }
func (f *verifyFake) obligationID(key string) string { return f.ids["obligation:"+key] }
func (f *verifyFake) workflowID(key string) string   { return f.ids["workflow:"+key] }

// keyForID is the inverse, for translating a filter the client sent back into
// dataset space.
func (f *verifyFake) keyForID(prefix, id string) string {
	for key, value := range f.ids {
		if value == id && strings.HasPrefix(key, prefix+":") {
			return strings.TrimPrefix(key, prefix+":")
		}
	}
	return ""
}

func (f *verifyFake) server() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		f.write(w, r, map[string]any{"status": "ready"}, nil)
	})
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: apiclient.SessionCookieName, Value: "test-session"})
		f.write(w, r, map[string]any{
			"mfaRequired": false,
			"user": map[string]any{
				"id": f.ids["tenant:"+f.tenant.spec.Key], "email": f.tenant.spec.Admin.Email,
				"name": f.tenant.spec.Admin.Name, "status": "active",
			},
		}, nil)
	})
	mux.HandleFunc("/auth/me", func(w http.ResponseWriter, r *http.Request) {
		f.write(w, r, map[string]any{
			"id": "user-1", "email": f.tenant.spec.Admin.Email, "name": f.tenant.spec.Admin.Name,
			"tenant": map[string]any{
				"id":       f.ids["tenant:"+f.tenant.spec.Key],
				"slug":     f.tenant.spec.Slug,
				"name":     f.tenant.spec.Name,
				"timezone": f.tenant.spec.Timezone,
			},
		}, nil)
	})
	mux.HandleFunc("/entities", f.handleEntities)
	mux.HandleFunc("/entities/", f.handlePeriods)
	mux.HandleFunc("/obligation-types", f.handleObligationTypes)
	mux.HandleFunc("/workflows", f.handleWorkflows)
	mux.HandleFunc("/workflows/", f.handlePreview)
	mux.HandleFunc("/reports/task-instances", f.handleInstances)
	mux.HandleFunc("/reports/workflow-stats", f.handleWorkflowStats)
	mux.HandleFunc("/reports/compliance-heatmap", f.handleHeatmap)
	mux.HandleFunc("/reports/compliance-status", f.handleCompliance)
	mux.HandleFunc("/reports/tax-financial", f.handleFinancial)
	mux.HandleFunc("/reports/export-raw", f.handleExport)
	return httptest.NewServer(mux)
}

// write emits the success envelope, giving Corrupt the last word.
func (f *verifyFake) write(w http.ResponseWriter, r *http.Request, data any, pagination map[string]any) {
	if f.Corrupt != nil {
		f.Corrupt(r.URL.Path, data)
	}
	body := map[string]any{"status": true, "data": data}
	if pagination != nil {
		body["pagination"] = pagination
	}
	w.Header().Set("Content-Type", "application/json")
	require.NoError(f.t, json.NewEncoder(w).Encode(body))
}

func (f *verifyFake) page(w http.ResponseWriter, r *http.Request, rows []map[string]any) {
	limit, offset := verifyTestInt(r, "limit", 20), verifyTestInt(r, "offset", 0)
	total := len(rows)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	f.write(w, r, rows[offset:end], map[string]any{"total": total, "limit": limit, "offset": offset})
}

func verifyTestInt(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func (f *verifyFake) handleEntities(w http.ResponseWriter, r *http.Request) {
	rows := []map[string]any{}
	for _, e := range f.tenant.spec.Entities {
		rows = append(rows, map[string]any{
			"id": f.entityID(e.Key), "name": e.Name, "country": e.Country,
			"financialYearEnd": e.FinancialYearEnd, "fiscalCalendarPattern": e.FiscalCalendarPattern,
			"parentEntityId": nil,
		})
	}
	f.page(w, r, rows)
}

func (f *verifyFake) handleObligationTypes(w http.ResponseWriter, r *http.Request) {
	rows := []map[string]any{}
	for _, o := range f.tenant.spec.ObligationTypes {
		rows = append(rows, map[string]any{
			"id": f.obligationID(o.Key), "name": o.Name, "code": o.Code,
			"template": o.Template, "category": o.Category,
		})
	}
	f.page(w, r, rows)
}

func (f *verifyFake) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	rows := []map[string]any{}
	for _, wf := range f.tenant.workflows {
		row := map[string]any{
			"id": f.workflowID(wf.spec.Key), "name": wf.spec.Name,
			"workflowCategory": wf.spec.Category, "status": wf.status,
			"financialYear": wf.spec.FinancialYear, "selectedPeriods": wf.spec.SelectedPeriods,
			"createdAt": f.created.Format("2006-01-02T15:04:05.000Z"),
		}
		if wf.entity != nil {
			row["entityId"] = f.entityID(wf.entity.Key)
		}
		if wf.obligation != nil {
			row["obligationTypeId"] = f.obligationID(wf.obligation.Key)
		}
		rows = append(rows, row)
	}
	f.page(w, r, rows)
}

// handlePeriods serves /entities/{id}/periods from the same calendar engine.
func (f *verifyFake) handlePeriods(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	require.Len(f.t, parts, 3)
	key := f.keyForID("entity", parts[1])
	entity, ok := f.tenant.spec.Entity(key)
	require.True(f.t, ok, "unknown entity id %s", parts[1])
	year, err := strconv.Atoi(r.URL.Query().Get("financialYear"))
	require.NoError(f.t, err)
	cal, err := oracleCalendarFor(entity, year)
	require.NoError(f.t, err)
	periods, err := cal.Periods(r.URL.Query().Get("periodicity"))
	require.NoError(f.t, err)

	rows := []map[string]any{}
	for _, p := range periods {
		rows = append(rows, map[string]any{
			"code": p.Code, "label": p.Label, "startDate": p.Start, "endDate": p.End,
		})
	}
	f.write(w, r, rows, nil)
}

// handlePreview serves /workflows/{id}/preview from the oracle's plan.
func (f *verifyFake) handlePreview(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	require.Len(f.t, parts, 3)
	wf, ok := f.tenant.byWfKey[f.keyForID("workflow", parts[1])]
	require.True(f.t, ok, "unknown workflow id %s", parts[1])

	tasks := []map[string]any{}
	for _, inst := range wf.planned {
		tasks = append(tasks, map[string]any{
			"templateId": inst.TemplateKey, "periodCode": inst.PeriodCode, "name": inst.Name,
			"taskType": inst.TaskType, "dueDate": inst.DueDate, "periodEndDate": inst.PeriodEnd,
			"filingDeadline": inst.FilingDeadline, "paymentDeadline": inst.PaymentDeadline,
			"orderIndex": inst.OrderIndex,
		})
	}
	periods := len(wf.spec.SelectedPeriods)
	if wf.spec.IsProject() {
		periods = 1
	}
	f.write(w, r, map[string]any{
		"workflowId": f.workflowID(wf.spec.Key), "workflowName": wf.spec.Name,
		"totalPeriods": periods, "taskTemplates": len(wf.spec.Templates),
		"totalTasks": len(wf.planned), "assigneesImpacted": []string{}, "tasks": tasks,
	}, nil)
}

func (f *verifyFake) handleInstances(w http.ResponseWriter, r *http.Request) {
	rows := []map[string]any{}
	for _, inst := range f.tenant.instances {
		row := map[string]any{
			"id": f.instIDs[inst], "workflowId": f.workflowID(inst.workflowKey()),
			"periodCode": inst.PeriodCode, "name": inst.Name, "taskType": inst.TaskType,
			"status": inst.Status, "dueDate": inst.DueDate, "periodEndDate": inst.PeriodEnd,
			"filingDeadline": inst.FilingDeadline, "paymentDeadline": inst.PaymentDeadline,
			"approvalRequired": inst.ApprovalRequired, "orderIndex": inst.OrderIndex,
			"taxDataStatus": inst.TaxDataStatus, "workflowCategory": inst.workflow.spec.Category,
			"assigneeId": nil, "assigneeName": nil, "notes": nil,
			"completedAt": nil, "submittedAt": nil, "approvedAt": nil,
			"submittedBy": nil, "approvedBy": nil,
		}
		if inst.AssigneeName != "" {
			row["assigneeId"] = "user-" + inst.AssigneeKey
			row["assigneeName"] = inst.AssigneeName
		}
		if inst.Notes != "" {
			row["notes"] = inst.Notes
		}
		if !inst.CompletedAt.IsZero() {
			row["completedAt"] = verifyTestInstant(inst.CompletedAt)
		}
		if !inst.SubmittedOn.IsZero() {
			submitted, ok := spec.Instance{CompletedOn: inst.SubmittedOn}.
				CompletionInstant(f.tenant.zone, "10:00")
			require.True(f.t, ok)
			row["submittedAt"] = verifyTestInstant(submitted)
			row["submittedBy"] = "user-" + inst.AssigneeKey
		}
		if inst.Via == "approve" {
			row["approvedAt"] = row["completedAt"]
			row["approvedBy"] = "user-approver"
		}
		rows = append(rows, row)
	}
	f.page(w, r, rows)
}

func verifyTestInstant(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func (f *verifyFake) handleWorkflowStats(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	for _, wf := range f.tenant.workflows {
		stats := wf.stats()
		var next any
		if !stats.NextDueDate.IsZero() {
			next = stats.NextDueDate.String()
		}
		out[f.workflowID(wf.spec.Key)] = map[string]any{
			"totalTasks": stats.TotalTasks, "completedTasks": stats.CompletedTasks,
			"completionPercent": stats.CompletionPercent, "nextDueDate": next,
		}
	}
	f.write(w, r, out, nil)
}

// filtersFrom translates the query the client sent back into dataset keys, so
// the fake can answer with the oracle.
func (f *verifyFake) filtersFrom(r *http.Request) oracleFilters {
	query := r.URL.Query()
	filters := oracleFilters{Year: query.Get("year")}
	if id := query.Get("entityId"); oracleFilterSet(id) {
		filters.EntityKey = f.keyForID("entity", id)
	}
	if id := query.Get("obligationTypeId"); oracleFilterSet(id) {
		filters.ObligationKey = f.keyForID("obligation", id)
	}
	return filters
}

func (f *verifyFake) handleHeatmap(w http.ResponseWriter, r *http.Request) {
	viewMode := r.URL.Query().Get("viewMode")
	if viewMode == "" {
		viewMode = oracleViewPeriod
	}
	heatmap := f.tenant.heatmap(f.filtersFrom(r), viewMode)

	colID := func(key string) string {
		if viewMode == oracleViewTaxType {
			return f.obligationID(key)
		}
		return key
	}
	cells := []map[string]any{}
	keys := make([]string, 0, len(heatmap.Cells))
	for key := range heatmap.Cells {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cell := heatmap.Cells[key]
		workflows := []string{}
		for wfKey := range cell.WorkflowKeys {
			workflows = append(workflows, f.workflowID(wfKey))
		}
		sort.Strings(workflows)
		cells = append(cells, map[string]any{
			"rowId": f.entityID(cell.RowKey), "rowLabel": cell.RowLabel,
			"colId": colID(cell.ColKey), "colLabel": cell.ColLabel,
			"status": cell.status(), "totalTasks": cell.TotalTasks,
			"completedTasks": cell.CompletedTasks, "overdueTasks": cell.OverdueTasks,
			"inProgressTasks": cell.InProgressTasks, "workflowIds": workflows,
		})
	}
	rows := []map[string]any{}
	for _, rowKey := range heatmap.RowOrder {
		label := rowKey
		for _, cell := range heatmap.Cells {
			if cell.RowKey == rowKey {
				label = cell.RowLabel
				break
			}
		}
		rows = append(rows, map[string]any{"id": f.entityID(rowKey), "label": label})
	}
	cols := []map[string]any{}
	for _, colKey := range heatmap.ColOrder {
		cols = append(cols, map[string]any{"id": colID(colKey), "label": colKey})
	}
	f.write(w, r, map[string]any{
		"rows": rows, "cols": cols, "cells": cells,
		"summary": map[string]any{
			"totalCells": heatmap.Summary.TotalCells, "green": heatmap.Summary.Green,
			"amber": heatmap.Summary.Amber, "red": heatmap.Summary.Red,
		},
	}, nil)
}

func (f *verifyFake) handleCompliance(w http.ResponseWriter, r *http.Request) {
	compliance := f.tenant.compliance(f.filtersFrom(r))
	rows := compliance.rowsWithClass(r.URL.Query().Get("status"))
	limit, offset := verifyTestInt(r, "limit", 1000), verifyTestInt(r, "offset", 0)

	out := []map[string]any{}
	for i, row := range rows {
		if i < offset || i >= offset+limit {
			continue
		}
		var filingDate any
		if !row.Instance.CompletedAt.IsZero() {
			filingDate = verifyTestInstant(row.Instance.CompletedAt)
		}
		out = append(out, map[string]any{
			"entityName": row.EntityName, "entityId": f.entityID(row.EntityKey),
			"taxType": row.TaxType, "obligationName": row.ObligationName,
			"obligationCode": row.ObligationCode, "period": row.Period,
			"filingDeadline": row.FilingDeadline, "filingDate": filingDate,
			"complianceStatus": row.Class, "penaltyInterest": row.PenaltyInterest,
			"workflowId":     f.workflowID(row.Instance.workflowKey()),
			"taskInstanceId": f.instIDs[row.Instance],
		})
	}
	f.write(w, r, map[string]any{
		"rows": out,
		"summary": map[string]any{
			"total": compliance.Summary.Total, "onTime": compliance.Summary.OnTime,
			"late": compliance.Summary.Late, "missed": compliance.Summary.Missed,
			"notDue": compliance.Summary.NotDue,
		},
		"totalCount": len(rows),
	}, nil)
}

func (f *verifyFake) handleFinancial(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("groupBy")
	if groupBy == "" {
		groupBy = oracleGroupEntity
	}
	financial := f.tenant.financial(f.filtersFrom(r), groupBy)

	figures := func(f *oracleFigures) map[string]any {
		out := map[string]any{}
		for _, field := range oracleFigureFields {
			out[field] = oracleRatFloat(f.field(field))
		}
		return out
	}
	rows := []map[string]any{}
	for _, row := range financial.Rows {
		entry := figures(row.Figures)
		entry["entityName"], entry["entityId"] = row.EntityName, f.entityID(row.EntityKey)
		entry["country"], entry["taxType"] = row.Country, row.TaxType
		entry["obligationName"], entry["obligationCode"] = row.ObligationName, row.ObligationCode
		entry["period"], entry["financialYear"] = row.Period, row.FinancialYear
		rows = append(rows, entry)
	}
	aggregated := []map[string]any{}
	for _, key := range financial.GroupOrder {
		group := financial.Groups[key]
		entry := figures(&group.Figures)
		entry["key"] = key
		switch groupBy {
		case oracleGroupEntity:
			entry["key"] = f.entityID(key)
		case oracleGroupObligation:
			entry["key"] = f.obligationID(key)
		}
		entry["label"], entry["count"] = group.Label, group.Count
		aggregated = append(aggregated, entry)
	}
	chart := []map[string]any{}
	for _, point := range financial.Chart {
		chart = append(chart, map[string]any{
			"period":       point.Key,
			"outputVat":    oracleRatFloat(&point.Figures.OutputVat),
			"inputVat":     oracleRatFloat(&point.Figures.InputVat),
			"netVat":       oracleRatFloat(&point.Figures.NetVat),
			"taxLiability": oracleRatFloat(&point.Figures.TaxLiability),
			"whtAmount":    oracleRatFloat(&point.Figures.WhtAmount),
			"totalAmount":  oracleRatFloat(&point.Figures.TotalAmount),
		})
	}
	f.write(w, r, map[string]any{
		"rows": rows, "aggregated": aggregated, "chartData": chart,
		"summary": map[string]any{
			"totalOutputVat":      oracleRatFloat(&financial.Summary.OutputVat),
			"totalInputVat":       oracleRatFloat(&financial.Summary.InputVat),
			"totalNetVat":         oracleRatFloat(&financial.Summary.NetVat),
			"totalTaxableIncome":  oracleRatFloat(&financial.Summary.TaxableIncome),
			"totalTaxLiability":   oracleRatFloat(&financial.Summary.TaxLiability),
			"totalWht":            oracleRatFloat(&financial.Summary.WhtAmount),
			"totalEngagementCost": oracleRatFloat(&financial.Summary.EngagementCost),
			"totalAmount":         oracleRatFloat(&financial.Summary.TotalAmount),
			"recordCount":         financial.RecordCount,
		},
		"totalCount": financial.RecordCount,
	}, nil)
}

func (f *verifyFake) handleExport(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	dataset := query.Get("dataset")
	if dataset == "" {
		dataset = oracleDatasetTasks
	}
	filters := oracleExportFilters{Category: query.Get("category")}
	if id := query.Get("entityId"); oracleFilterSet(id) {
		filters.EntityKey = f.keyForID("entity", id)
	}
	if id := query.Get("obligationTypeId"); oracleFilterSet(id) {
		filters.ObligationKey = f.keyForID("obligation", id)
	}
	if raw := query.Get("dateFrom"); raw != "" {
		parsed, err := dateonly.Parse(raw)
		require.NoError(f.t, err)
		filters.DateFrom = parsed
	}
	if raw := query.Get("dateTo"); raw != "" {
		parsed, err := dateonly.Parse(raw)
		require.NoError(f.t, err)
		filters.DateTo = parsed
	}

	created := map[string]dateonly.Date{}
	for _, wf := range f.tenant.workflows {
		created[wf.spec.Key] = dateonly.FromTime(f.created)
	}

	rows := []map[string]any{}
	switch dataset {
	case oracleDatasetWorkflows:
		for _, wf := range f.tenant.exportWorkflows(filters, created) {
			entity, obligation, periodicity, projectType := "", "", "", ""
			if wf.entity != nil {
				entity = wf.entity.Name
			}
			if wf.obligation != nil {
				obligation = wf.obligation.Code
			}
			if wf.spec.Periodicity != nil {
				periodicity = *wf.spec.Periodicity
			}
			if wf.spec.ProjectType != nil {
				projectType = *wf.spec.ProjectType
			}
			rows = append(rows, map[string]any{
				"workflowName": wf.spec.Name, "category": wf.spec.Category, "status": wf.status,
				"financialYear": wf.spec.FinancialYear, "entityName": entity,
				"obligationCode": obligation, "periodicity": periodicity, "projectType": projectType,
			})
		}
	case oracleDatasetTaxData:
		for _, inst := range f.tenant.exportInstances(filters, true) {
			figures := oracleExportTaxDataFigures(inst.TaxData)
			rows = append(rows, map[string]any{
				"workflowName": inst.workflow.spec.Name, "periodCode": inst.PeriodCode,
				"taskName": inst.Name, "taxType": inst.taxType(),
				"financialYear": inst.workflow.spec.FinancialYear, "taxDataStatus": inst.TaxDataStatus,
				"outputVat": oracleRatFloat(&figures.OutputVat), "inputVat": oracleRatFloat(&figures.InputVat),
				"netVat": oracleRatFloat(&figures.NetVat), "taxableIncome": oracleRatFloat(&figures.TaxableIncome),
				"taxLiability": oracleRatFloat(&figures.TaxLiability), "whtAmount": oracleRatFloat(&figures.WhtAmount),
				"penaltyAmount":  oracleRatFloat(&figures.PenaltyAmount),
				"interestAmount": oracleRatFloat(&figures.InterestAmount),
				"engagementCost": oracleRatFloat(&figures.EngagementCost),
			})
		}
	default:
		for _, inst := range f.tenant.exportInstances(filters, false) {
			rows = append(rows, map[string]any{
				"workflowName": inst.workflow.spec.Name, "periodCode": inst.PeriodCode,
				"taskName": inst.Name, "status": inst.Status, "taxDataStatus": inst.TaxDataStatus,
				"dueDate": inst.DueDate.String(), "filingDeadline": inst.FilingDeadline.String(),
				"assigneeName": inst.AssigneeName, "workflowCategory": inst.workflow.spec.Category,
			})
		}
	}
	f.write(w, r, map[string]any{"dataset": dataset, "rows": rows, "totalCount": len(rows)}, nil)
}

// ---------------------------------------------------------------------------
// The tests
// ---------------------------------------------------------------------------

// verifyTestRun writes the fixture, starts the fake and runs verify against it.
func verifyTestRun(t *testing.T, corrupt func(path string, payload any), options ...func(*VerifyDeps)) (string, error) {
	t.Helper()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "dataset.json")
	require.NoError(t, os.WriteFile(specPath, []byte(verifyTestDataset), 0o600))

	loaded, err := spec.LoadAndValidate(specPath)
	require.NoError(t, err, "the fixture must be a valid dataset")
	world, err := buildOracleWorld(loaded, time.Now(), loaded.AsOf)
	require.NoError(t, err)

	fake := verifyNewFake(t, world)
	fake.Corrupt = corrupt
	server := fake.server()
	defer server.Close()

	var out bytes.Buffer
	deps := VerifyDeps{
		Spec:   specPath,
		API:    server.URL,
		In:     filepath.Join(dir, "absent-output.json"),
		Today:  loaded.AsOf.String(),
		Stdout: &out,
		Stderr: &out,
		Client: apiclient.New(server.URL),
		Now:    func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) },
	}
	for _, option := range options {
		option(&deps)
	}
	// The result must be read AFTER the run: a return statement evaluates its
	// expressions left to right, so out.String() inline would capture nothing.
	err = runVerify(context.Background(), deps)
	return out.String(), err
}

func TestVerifyAcceptsACorrectServer(t *testing.T) {
	t.Parallel()
	out, err := verifyTestRun(t, nil)
	require.NoError(t, err, "a server answering from the oracle must produce no differences:\n%s", out)
	assert.Contains(t, out, "0 failed")
	assert.Contains(t, out, "0 differences")
	assert.Contains(t, out, "tenant acme")
	assert.Contains(t, out, "Europe/Berlin")
	assert.NotContains(t, out, "DIFFERENCES (")
}

func TestVerifyReportsEveryKindOfDifference(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		corrupt func(payload any)
		expect  []string
	}{
		{
			name: "a heatmap cell count",
			path: "/reports/compliance-heatmap",
			corrupt: func(payload any) {
				cells := payload.(map[string]any)["cells"].([]map[string]any)
				cells[0]["completedTasks"] = 99
			},
			expect: []string{"compliance-heatmap", "completedTasks", "acme.de|", "got 99"},
		},
		{
			name: "a heatmap summary",
			path: "/reports/compliance-heatmap",
			corrupt: func(payload any) {
				payload.(map[string]any)["summary"].(map[string]any)["red"] = 7
			},
			expect: []string{"summary.red", "got 7"},
		},
		{
			name: "a compliance summary narrowed by the status filter (F5)",
			path: "/reports/compliance-status",
			corrupt: func(payload any) {
				summary := payload.(map[string]any)["summary"].(map[string]any)
				summary["total"] = summary["onTime"]
			},
			expect: []string{"compliance-status", "summary.total"},
		},
		{
			name: "a compliance row's classification",
			path: "/reports/compliance-status",
			corrupt: func(payload any) {
				rows := payload.(map[string]any)["rows"].([]map[string]any)
				if len(rows) > 0 {
					rows[0]["complianceStatus"] = oracleNotDue
				}
			},
			expect: []string{"complianceStatus"},
		},
		{
			name: "a financial total",
			path: "/reports/tax-financial",
			corrupt: func(payload any) {
				payload.(map[string]any)["summary"].(map[string]any)["totalNetVat"] = 1.0
			},
			expect: []string{"tax-financial", "summary.netVat", "got 1"},
		},
		{
			name: "an export count",
			path: "/reports/export-raw",
			corrupt: func(payload any) {
				payload.(map[string]any)["totalCount"] = 0
			},
			expect: []string{"export-raw", "totalCount"},
		},
		{
			name: "an instance due date",
			path: "/reports/task-instances",
			corrupt: func(payload any) {
				rows := payload.([]map[string]any)
				rows[0]["dueDate"] = "2999-01-01"
			},
			expect: []string{"task-instances", "dueDate", "2999-01-01"},
		},
		{
			name: "a payment deadline on a project instance",
			path: "/reports/task-instances",
			corrupt: func(payload any) {
				for _, row := range payload.([]map[string]any) {
					if row["periodCode"] == spec.ProjectPeriodCode {
						row["paymentDeadline"] = "2026-06-30"
					}
				}
			},
			expect: []string{"paymentDeadline", "expected null"},
		},
		{
			name: "a completion instant written as the naive civil date",
			path: "/reports/task-instances",
			corrupt: func(payload any) {
				for _, row := range payload.([]map[string]any) {
					if row["completedAt"] != nil {
						row["completedAt"] = "2026-02-01T00:00:00.000Z"
					}
				}
			},
			expect: []string{"completedAt"},
		},
		{
			name: "a workflow-stats next due date",
			path: "/reports/workflow-stats",
			corrupt: func(payload any) {
				for _, stat := range payload.(map[string]any) {
					stat.(map[string]any)["nextDueDate"] = nil
				}
			},
			expect: []string{"workflow-stats", "nextDueDate"},
		},
		{
			name: "a preview date the engine would not produce",
			path: "/workflows/",
			corrupt: func(payload any) {
				tasks := payload.(map[string]any)["tasks"].([]map[string]any)
				if len(tasks) > 0 {
					tasks[0]["filingDeadline"] = "2030-01-01"
				}
			},
			expect: []string{"workflow-preview", "filingDeadline", "2030-01-01"},
		},
		{
			name: "a period the calendar would not produce",
			path: "/entities/",
			corrupt: func(payload any) {
				rows := payload.([]map[string]any)
				if len(rows) > 0 {
					rows[0]["endDate"] = dateonly.New(2030, 1, 1)
				}
			},
			expect: []string{"entity-periods", "endDate"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := verifyTestRun(t, func(path string, payload any) {
				if strings.HasPrefix(path, tc.path) {
					tc.corrupt(payload)
				}
			})
			require.Error(t, err, "output was:\n%s", out)
			assert.Contains(t, err.Error(), "checks failed")
			assert.Contains(t, out, "DIFFERENCES (")
			for _, want := range tc.expect {
				assert.Contains(t, out, want, "the report must name what differs\n%s", out)
			}
		})
	}
}

func TestVerifyJSONOutput(t *testing.T) {
	t.Parallel()
	out, err := verifyTestRun(t, nil, func(d *VerifyDeps) { d.JSON = true })
	require.NoError(t, err)

	var payload struct {
		Summary verifySummary      `json:"summary"`
		Tenants []verifyTenantInfo `json:"tenants"`
		Checks  []struct {
			Tenant  string `json:"tenant"`
			Check   string `json:"check"`
			Outcome string `json:"outcome"`
		} `json:"checks"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload), out)
	assert.Zero(t, payload.Summary.Failed)
	assert.Positive(t, payload.Summary.Checks)
	assert.Equal(t, payload.Summary.Checks, payload.Summary.Passed+payload.Summary.Skipped)
	require.Len(t, payload.Tenants, 1)
	assert.Equal(t, "Europe/Berlin", payload.Tenants[0].Timezone)
	assert.Equal(t, "2026-09-06", payload.Tenants[0].Today)

	names := map[string]bool{}
	for _, check := range payload.Checks {
		names[check.Check] = true
	}
	for _, want := range []string{
		"tenant-registry", "task-instances", "workflow-preview", "entity-periods",
		"compliance-heatmap", "compliance-status", "tax-financial", "export-raw",
		"workflow-stats", "dashboard", "project-participation",
	} {
		assert.True(t, names[want], "the run must cover %s", want)
	}
}

func TestVerifyFlagsAreParsedFromArgs(t *testing.T) {
	t.Parallel()
	deps := VerifyDeps{Spec: "default.json", API: "http://default"}
	fs := verifyFlagSet(&deps)
	require.NoError(t, fs.Parse([]string{
		"--spec", "other.json", "--api", "http://api:3000", "--in", "ids.json",
		"--only", "globex", "--today", "2026-09-10", "--json", "--strict-static",
	}))
	assert.Equal(t, "other.json", deps.Spec)
	assert.Equal(t, "http://api:3000", deps.API)
	assert.Equal(t, "ids.json", deps.In)
	assert.Equal(t, "globex", deps.Only)
	assert.Equal(t, "2026-09-10", deps.Today)
	assert.True(t, deps.JSON)
	assert.True(t, deps.StrictStatic)
}

func TestVerifyRejectsABadTodayAndAnUnknownTenant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "dataset.json")
	require.NoError(t, os.WriteFile(specPath, []byte(verifyTestDataset), 0o600))

	var out bytes.Buffer
	err := runVerify(context.Background(), VerifyDeps{
		Spec: specPath, API: "http://127.0.0.1:1", Today: "yesterday",
		Stdout: &out, Stderr: &out,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--today")

	err = runVerify(context.Background(), VerifyDeps{
		Spec: specPath, API: "http://127.0.0.1:1", Only: "nosuchtenant",
		Stdout: &out, Stderr: &out,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--only")
}

func TestVerifyFilterKeyParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw       string
		filters   oracleFilters
		dataset   string
		viewMode  string
		status    string
		dateRange [2]string
	}{
		{raw: "all", dataset: oracleDatasetTasks},
		{
			raw:      "year=2026&viewMode=period",
			filters:  oracleFilters{Year: "2026"},
			dataset:  oracleDatasetTasks,
			viewMode: oracleViewPeriod,
		},
		{
			raw:      "year=all&entityId=acme.de&viewMode=tax-type",
			filters:  oracleFilters{Year: "all", EntityKey: "acme.de"},
			dataset:  oracleDatasetTasks,
			viewMode: oracleViewTaxType,
		},
		{
			raw:     "obligationTypeId=acme.vat",
			filters: oracleFilters{ObligationKey: "acme.vat"},
			dataset: oracleDatasetTasks,
		},
		{raw: "status=late", status: "late", dataset: oracleDatasetTasks},
		{raw: "workflows&category=project", dataset: oracleDatasetWorkflows},
		{
			raw:       "tax-data&dateFrom=2026-07-01&dateTo=2026-09-30",
			dataset:   oracleDatasetTaxData,
			dateRange: [2]string{"2026-07-01", "2026-09-30"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			key := verifyParseFilterKey(tc.raw)
			assert.Equal(t, tc.filters, key.oracleFilters())
			assert.Equal(t, tc.dataset, key.dataset())
			assert.Equal(t, tc.viewMode, key.Params["viewMode"])
			assert.Equal(t, tc.status, key.Params["status"])

			filters, err := key.exportFilters(nil)
			require.NoError(t, err)
			if tc.dateRange[0] != "" {
				assert.Equal(t, tc.dateRange[0], filters.DateFrom.String())
				assert.Equal(t, tc.dateRange[1], filters.DateTo.String())
			} else {
				assert.True(t, filters.DateFrom.IsZero())
			}
		})
	}
}

func TestVerifySeedDateResolution(t *testing.T) {
	t.Parallel()
	seedDates := []dateonly.Date{dateonly.New(2026, 9, 6), dateonly.New(2026, 9, 7)}

	from, err := verifyResolveDate("dateFrom", "SEED_DATE", seedDates)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-06", from, "the earliest creation date opens the window")

	to, err := verifyResolveDate("dateTo", "SEED_DATE", seedDates)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-07", to, "the latest closes it")

	plain, err := verifyResolveDate("dateFrom", "2026-01-01", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026-01-01", plain)

	_, err = verifyResolveDate("dateFrom", "SEED_DATE", nil)
	require.Error(t, err, "without a workflow there is no seed date to stand in")
}

// TestVerifyStaticTablesAgainstTheRealDataset reads the dataset's own
// expectedAsOf tables and diffs them against the oracle recomputed for that
// same date — no API involved. It reports rather than fails: the dataset is
// edited independently of this package, and `verify --strict-static` is where
// drift is meant to stop a run. A difference here means either the table or
// the oracle is wrong, and the log says exactly which number.
func TestVerifyStaticTablesAgainstTheRealDataset(t *testing.T) {
	t.Parallel()
	loaded := oracleTestLoadDataset(t)
	report := &verifyReport{}
	for _, tenant := range loaded.Tenants {
		verifyStaticTables(report, loaded, tenant.Key, false)
	}

	for _, check := range report.Checks {
		require.NotEqual(t, verifyOutcomeError, check.Outcome, check.Reason)
	}
	// The comparison must have teeth: a table with one number changed has to
	// come back as drift.
	mutated := *loaded
	mutated.ExpectedAsOf = map[string]map[string]spec.Expectations{}
	for date, byTenant := range loaded.ExpectedAsOf {
		mutated.ExpectedAsOf[date] = map[string]spec.Expectations{}
		for key, expectations := range byTenant {
			expectations.Dashboard.Overdue += 3
			mutated.ExpectedAsOf[date][key] = expectations
		}
	}
	sentinel := &verifyReport{}
	verifyStaticTables(sentinel, &mutated, loaded.Tenants[0].Key, false)
	require.NotEmpty(t, sentinel.diffs(verifyLevelInfo),
		"the static comparison must notice a changed number")

	drift := report.diffs(verifyLevelInfo)
	if len(drift) == 0 {
		t.Logf("the dataset's static tables agree with the oracle at %s", loaded.AsOf)
		return
	}
	t.Logf("%d static-table differences at %s (informational — run verify --strict-static to fail on them):",
		len(drift), loaded.AsOf)
	for i, d := range drift {
		if i == 60 {
			t.Logf("  … and %d more", len(drift)-i)
			break
		}
		t.Logf("  %s", d)
	}
}

func TestVerifyValueRendering(t *testing.T) {
	t.Parallel()
	var absentDate *dateonly.Date
	var absentString *string
	assert.Equal(t, "null", verifyValue(nil))
	assert.Equal(t, "null", verifyValue(absentDate))
	assert.Equal(t, "null", verifyValue(absentString))
	assert.Equal(t, "null", verifyValue(dateonly.Date{}))
	assert.Equal(t, "2026-01-31", verifyValue(dateonly.New(2026, 1, 31)))
	assert.Equal(t, "2026-01-31", verifyValue(&[]dateonly.Date{dateonly.New(2026, 1, 31)}[0]))
	assert.Equal(t, "[a, b]", verifyValue([]string{"a", "b"}))
	assert.Equal(t, "true", verifyValue(true))
	assert.Equal(t, "7", verifyValue(7))
}

func TestVerifyDiffCapping(t *testing.T) {
	t.Parallel()
	report := &verifyReport{}
	check := report.check("acme", "compliance-status", "all")
	for i := 0; i < verifyMaxDiffsPerCheck+10; i++ {
		check.diff("field", "key"+strconv.Itoa(i), i, i+1)
	}
	assert.Len(t, check.Diffs, verifyMaxDiffsPerCheck)
	assert.Equal(t, 10, check.Suppressed)
	assert.Equal(t, verifyOutcomeFail, check.Outcome)

	summary := report.summary()
	assert.Equal(t, 1, summary.Checks)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, verifyMaxDiffsPerCheck+10, summary.Diffs)

	var out bytes.Buffer
	report.writeText(&out)
	assert.Contains(t, out.String(), "+10 further differences not listed")
}
