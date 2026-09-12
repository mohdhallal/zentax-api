package audit_test

import (
	"encoding/json"
	"net/http"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// The second half of the ADR-0008 envelope, for the four resources the first
// pass left writing status-only or empty payloads: task instances (the object
// that carries the figures), obligation types (the reference data that defines
// what an obligation IS), and a member's grants, re-issued invite and
// activation (the segregation-of-duties record, which survives nowhere else
// because user_grants is hard-deleted).
//
// It also pins the rule the whitelists now claim: a value is quoted only when
// the request contract constrains its shape. Country, tax residency,
// jurisdiction and jurisdiction state are `max=100` free text, so they are
// withheld like a name — see TestFreeTextNeverReachesTheEnvelope.

// entriesFor returns every entry for an action on a resource, oldest first —
// the repeated-edit counterpart of entryFor.
func (s *AuditDetailsSuite) entriesFor(entries []audit.Entry, action, resourceID string) []audit.Entry {
	var found []audit.Entry
	for _, e := range entries {
		if e.Action == action && e.ResourceID == resourceID {
			found = append(found, e)
		}
	}
	return found
}

// taxDataShapeOf decodes the tax record's shape summary, which sits BESIDE the
// `fields` envelope: `fields` maps a field name to exactly one {from, to} pair,
// and a shape is not a before/after value.
func (s *AuditDetailsSuite) taxDataShapeOf(e audit.Entry) map[string]any {
	var payload struct {
		TaxData map[string]any `json:"taxData"`
	}
	s.Require().NoError(json.Unmarshal(e.Details, &payload),
		"details must decode: %s", string(e.Details))
	s.Require().NotEmpty(payload.TaxData,
		"%s recorded no tax-data shape: %s", e.Action, string(e.Details))
	return payload.TaxData
}

// ---- task instances ----

// A task instance is where the figures live, and ADR-0018 freezes it only once
// it is submitted — so the pre-approval drafting window, when the numbers are
// actually entered, is exactly where the trail has to work. This walks a real
// edit of a tax figure with the status left alone: the change that used to be
// byte-identical to a status nudge.
func (s *AuditDetailsSuite) TestTaskInstanceRecordsTheEditAndShapesTheFigures() {
	tenant := s.InsertTenant("aud-det-ti", "Audit Details TI").String()
	admin := s.As(tenant)

	var entity, obType, wf, tpl, task struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Initech BV", "country": "Netherlands", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "VAT", "code": "VAT-NL", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	// A typed schema, so the recorded keys are real template field ids and the
	// figures go through ADR-0001 validation on the way in.
	r = admin.POST(s.T(), "/data-templates", map[string]any{
		"name": "NL VAT figures", "templateType": "Custom",
		"fields": []map[string]any{
			{"id": "salesTotal", "name": "Total Sales (net)", "fieldType": "numeric", "mandatory": false},
			{"id": "vatDue", "name": "VAT due", "fieldType": "numeric", "mandatory": false},
			{"id": "carryForward", "name": "Carried forward", "fieldType": "numeric", "mandatory": false},
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &tpl)

	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days",
			"offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = admin.POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare return", "taskType": "preparation",
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
		"dataTemplateId": tpl.ID,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &task)

	admin.POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var instances []struct {
		ID      string `json:"id"`
		DueDate string `json:"dueDate"`
		Status  string `json:"status"`
	}
	admin.GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &instances)
	s.Require().Len(instances, 1)
	instance := instances[0]
	s.Require().Equal("not_started", instance.Status)

	// Edit one: work starts, the figures are entered, a note is left.
	const salesTotal, vatFirst = 481516.23, 2342.07
	admin.PUT(s.T(), "/task-instances/"+instance.ID, map[string]any{
		"status": "in_progress", "notes": "first pass from the ledger export",
		"taxData": map[string]any{"salesTotal": salesTotal, "vatDue": vatFirst},
	}).AssertStatus(s.T(), http.StatusOK)

	// Edit two — the one that used to leave no trace at all: the status does
	// not move, a tax figure is rewritten, a figure is added and the legal due
	// date is overridden.
	const vatCorrected, carryForward = 2601.88, 77.19
	admin.PUT(s.T(), "/task-instances/"+instance.ID, map[string]any{
		"status": "in_progress", "dueDate": "2025-11-30",
		"notes": "corrected after the reconciliation",
		"taxData": map[string]any{
			"salesTotal": salesTotal, "vatDue": vatCorrected, "carryForward": carryForward,
		},
	}).AssertStatus(s.T(), http.StatusOK)

	entries := s.trail(tenant)
	updates := s.entriesFor(entries, "task_instance.updated", instance.ID)
	s.Require().Len(updates, 2)

	// ---- the first edit: status, notes and the first two figures. ----
	s.requireChange(updates[0], "status", "not_started", "in_progress")
	s.requireChange(updates[0], "notes", "empty", "set")
	s.requireUnrecorded(updates[0], "dueDate")
	s.Require().Equal(map[string]any{
		"figuresBefore": float64(0), "figuresAfter": float64(2),
		"changedKeys": []any{"salesTotal", "vatDue"},
	}, s.taxDataShapeOf(updates[0]))

	// ---- the second edit: no status change, and the trail still says what
	// moved — which is the entire point.
	s.requireUnrecorded(updates[1], "status")
	s.requireChange(updates[1], "dueDate", instance.DueDate, "2025-11-30")
	s.requireChange(updates[1], "notes", "set", "set")
	s.Require().Equal(map[string]any{
		"figuresBefore": float64(2), "figuresAfter": float64(3),
		"changedKeys": []any{"carryForward", "vatDue"},
	}, s.taxDataShapeOf(updates[1]),
		"a rewritten figure must be named, and salesTotal did not move")

	// ---- The figures themselves never reach the log, nor the notes. ----
	s.requireNoLeak(entries,
		"481516.23", "2342.07", "2601.88", "77.19",
		"first pass from the ledger export", "corrected after the reconciliation")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// ---- obligation types ----

// An obligation type says what a tax obligation IS — the template every
// workflow computes from and every report groups by. Nothing versions it, and
// the entity-obligation envelope records only its id, so create / update /
// delete here is the whole history of a definition.
func (s *AuditDetailsSuite) TestObligationTypeRecordsBeforeAndAfter() {
	tenant := s.InsertTenant("aud-det-ot", "Audit Details OT").String()
	admin := s.As(tenant)

	const name, code, renamed = "DAC6 disclosure", "DAC6", "DAC6-EU"
	description := "Reportable cross-border arrangements"

	var obType struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/obligation-types", map[string]any{
		"name": name, "code": code, "template": "Custom", "description": description,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	// Re-pointed at another engine, re-coded, retired, and the note dropped.
	admin.PUT(s.T(), "/obligation-types/"+obType.ID, map[string]any{
		"name": name, "code": renamed, "template": "CIT", "status": "inactive",
	}).AssertStatus(s.T(), http.StatusOK)

	admin.DELETE(s.T(), "/obligation-types/"+obType.ID).AssertStatus(s.T(), http.StatusNoContent)

	entries := s.trail(tenant)

	// ---- created: what the definition was born as. ----
	created := s.entryFor(entries, "obligation_type.created", obType.ID)
	s.requireChange(created, "template", nil, "Custom")
	s.requireChange(created, "category", nil, "custom")
	s.requireChange(created, "status", nil, "active")
	s.requireChange(created, "code", nil, "set")
	s.requireChange(created, "name", nil, "set")
	s.requireChange(created, "description", nil, "set")

	// ---- updated: the superseded definition, which exists nowhere else. ----
	updated := s.entryFor(entries, "obligation_type.updated", obType.ID)
	s.requireChange(updated, "template", "Custom", "CIT")
	s.requireChange(updated, "status", "active", "inactive")
	s.requireChange(updated, "code", "set", "set")          // re-coded; value withheld
	s.requireChange(updated, "description", "set", "empty") // dropped
	s.requireUnrecorded(updated, "category")                // untouched
	s.requireUnrecorded(updated, "name")

	// ---- deleted: what left with it. ----
	deleted := s.entryFor(entries, "obligation_type.deleted", obType.ID)
	s.requireChange(deleted, "template", "CIT", nil)
	s.requireChange(deleted, "status", "inactive", nil)
	s.requireChange(deleted, "code", "set", nil)

	s.requireNoLeak(entries, name, code, renamed, description)
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// ---- members ----

// The access-review envelope. user_grants is hard-deleted — no revoked_at, no
// history table — so a replaced or removed grant survives only here, and "what
// could this person do before?" is answerable from the trail or nowhere.
func (s *AuditDetailsSuite) TestMemberGrantChangesRecordThePriorGrants() {
	tenant := s.InsertTenant("aud-det-mb", "Audit Details MB").String()
	admin := s.As(tenant)

	var entity struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Globex SARL", "country": "France", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	const email, memberName, renamed = "carol@audit.test", "Carol Preparer", "Carol P."

	// Invite → accept: the activation is a real transition, and it used to
	// record nothing at all.
	var invited struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
		InviteToken string `json:"inviteToken"`
	}
	r = admin.POST(s.T(), "/members",
		map[string]any{"email": email, "name": memberName, "role": "preparer"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &invited)
	memberID := invited.Member.ID

	s.Client.External().POST(s.T(), "/auth/accept-invite", map[string]any{
		"token": invited.InviteToken, "password": "correct-horse-battery-staple",
	}).AssertStatus(s.T(), http.StatusOK)

	// The segregation-of-duties edit: preparer → manager, replacing every grant.
	admin.PUT(s.T(), "/members/"+memberID+"/role",
		map[string]any{"role": "manager"}).AssertStatus(s.T(), http.StatusOK)

	// A second, scoped grant added, then taken away again.
	var withGrant struct {
		Grants []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"grants"`
	}
	r = admin.POST(s.T(), "/members/"+memberID+"/grants",
		map[string]any{"role": "reviewer", "scopeEntityId": entity.ID})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &withGrant)
	var scopedGrantID string
	for _, g := range withGrant.Grants {
		if g.Role == "reviewer" {
			scopedGrantID = g.ID
		}
	}
	s.Require().NotEmpty(scopedGrantID)
	admin.DELETE(s.T(), "/members/"+memberID+"/grants/"+scopedGrantID).
		AssertStatus(s.T(), http.StatusNoContent)

	// And the member is renamed and disabled.
	admin.PUT(s.T(), "/members/"+memberID,
		map[string]any{"name": renamed, "status": "disabled"}).AssertStatus(s.T(), http.StatusOK)

	// A second member, left invited, whose invite is re-issued.
	var pending struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
	}
	r = admin.POST(s.T(), "/members",
		map[string]any{"email": "dana@audit.test", "name": "Dana Viewer", "role": "viewer"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &pending)
	admin.POST(s.T(), "/members/"+pending.Member.ID+"/invite", nil).
		AssertStatus(s.T(), http.StatusCreated)

	entries := s.trail(tenant)

	preparer := []any{map[string]any{"role": "preparer"}}
	manager := []any{map[string]any{"role": "manager"}}
	managerPlusScoped := []any{
		map[string]any{"role": "manager"},
		map[string]any{"role": "reviewer", "scopeEntityId": entity.ID},
	}

	// ---- activation: invited → active, and a credential now exists. ----
	activated := s.entryFor(entries, "member.activated", memberID)
	s.requireChange(activated, "status", "invited", "active")
	s.requireChange(activated, "hasPassword", false, true)
	s.requireUnrecorded(activated, "name") // the invite carried no rename

	// ---- the three grant changes, each with the grant set it replaced. ----
	roleChanges := s.entriesFor(entries, "member.role_changed", memberID)
	s.Require().Len(roleChanges, 3)
	s.requireChange(roleChanges[0], "grants", preparer, manager)
	s.requireChange(roleChanges[1], "grants", manager, managerPlusScoped)
	s.requireChange(roleChanges[2], "grants", managerPlusScoped, manager)

	// ---- the rename and the disable. ----
	updated := s.entryFor(entries, "member.updated", memberID)
	s.requireChange(updated, "status", "active", "disabled")
	s.requireChange(updated, "name", "set", "set")
	s.requireUnrecorded(updated, "grants")

	// ---- the re-issued invite: a credential window, and what it unlocks. ----
	reissued := s.entryFor(entries, "member.invite_reissued", pending.Member.ID)
	s.requireChange(reissued, "status", nil, "invited")
	s.Require().Equal([]any{map[string]any{"role": "viewer"}},
		s.fieldsOf(reissued)["grants"]["to"])
	window, ok := s.fieldsOf(reissued)["inviteExpiresAt"]
	s.Require().True(ok, "the re-issue must date the credential window it opened: %s",
		string(reissued.Details))
	s.Require().NotEmpty(window["to"])

	// ---- No personal data, and the chain still verifies. ----
	s.requireNoLeak(entries, email, memberName, renamed,
		"dana@audit.test", "Dana Viewer", "Globex SARL")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// ---- the free-text rule ----

// The whitelists claim a value is quoted only when the request contract
// constrains its shape. Country, taxResidency, jurisdiction and
// jurisdictionState are `max=100` free text with no code list behind them, so
// this plants a person, an address, a phone number and two email addresses in
// those four fields and proves none of them reaches the envelope — while the
// change itself is still recorded and dated.
func (s *AuditDetailsSuite) TestFreeTextNeverReachesTheEnvelope() {
	tenant := s.InsertTenant("aud-det-ft", "Audit Details FT").String()
	admin := s.As(tenant)

	const (
		person       = "Jane Doe, 12 Hauptstrasse, Berlin"
		personEmail  = "jane.doe@example.com"
		agent        = "contact Herr Klaus Weber, +49 170 1234567"
		agentEmail   = "klaus.weber@probe.example"
		movedCountry = "Grand Duchy of Luxembourg"
	)

	var entity, obType, obligation struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Probe SA", "country": person, "taxResidency": personEmail,
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "WHT", "code": "WHT-PROBE", "template": "WHT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = admin.POST(s.T(), "/entity-obligations", map[string]any{
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"jurisdiction": agent, "jurisdictionState": agentEmail,
		"currency": "EUR", "periodicity": "quarterly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end", "offsetUnit": "days",
			"offsetValue": 30, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obligation)

	// Moving the country is a real, recordable change — just not a quotable one.
	admin.PUT(s.T(), "/entities/"+entity.ID, map[string]any{
		"name": "Probe SA", "country": movedCountry, "taxResidency": personEmail,
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	}).AssertStatus(s.T(), http.StatusOK)

	entries := s.trail(tenant)

	created := s.entryFor(entries, "entity.created", entity.ID)
	s.requireChange(created, "country", nil, "set")
	s.requireChange(created, "taxResidency", nil, "set")

	updated := s.entryFor(entries, "entity.updated", entity.ID)
	s.requireChange(updated, "country", "set", "set")
	s.requireUnrecorded(updated, "taxResidency") // unchanged

	obCreated := s.entryFor(entries, "entity_obligation.created", obligation.ID)
	s.requireChange(obCreated, "jurisdiction", nil, "set")
	s.requireChange(obCreated, "jurisdictionState", nil, "set")
	s.requireChange(obCreated, "currency", nil, "EUR") // len=3, uppercase: quotable

	s.requireNoLeak(entries, person, personEmail, agent, agentEmail, movedCountry, "Probe SA")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}
