package audit_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// The fourth pass over the ADR-0008 envelope: the links and the last free text.
//
// Three of the gaps it closes are the same gap in different modules — a value
// the contract bounds by LENGTH and nothing else, quoted verbatim into an
// append-only, hash-chained ledger that sits outside the ADR-0007 erasure
// boundary. The other three are the reverse: an identifier the envelope needs
// and did not carry, so a reader could see that something happened and not what
// it happened to.
//
//   - a workflow quoted its financialYear (`max=9`) and every selectedPeriods
//     entry (`dive,max=16`) — the SAME period codes the entity envelope already
//     gates, so one tenant-typed string entered the chain through one door and
//     was withheld through the other;
//   - an entity delete revoked RBAC grants and recorded only how many;
//   - a document and each new version recorded neither the workflow nor the
//     task instance the evidence was filed against — the link an auditor
//     follows, and the one a workflow delete destroys.

// ---- the last verbatim free text: a workflow's periods ----

// The gate is the entity module's, applied to the fields that JOIN to it:
// selectedPeriods names the codes entities.customPeriods defines, and quoting
// them here while gating them there was the inconsistency, not the length.
//
// Sixteen characters is a narrow window and wide enough for an email address,
// which is the whole argument: a length bound is not a shape.
func (s *AuditDetailsSuite) TestWorkflowPeriodsAndYearAreShapeGated() {
	tenant := s.InsertTenant("aud-det-wfp", "Audit Details WFP").String()
	admin := s.As(tenant)

	const (
		yearContact  = "j@doe.com" // exactly 9: inside `max=9`
		periodPerson = "jane@x.io" // 9: inside `dive,max=16`
		periodPhrase = "Q1 draft"  // 8, but carries a space
		periodReal   = "Q4-adj"    // a code a real cut uses
		yearReal     = "2026-27"   // and a year one does
	)

	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Hooli Ltd", "country": "Ireland", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "VAT", "code": "VAT-IE", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	// Born with a contact address where the fiscal year belongs, and two
	// people in the period list beside one real code.
	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "Quarterly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "quarterly", "financialYear": yearContact,
		"selectedPeriods": []string{periodPerson, periodPhrase, periodReal},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days",
			"offsetValue": 20, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	// Then corrected to real values, and deleted — so all three envelopes are
	// exercised, including the two-sided one where a redaction has to compare.
	admin.PUT(s.T(), "/workflows/"+wf.ID, map[string]any{
		"name": "Quarterly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "quarterly", "financialYear": yearReal,
		"selectedPeriods": []string{"Q1", "Q2"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days",
			"offsetValue": 20, "offsetDirection": "after",
		},
		"status": "draft",
	}).AssertStatus(s.T(), http.StatusOK)

	admin.DELETE(s.T(), "/workflows/"+wf.ID).AssertStatus(s.T(), http.StatusNoContent)

	entries := s.trail(tenant)

	// ---- created: the address is withheld, the real code survives. ----
	created := s.entryFor(entries, "workflow.created", wf.ID)
	s.requireChange(created, "financialYear", nil, "redacted")
	s.Require().Equal([]any{"redacted", "redacted", periodReal},
		s.fieldsOf(created)["selectedPeriods"]["to"],
		"a code that looks like a code is still quoted — the gate is a shape, not a ban")

	// ---- updated: both sides gated, and the change is still detected. ----
	updated := s.entryFor(entries, "workflow.updated", wf.ID)
	s.requireChange(updated, "financialYear", "redacted", yearReal)
	s.Require().Equal([]any{"redacted", "redacted", periodReal},
		s.fieldsOf(updated)["selectedPeriods"]["from"])
	s.Require().Equal([]any{"Q1", "Q2"}, s.fieldsOf(updated)["selectedPeriods"]["to"])

	// ---- deleted: the one-sided envelope carries the gated before side. ----
	deleted := s.entryFor(entries, "workflow.deleted", wf.ID)
	s.requireChange(deleted, "financialYear", yearReal, nil)
	s.Require().Equal([]any{"Q1", "Q2"}, s.fieldsOf(deleted)["selectedPeriods"]["from"])

	// ---- Nothing a person typed reached the chain, and it still verifies. ----
	s.requireNoLeak(entries, yearContact, periodPerson, periodPhrase, "Quarterly VAT", "Hooli Ltd")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the gated envelopes")
}

// ---- the access an entity delete takes away ----

// Deleting an entity cascades every RBAC grant scoped to it out of user_grants
// — hard-deleted, no revoked_at, no history table, no per-row entry. The
// envelope recorded userGrantsRevoked: 2 and nothing about whose access, at
// which role, on the one operation nothing can undo. Recovering it by scanning
// earlier member.role_changed entries works only for grants made through the
// grant routes; a grant made at invite time is not there.
func (s *AuditDetailsSuite) TestEntityDeleteNamesTheGrantsItRevoked() {
	tenant := s.InsertTenant("aud-det-grants", "Audit Details GRANTS").String()
	admin := s.As(tenant)

	var entity, keep struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Umbrella SARL", "country": "Luxembourg", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/entities", map[string]any{
		"name": "Umbrella Holding", "country": "Luxembourg", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &keep)

	// Two principals scoped to the doomed entity, granted two different ways:
	// one at invite time (the case that is unreconstructable from anywhere
	// else) and one through the grants route. A third grant, on the entity that
	// survives, must NOT appear in the envelope.
	invite := func(email, name, role, scope string) string {
		var inv struct {
			Member struct {
				ID string `json:"id"`
			} `json:"member"`
		}
		body := map[string]any{"email": email, "name": name, "role": role}
		if scope != "" {
			body["scopeEntityId"] = scope
		}
		resp := admin.POST(s.T(), "/members", body)
		resp.AssertStatus(s.T(), http.StatusCreated)
		resp.DecodeData(s.T(), &inv)
		return inv.Member.ID
	}

	atInvite := invite("scoped.preparer@audit.test", "Scoped Preparer", "preparer", entity.ID)
	viaRoute := invite("later.reviewer@audit.test", "Later Reviewer", "viewer", "")
	admin.POST(s.T(), "/members/"+viaRoute+"/grants",
		map[string]any{"role": "reviewer", "scopeEntityId": entity.ID}).
		AssertStatus(s.T(), http.StatusCreated)
	admin.POST(s.T(), "/members/"+viaRoute+"/grants",
		map[string]any{"role": "manager", "scopeEntityId": keep.ID}).
		AssertStatus(s.T(), http.StatusCreated)

	admin.DELETE(s.T(), "/entities/"+entity.ID).AssertStatus(s.T(), http.StatusNoContent)

	entries := s.trail(tenant)
	deleted := s.entryFor(entries, "entity.deleted", entity.ID)

	// The count still runs — and the list is what it could never say.
	s.Require().Equal(float64(2), s.countOf(deleted, "userGrantsRevoked"))
	var payload struct {
		RevokedGrants []map[string]any `json:"revokedGrants"`
	}
	s.Require().NoError(json.Unmarshal(deleted.Details, &payload))
	s.Require().Equal([]map[string]any{
		{"userId": atInvite, "role": "preparer"},
		{"userId": viaRoute, "role": "reviewer"},
	}, payload.RevokedGrants,
		"whose access was removed, at which role — ordered so equal revocations record equally")
	s.Require().Len(payload.RevokedGrants, int(s.countOf(deleted, "userGrantsRevoked")),
		"the list and the census must agree")

	// The grant on the surviving entity is not swept in, and the entity's own
	// whitelist still sits beside the census in the same envelope.
	s.requireChange(deleted, "fiscalCalendarPattern", "standard", nil)
	s.requireChange(deleted, "name", "set", nil)

	// The revoked rows really are gone, so the envelope is now the only copy.
	s.Require().Equal(0, s.countScopedGrants(tenant, entity.ID),
		"user_grants is hard-deleted by the cascade — no revoked_at, no history table")

	s.requireNoLeak(entries, "Umbrella SARL", "Umbrella Holding",
		"scoped.preparer@audit.test", "Scoped Preparer",
		"later.reviewer@audit.test", "Later Reviewer")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// ---- the link a workflow delete destroys ----

// A document entry is evidence that a file was filed. It recorded the type, the
// category, the version and the size, and neither the workflow nor the task
// instance it was filed against — while document.updated and document.deleted
// recorded both, so the module was mixed-shape and the half that lacked the
// pointer was the half a cascade leaves behind.
//
// This walks the case that makes it matter: upload, add a version, then delete
// the workflow. The delete hard-cascades documents and document_versions with
// no per-row entry, so from that instant resource_id points at nothing and the
// create/version envelopes are the only record of what the evidence belonged
// to.
func (s *AuditDetailsSuite) TestDocumentEnvelopesOutliveTheWorkflowTheyBelongedTo() {
	tenant := s.InsertTenant("aud-det-doc", "Audit Details DOC").String()
	admin := s.As(tenant)

	var entity, obType, wf, task struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Vandelay Industries", "country": "Latvia", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "VAT", "code": "VAT-LV", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
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
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &task)

	admin.POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)
	var instances []struct {
		ID string `json:"id"`
	}
	admin.GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &instances)
	s.Require().Len(instances, 1)
	instance := instances[0].ID

	const label, notes = "Jane Doe assessment", "call Jane on 555-0100"
	v1, v2 := []byte("%PDF-1.4 first"), []byte("%PDF-1.4 second cut")

	var doc struct {
		ID string `json:"id"`
	}
	r = admin.POSTMultipart(s.T(), "/workflows/"+wf.ID+"/documents",
		map[string]string{
			"documentType": "draft_return", "category": "compliance",
			"label": label, "notes": notes, "taskInstanceId": instance,
		},
		acceptance.MultipartFile{FileName: "return.pdf", ContentType: "application/pdf", Content: v1})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &doc)

	admin.POSTMultipart(s.T(), "/documents/"+doc.ID+"/versions", nil,
		acceptance.MultipartFile{FileName: "return-v2.pdf", ContentType: "application/pdf", Content: v2}).
		AssertStatus(s.T(), http.StatusCreated)

	// The cascade: the workflow goes, and it takes the document rows with it.
	admin.DELETE(s.T(), "/workflows/"+wf.ID).AssertStatus(s.T(), http.StatusNoContent)
	admin.GET(s.T(), "/documents/"+doc.ID).AssertStatus(s.T(), http.StatusNotFound)

	entries := s.trail(tenant)

	created := s.entryFor(entries, "document.created", doc.ID)
	s.requireChange(created, "workflowId", nil, wf.ID)
	s.requireChange(created, "taskInstanceId", nil, instance)
	s.requireChange(created, "documentType", nil, "draft_return")
	s.requireChange(created, "label", nil, "set")
	s.Require().Equal(hexDigest(v1), s.stringBeside(created, "sha256"),
		"the ledger attests WHICH bytes were filed")

	added := s.entryFor(entries, "document.version_added", doc.ID)
	s.requireChange(added, "workflowId", nil, wf.ID)
	s.requireChange(added, "taskInstanceId", nil, instance)
	s.Require().Equal(hexDigest(v2), s.stringBeside(added, "sha256"))

	// The workflow census counted them, and the census is all it says — which
	// is exactly why the per-document entries have to carry the pointer.
	wfDeleted := s.entryFor(entries, "workflow.deleted", wf.ID)
	s.Require().Equal(float64(1), s.countOf(wfDeleted, "documents"))
	s.Require().Equal(float64(2), s.countOf(wfDeleted, "documentVersions"))

	s.requireNoLeak(entries, label, notes, "return.pdf", "return-v2.pdf",
		"Vandelay Industries", "Monthly VAT")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// hexDigest is the SHA-256 the upload path computes, so the test derives the
// expected digest from the bytes rather than copying it out of the response.
func hexDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// stringBeside reads a string that sits BESIDE the change set — a version fact
// on an upload envelope. `fields` maps a field name to a {from, to} pair, and a
// digest is not a before/after value, so the two never collide. It is the
// string counterpart of countOf.
func (s *AuditDetailsSuite) stringBeside(e audit.Entry, key string) string {
	var payload map[string]any
	s.Require().NoError(json.Unmarshal(e.Details, &payload),
		"details must decode: %s", string(e.Details))
	value, ok := payload[key].(string)
	s.Require().True(ok, "%s must record %s: %s", e.Action, key, string(e.Details))
	return value
}

// countScopedGrants reads how many RBAC grants are still scoped to an entity.
// user_grants is RLS'd, so the read needs the tenant GUC bound on its own
// transaction — the same shape `trail` uses for audit_log. Without it the query
// would return zero for any tenant and the assertion above would prove nothing.
func (s *AuditDetailsSuite) countScopedGrants(tenantID, entityID string) int {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)

	var n int
	s.Require().NoError(tx.Get(&n,
		`SELECT COUNT(*)::int FROM user_grants WHERE scope_entity_id = $1`, entityID))
	return n
}

// ---- the statutory date a human replaced when the period was materialised ----

// Starting a workflow writes the instances in bulk, so the start entry is the
// only record of the run. It used to carry three integers, one of them the
// COUNT of overrides — a filing deadline moved, without saying which one, from
// what, or to what, on the operation that fixes every statutory date in the
// period. The count is still there; beside it now sits each substitution.
func (s *AuditDetailsSuite) TestStartRecordsWhichStatutoryDatesWereReplaced() {
	tenant := s.InsertTenant("aud-det-ovr", "Audit Details OVR").String()
	admin := s.As(tenant)

	var entity, obType, wf, task struct {
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

	// One quarter, one template, a filing deadline 20 days after the period end
	// — so the computed due date is arithmetic the test can state exactly.
	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "VAT Q1", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "quarterly", "financialYear": "2026",
		"selectedPeriods": []string{"Q1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days",
			"offsetValue": 20, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = admin.POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "File the return", "taskType": "submission",
		"orderIndex": 1, "dueDateReference": "filing_deadline",
		"dueDateOffsetUnit": "days", "dueDateOffsetValue": 0, "dueDateOffsetDirection": "after",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &task)

	// Q1 ends 2026-03-31, so the rule computes 2026-04-20. The filer moves it.
	const substituted = "2026-05-04"
	admin.POST(s.T(), "/workflows/"+wf.ID+"/start", map[string]any{
		"taskOverrides": map[string]any{
			task.ID + "_Q1": map[string]any{"dueDate": substituted},
		},
	}).AssertStatus(s.T(), http.StatusCreated)

	entries := s.trail(tenant)
	started := s.entryFor(entries, "workflow.started", wf.ID)
	details := s.detailsOf(started)

	s.Require().EqualValues(1, details["overrides"], "the count stays")
	applied, ok := details["appliedOverrides"].([]any)
	s.Require().True(ok, "the start entry must name the substitutions, not only count them")
	s.Require().Len(applied, 1)

	one, ok := applied[0].(map[string]any)
	s.Require().True(ok)
	s.Require().Equal(task.ID, one["templateId"])
	s.Require().Equal("Q1", one["periodCode"])
	s.Require().Equal("dueDate", one["field"])
	s.Require().Equal("2026-04-20", one["computed"], "what the rule produced")
	s.Require().Equal(substituted, one["applied"], "what was filed instead")

	s.requireNoLeak(entries, "Initech BV", "File the return", "VAT Q1")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the override envelope")
}
