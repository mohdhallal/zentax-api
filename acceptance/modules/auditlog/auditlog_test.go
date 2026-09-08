package auditlog_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// AuditLogSuite proves the read API over the ADR-0008 audit trail: newest
// first, actor names resolved at read time, the workflow filter following
// template / instance events up to their workflow, date windows, pagination,
// the audit:read capability gate and RLS isolation.
type AuditLogSuite struct {
	acceptance.Suite
}

func TestAuditLogSuite(t *testing.T) {
	suite.Run(t, new(AuditLogSuite))
}

type auditRow struct {
	ID           string          `json:"id"`
	Seq          int64           `json:"seq"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resourceType"`
	ResourceID   string          `json:"resourceId"`
	ActorID      string          `json:"actorId"`
	ActorName    *string         `json:"actorName"`
	OccurredAt   string          `json:"occurredAt"`
	RequestID    string          `json:"requestId"`
	Details      json.RawMessage `json:"details"`
	WorkflowID   *string         `json:"workflowId"`
	WorkflowName *string         `json:"workflowName"`
	Hash         string          `json:"hash"`
}

type pagination struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func (s *AuditLogSuite) list(rb *acceptance.RequestBuilder, query string) ([]auditRow, pagination) {
	r := rb.GET(s.T(), "/audit-log"+query)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status     bool       `json:"status"`
		Data       []auditRow `json:"data"`
		Pagination pagination `json:"pagination"`
	}
	s.Require().NoError(json.Unmarshal([]byte(r.BodyString()), &env))
	s.Require().True(env.Status, "body: %s", r.BodyString())
	return env.Data, env.Pagination
}

func actions(rows []auditRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Action)
	}
	return out
}

func (s *AuditLogSuite) TestAuditTrailReadAPI() {
	tenant := s.InsertTenant("al-a", "Audit Log A").String()
	other := s.InsertTenant("al-b", "Audit Log B").String()
	admin := s.As(tenant)

	// ---- Drive a flow as tenant_admin: entity -> obligation type -> workflow
	// -> template -> start -> one instance update. ----
	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Acme", "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types", map[string]any{"name": "VAT", "code": "VAT-RET", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	var tmpl struct {
		ID string `json:"id"`
	}
	r = admin.POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation",
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5, "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &tmpl)

	admin.POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var instances []struct {
		ID string `json:"id"`
	}
	admin.GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &instances)
	s.Require().Len(instances, 1)
	tiID := instances[0].ID
	admin.PUT(s.T(), "/task-instances/"+tiID, map[string]any{"status": "in_progress"}).AssertStatus(s.T(), http.StatusOK)

	// ---- Full trail: newest first, actor resolved, workflow resolved. ----
	rows, pg := s.list(admin, "")
	s.Require().Equal(pagination{Total: 6, Limit: 100, Offset: 0}, pg)
	s.Require().Equal([]string{
		"task_instance.updated", "workflow.started", "workflow_task.created",
		"workflow.created", "obligation_type.created", "entity.created",
	}, actions(rows))

	// occurredAt strictly descending (ISO-8601 UTC sorts lexicographically); seq too.
	s.Require().True(sort.SliceIsSorted(rows, func(i, j int) bool { return rows[i].OccurredAt > rows[j].OccurredAt }))
	for i := 1; i < len(rows); i++ {
		s.Require().Greater(rows[i-1].Seq, rows[i].Seq)
	}

	for _, row := range rows {
		s.Require().NotEmpty(row.ID)
		s.Require().NotEmpty(row.ActorID)
		s.Require().NotNil(row.ActorName, "actor name is resolved at read time from the user row")
		s.Require().Equal("Test User", *row.ActorName) // the seeded session user's name
		s.Require().NotEmpty(row.RequestID)
		s.Require().Len(row.Hash, 64)
		s.Require().Regexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`, row.OccurredAt)
		var details map[string]any
		s.Require().NoError(json.Unmarshal(row.Details, &details), "details must be a JSON object")
	}

	byAction := map[string]auditRow{}
	for _, row := range rows {
		byAction[row.Action] = row
	}
	wantWorkflow := func(row auditRow) {
		s.Require().NotNil(row.WorkflowID, "%s must resolve its workflow", row.Action)
		s.Require().Equal(wf.ID, *row.WorkflowID)
		s.Require().NotNil(row.WorkflowName)
		s.Require().Equal("Monthly VAT", *row.WorkflowName)
	}
	wantWorkflow(byAction["workflow.created"])
	wantWorkflow(byAction["workflow.started"])
	wantWorkflow(byAction["workflow_task.created"])
	wantWorkflow(byAction["task_instance.updated"])
	s.Require().Equal("workflow", byAction["workflow.created"].ResourceType)
	s.Require().Equal(wf.ID, byAction["workflow.created"].ResourceID)
	s.Require().Equal("workflow_task", byAction["workflow_task.created"].ResourceType)
	s.Require().Equal(tmpl.ID, byAction["workflow_task.created"].ResourceID)
	s.Require().Equal("task_instance", byAction["task_instance.updated"].ResourceType)
	s.Require().Equal(tiID, byAction["task_instance.updated"].ResourceID)
	s.Require().Nil(byAction["entity.created"].WorkflowID)
	s.Require().Nil(byAction["entity.created"].WorkflowName)
	s.Require().Nil(byAction["obligation_type.created"].WorkflowID)
	// The instance update's whitelisted details carry the status transition.
	s.Require().Contains(string(byAction["task_instance.updated"].Details), "in_progress")
	// workflow.started counts, no free text.
	s.Require().Contains(string(byAction["workflow.started"].Details), "1")

	// ---- workflowId filter follows template + instance events up to the workflow. ----
	rows, pg = s.list(admin, "?workflowId="+wf.ID)
	s.Require().Equal(4, pg.Total)
	s.Require().Equal([]string{
		"task_instance.updated", "workflow.started", "workflow_task.created", "workflow.created",
	}, actions(rows))
	for _, row := range rows {
		s.Require().NotEqual("entity", row.ResourceType)
	}

	// ---- resourceType / resourceId / action narrow. ----
	rows, pg = s.list(admin, "?resourceType=entity")
	s.Require().Equal(1, pg.Total)
	s.Require().Equal("entity.created", rows[0].Action)
	s.Require().Equal(entity.ID, rows[0].ResourceID)

	rows, pg = s.list(admin, "?resourceId="+tiID)
	s.Require().Equal(1, pg.Total)
	s.Require().Equal("task_instance.updated", rows[0].Action)

	rows, pg = s.list(admin, "?action=workflow.started")
	s.Require().Equal(1, pg.Total)
	s.Require().Equal("workflow.started", rows[0].Action)

	// action repeats: any of the given actions (so a UI verb can map onto
	// several stored actions); unknown values simply match nothing; a blank
	// value is no filter; each value is capped at 60 characters.
	rows, pg = s.list(admin, "?action=workflow.started&action=workflow.created")
	s.Require().Equal(2, pg.Total)
	s.Require().Equal([]string{"workflow.started", "workflow.created"}, actions(rows))
	rows, pg = s.list(admin, "?action=workflow.started&action=nope")
	s.Require().Equal(1, pg.Total)
	s.Require().Equal([]string{"workflow.started"}, actions(rows))
	_, pg = s.list(admin, "?action=nope")
	s.Require().Zero(pg.Total)
	_, pg = s.list(admin, "?action=")
	s.Require().Equal(6, pg.Total)
	rows, pg = s.list(admin, "?action=task_instance.updated&action=entity.created&workflowId="+wf.ID)
	s.Require().Equal(1, pg.Total, "the action set composes with the other filters")
	s.Require().Equal([]string{"task_instance.updated"}, actions(rows))
	admin.GET(s.T(), "/audit-log?action="+strings.Repeat("a", 61)).AssertStatus(s.T(), http.StatusBadRequest)

	rows, pg = s.list(admin, "?workflowId="+wf.ID+"&resourceType=workflow")
	s.Require().Equal(2, pg.Total)
	s.Require().Equal([]string{"workflow.started", "workflow.created"}, actions(rows))

	// ---- from / to window (inclusive calendar days on occurred_at). ----
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	_, pg = s.list(admin, "?from="+today+"&to="+today)
	s.Require().Equal(6, pg.Total)
	_, pg = s.list(admin, "?from="+yesterday+"&to="+tomorrow)
	s.Require().Equal(6, pg.Total)
	_, pg = s.list(admin, "?from="+tomorrow)
	s.Require().Zero(pg.Total)
	_, pg = s.list(admin, "?to="+yesterday)
	s.Require().Zero(pg.Total)

	// ---- Pagination. ----
	rows, pg = s.list(admin, "?limit=2")
	s.Require().Len(rows, 2)
	s.Require().Equal(pagination{Total: 6, Limit: 2, Offset: 0}, pg)
	s.Require().Equal([]string{"task_instance.updated", "workflow.started"}, actions(rows))
	rows, pg = s.list(admin, "?limit=2&offset=4")
	s.Require().Len(rows, 2)
	s.Require().Equal(pagination{Total: 6, Limit: 2, Offset: 4}, pg)
	s.Require().Equal([]string{"obligation_type.created", "entity.created"}, actions(rows))

	// ---- Validation. ----
	admin.GET(s.T(), "/audit-log?from=2026-99-99").AssertStatus(s.T(), http.StatusBadRequest)
	admin.GET(s.T(), "/audit-log?from=yesterday").AssertStatus(s.T(), http.StatusBadRequest)
	admin.GET(s.T(), "/audit-log?resourceType=user").AssertStatus(s.T(), http.StatusBadRequest)
	admin.GET(s.T(), "/audit-log?workflowId=not-a-uuid").AssertStatus(s.T(), http.StatusBadRequest)
	admin.GET(s.T(), "/audit-log?limit=501").AssertStatus(s.T(), http.StatusBadRequest)

	// ---- Capability gate: audit:read is reviewer / manager / tenant_admin only. ----
	s.AsRole(tenant, "viewer").GET(s.T(), "/audit-log").AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "preparer").GET(s.T(), "/audit-log").AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "reviewer").GET(s.T(), "/audit-log").AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "manager").GET(s.T(), "/audit-log").AssertStatus(s.T(), http.StatusOK)
	s.AsUngranted(tenant).GET(s.T(), "/audit-log").AssertStatus(s.T(), http.StatusForbidden)
	s.Client.External().GET(s.T(), "/audit-log").AssertStatus(s.T(), http.StatusUnauthorized)

	// ---- RLS: the other tenant's trail is empty, even filtered by this workflow. ----
	rows, pg = s.list(s.As(other), "")
	s.Require().Empty(rows)
	s.Require().Zero(pg.Total)
	rows, pg = s.list(s.As(other), "?workflowId="+wf.ID)
	s.Require().Empty(rows)
	s.Require().Zero(pg.Total)
}
