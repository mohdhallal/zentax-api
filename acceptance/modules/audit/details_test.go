package audit_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// AuditDetailsSuite proves the ADR-0008 envelope records WHAT changed, not just
// that something did: for every resource whose history lives nowhere else
// (entities, entity obligations, workflows, workflow task templates) a create,
// an update and a delete carry the whitelisted fields that moved, each with its
// before and after value — and the hash chain still verifies across the richer
// payloads.
//
// It also pins the other half of the design: the whitelist is a whitelist.
// Names, descriptions, role labels, the tax reference number and every other
// free-text field never appear as values anywhere in the trail; the trail says
// only that they changed. (coverage_test.go plants personal data in the
// free-text fields and proves it stays out.)
type AuditDetailsSuite struct {
	acceptance.Suite
}

func TestAuditDetailsSuite(t *testing.T) {
	suite.Run(t, new(AuditDetailsSuite))
}

// ---- reading the trail ----

const trailColumns = `event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
                      occurred_at, request_id, details, prev_hash, hash`

// trail reads one tenant's whole chain, oldest first. audit_log is RLS'd, so
// the read needs the tenant GUC bound on its own transaction.
func (s *AuditDetailsSuite) trail(tenantID string) []audit.Entry {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)

	var entries []audit.Entry
	s.Require().NoError(tx.Select(&entries,
		`SELECT `+trailColumns+` FROM audit_log ORDER BY seq ASC`))
	return entries
}

// entryFor returns the single entry for an action on a resource.
func (s *AuditDetailsSuite) entryFor(entries []audit.Entry, action, resourceID string) audit.Entry {
	var found []audit.Entry
	for _, e := range entries {
		if e.Action == action && e.ResourceID == resourceID {
			found = append(found, e)
		}
	}
	s.Require().Len(found, 1, "expected exactly one %s for %s", action, resourceID)
	return found[0]
}

// fieldsOf decodes an entry's change set: field name -> {"from":…, "to":…}.
func (s *AuditDetailsSuite) fieldsOf(e audit.Entry) map[string]map[string]any {
	var payload struct {
		Fields map[string]map[string]any `json:"fields"`
	}
	s.Require().NoError(json.Unmarshal(e.Details, &payload),
		"details must decode: %s", string(e.Details))
	s.Require().NotEmpty(payload.Fields, "%s recorded no fields: %s", e.Action, string(e.Details))
	return payload.Fields
}

// requireChange asserts one field's recorded before and after. Pass nil for a
// side that must be absent (a create has no "from", a delete no "to").
func (s *AuditDetailsSuite) requireChange(e audit.Entry, field string, from, to any) {
	change, ok := s.fieldsOf(e)[field]
	s.Require().True(ok, "%s must record %s: %s", e.Action, field, string(e.Details))
	if from == nil {
		s.Require().NotContains(change, "from", "%s.%s must carry no before value", e.Action, field)
	} else {
		s.Require().Equal(from, change["from"], "%s.%s before value", e.Action, field)
	}
	if to == nil {
		s.Require().NotContains(change, "to", "%s.%s must carry no after value", e.Action, field)
	} else {
		s.Require().Equal(to, change["to"], "%s.%s after value", e.Action, field)
	}
}

// requireUnrecorded asserts a whitelisted field that did not move is absent:
// the payload's key set is the change set, not a row dump.
func (s *AuditDetailsSuite) requireUnrecorded(e audit.Entry, field string) {
	s.Require().NotContains(s.fieldsOf(e), field,
		"%s must not record the unchanged %s: %s", e.Action, field, string(e.Details))
}

// requireNoLeak asserts no entry in the chain quotes a value the envelope is
// meant to withhold (ADR-0007 / ADR-0008: no names, no free text, no tax ids).
func (s *AuditDetailsSuite) requireNoLeak(entries []audit.Entry, secrets ...string) {
	for _, e := range entries {
		for _, secret := range secrets {
			s.Require().NotContains(string(e.Details), secret,
				"seq %d (%s) leaked a withheld value", e.Seq, e.Action)
		}
	}
}

// ---- the tests ----

// An entity's fiscal calendar and an obligation's deadline rule are the two
// configurations that decide when a filing was legally due, and neither is
// versioned anywhere else. This walks create -> update -> delete over both.
func (s *AuditDetailsSuite) TestEntityAndObligationRecordBeforeAndAfter() {
	tenant := s.InsertTenant("aud-det-a", "Audit Details A").String()
	admin := s.As(tenant)

	legalName := "Acme Gesellschaft mit beschränkter Haftung"
	var entity, obType, obligation struct {
		ID string `json:"id"`
	}

	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Acme GmbH", "legalName": legalName, "country": "Germany",
		"taxResidency": "Germany", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "VAT", "code": "VAT-RET", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	// A monthly VAT obligation filed 20 days after period end, weekend-adjusted.
	r = admin.POST(s.T(), "/entity-obligations", map[string]any{
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"taxReferenceNumber": "DE123456789", "jurisdiction": "Germany",
		"currency": "EUR", "periodicity": "monthly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end", "offsetUnit": "days",
			"offsetValue": 20, "offsetDirection": "after",
			"weekendAdjustment": "next-business-day",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obligation)

	// The deadline moves to 45 days and the tax reference is cleared.
	admin.PUT(s.T(), "/entity-obligations/"+obligation.ID, map[string]any{
		"jurisdiction": "Germany", "currency": "EUR", "periodicity": "monthly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end", "offsetUnit": "days",
			"offsetValue": 45, "offsetDirection": "after",
			"weekendAdjustment": "next-business-day",
		},
		"status": "active",
	}).AssertStatus(s.T(), http.StatusOK)

	// The entity is re-cut onto a 454 retail calendar and archived, and renamed.
	admin.PUT(s.T(), "/entities/"+entity.ID, map[string]any{
		"name": "Acme Holding GmbH", "legalName": legalName, "country": "Germany",
		"taxResidency": "Germany", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "454", "fiscalWeekEndDay": "sunday",
		"fiscalYearEndRule": "last", "status": "archived",
	}).AssertStatus(s.T(), http.StatusOK)

	// And the obligation is deleted outright.
	admin.DELETE(s.T(), "/entity-obligations/"+obligation.ID).
		AssertStatus(s.T(), http.StatusNoContent)

	entries := s.trail(tenant)

	// ---- entity.created: the calendar it was born with, names withheld. ----
	created := s.entryFor(entries, "entity.created", entity.ID)
	s.requireChange(created, "country", nil, "set") // free text: dated, not quoted
	s.requireChange(created, "fiscalCalendarPattern", nil, "standard")
	s.requireChange(created, "financialYearEnd", nil, "12-31")
	s.requireChange(created, "name", nil, "set")
	s.requireChange(created, "legalName", nil, "set")

	// ---- entity.updated: the superseded calendar is recoverable. ----
	updated := s.entryFor(entries, "entity.updated", entity.ID)
	s.requireChange(updated, "fiscalCalendarPattern", "standard", "454")
	s.requireChange(updated, "fiscalWeekEndDay", "saturday", "sunday")
	s.requireChange(updated, "fiscalYearEndRule", "nearest", "last")
	s.requireChange(updated, "status", "active", "archived")
	s.requireChange(updated, "name", "set", "set") // renamed; value withheld
	s.requireUnrecorded(updated, "country")        // untouched
	s.requireUnrecorded(updated, "financialYearEnd")
	s.requireUnrecorded(updated, "legalName")

	// ---- entity_obligation.created: the rule that will compute the dates. ----
	obCreated := s.entryFor(entries, "entity_obligation.created", obligation.ID)
	s.requireChange(obCreated, "periodicity", nil, "monthly")
	s.requireChange(obCreated, "currency", nil, "EUR")
	s.requireChange(obCreated, "entityId", nil, entity.ID)
	s.requireChange(obCreated, "taxReferenceNumber", nil, "set")
	rule, ok := s.fieldsOf(obCreated)["deadlineRule"]["to"].(map[string]any)
	s.Require().True(ok, "the deadline rule must be recorded whole")
	s.Require().Equal(float64(20), rule["offsetValue"])
	s.Require().Equal("next-business-day", rule["weekendAdjustment"])

	// ---- entity_obligation.updated: both rules, and the cleared tax id. ----
	obUpdated := s.entryFor(entries, "entity_obligation.updated", obligation.ID)
	s.requireChange(obUpdated, "taxReferenceNumber", "set", "empty")
	s.requireUnrecorded(obUpdated, "periodicity")
	s.requireUnrecorded(obUpdated, "status")
	ruleChange := s.fieldsOf(obUpdated)["deadlineRule"]
	before, ok := ruleChange["from"].(map[string]any)
	s.Require().True(ok, "the superseded rule must be recorded: %v", ruleChange)
	after, ok := ruleChange["to"].(map[string]any)
	s.Require().True(ok, "the new rule must be recorded: %v", ruleChange)
	s.Require().Equal(float64(20), before["offsetValue"],
		"the rule that computed the already-filed periods")
	s.Require().Equal(float64(45), after["offsetValue"])

	// ---- entity_obligation.deleted: what left with it. ----
	obDeleted := s.entryFor(entries, "entity_obligation.deleted", obligation.ID)
	s.requireChange(obDeleted, "status", "active", nil)
	s.requireChange(obDeleted, "periodicity", "monthly", nil)
	gone, ok := s.fieldsOf(obDeleted)["deadlineRule"]["from"].(map[string]any)
	s.Require().True(ok, "the deleted obligation's rule must be recorded")
	s.Require().Equal(float64(45), gone["offsetValue"])

	// ---- The envelope stays PII-free, and the chain still verifies. ----
	s.requireNoLeak(entries, "Acme GmbH", "Acme Holding GmbH", legalName, "DE123456789")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")

	// ---- And an auditor sees it through the read API, not just in the table. ----
	resp := admin.GET(s.T(), "/audit-log?resourceType=entity_obligation&resourceId="+obligation.ID)
	resp.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Data []struct {
			Action  string          `json:"action"`
			Details json.RawMessage `json:"details"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(resp.BodyString()), &env))
	s.Require().Len(env.Data, 3, "created, updated and deleted: %s", resp.BodyString())
	for _, row := range env.Data {
		s.Require().Contains(string(row.Details), "deadlineRule",
			"%s must reach the auditor with the rule attached", row.Action)
	}
	s.Require().Contains(string(env.Data[1].Details), `"offsetValue":20`,
		"the update must hand the auditor the superseded rule")
}

// A workflow's status and due-date rule re-date work already in flight; a task
// template's approval requirement is the segregation-of-duties switch. Neither
// is versioned anywhere else either.
func (s *AuditDetailsSuite) TestWorkflowAndTemplateRecordBeforeAndAfter() {
	tenant := s.InsertTenant("aud-det-b", "Audit Details B").String()
	admin := s.As(tenant)

	var entity, obType, wf, task struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Globex SA", "country": "France", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "CIT", "code": "CIT-RET", "template": "CIT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "Monthly CIT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2026", "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days",
			"offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = admin.POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare return", "taskType": "preparation",
		"roleLabel": "Maria's review", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
		"requiredDocuments": []map[string]any{
			{"name": "Signed CIT return", "required": true},
			{"name": "Trial balance", "required": false},
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &task)

	// The workflow goes live, gains a period and loses 15 days of slack.
	admin.PUT(s.T(), "/workflows/"+wf.ID, map[string]any{
		"name": "Monthly CIT (revised)", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2026",
		"selectedPeriods": []string{"M1", "M2"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days",
			"offsetValue": 30, "offsetDirection": "after",
		},
		"tasksSequential": true, "status": "active",
	}).AssertStatus(s.T(), http.StatusOK)

	// And the template loses its approval requirement.
	admin.PUT(s.T(), "/workflow-tasks/"+task.ID, map[string]any{
		"name": "Prepare return", "taskType": "preparation",
		"approvalRequired": false,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 10,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
		"requiredDocuments": []map[string]any{
			{"name": "Signed CIT return", "required": false},
		},
	}).AssertStatus(s.T(), http.StatusOK)

	entries := s.trail(tenant)

	// ---- workflow.created ----
	created := s.entryFor(entries, "workflow.created", wf.ID)
	s.requireChange(created, "status", nil, "draft")
	s.requireChange(created, "periodicity", nil, "monthly")
	s.requireChange(created, "entityId", nil, entity.ID)
	s.requireChange(created, "name", nil, "set")
	s.Require().Equal([]any{"M1"}, s.fieldsOf(created)["selectedPeriods"]["to"])

	// ---- workflow.updated: the lifecycle step and the re-dating. ----
	updated := s.entryFor(entries, "workflow.updated", wf.ID)
	s.requireChange(updated, "status", "draft", "active")
	s.requireChange(updated, "tasksSequential", false, true)
	s.requireChange(updated, "name", "set", "set")
	s.requireUnrecorded(updated, "periodicity")
	s.requireUnrecorded(updated, "description") // never set on either side
	s.Require().Equal([]any{"M1"}, s.fieldsOf(updated)["selectedPeriods"]["from"])
	s.Require().Equal([]any{"M1", "M2"}, s.fieldsOf(updated)["selectedPeriods"]["to"])
	ruleChange := s.fieldsOf(updated)["dueDateRule"]
	oldRule, ok := ruleChange["from"].(map[string]any)
	s.Require().True(ok, "the superseded due-date rule must be recorded: %v", ruleChange)
	newRule, ok := ruleChange["to"].(map[string]any)
	s.Require().True(ok, "the new due-date rule must be recorded: %v", ruleChange)
	s.Require().Equal(float64(15), oldRule["offsetValue"])
	s.Require().Equal(float64(30), newRule["offsetValue"])

	// ---- workflow_task.created / .updated: the SoD switch and the offsets. ----
	taskCreated := s.entryFor(entries, "workflow_task.created", task.ID)
	s.requireChange(taskCreated, "approvalRequired", nil, true)
	s.requireChange(taskCreated, "taskType", nil, "preparation")
	s.requireChange(taskCreated, "dueDateOffsetValue", nil, float64(5))
	s.requireChange(taskCreated, "workflowId", nil, wf.ID)
	s.requireChange(taskCreated, "roleLabel", nil, "set")
	s.Require().Equal(map[string]any{"count": float64(2), "mandatory": float64(1)},
		s.fieldsOf(taskCreated)["requiredDocuments"]["to"])

	taskUpdated := s.entryFor(entries, "workflow_task.updated", task.ID)
	s.requireChange(taskUpdated, "approvalRequired", true, false)
	s.requireChange(taskUpdated, "dueDateOffsetValue", float64(5), float64(10))
	s.requireChange(taskUpdated, "roleLabel", "set", "empty")
	s.requireUnrecorded(taskUpdated, "taskType")
	s.requireUnrecorded(taskUpdated, "name")
	s.Require().Equal(map[string]any{"count": float64(2), "mandatory": float64(1)},
		s.fieldsOf(taskUpdated)["requiredDocuments"]["from"])
	s.Require().Equal(map[string]any{"count": float64(1), "mandatory": float64(0)},
		s.fieldsOf(taskUpdated)["requiredDocuments"]["to"])

	// ---- Still PII-free, still a verifiable chain. ----
	s.requireNoLeak(entries, "Monthly CIT", "Monthly CIT (revised)", "Prepare return",
		"Maria's review", "Signed CIT return", "Trial balance", "Globex SA")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}
