package audit_test

import (
	"encoding/json"
	"net/http"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// The third pass over the ADR-0008 envelope, closing what the second left:
//
//   - a data template recorded a field COUNT, so rewriting the schema that
//     validates tax figures was byte-identical to renaming one;
//   - entity, workflow and workflow-task deletes recorded how many rows
//     cascaded and nothing about what the row WAS, on the one operation that
//     cannot be undone;
//   - the two rule objects the whitelists quote whole — a deadline rule and a
//     custom fiscal calendar — each carried one unbounded free-text member
//     inside them, which reopened at one level down the hole the free-text rule
//     closed at the top.
//
// Every test here asserts the stored before AND after, and re-verifies the hash
// chain across the richer payloads.

// ---- data templates ----

// A data template is the only resource whose change leaves no trace in the data
// it governs: a task instance records its figures, nothing records the rules
// they were validated against, and domain.FieldsCompatible freezes only a
// field's id and type once the template is in use. So the bounds, the decimal
// places and the mandatory flag of a template attached to a LIVE workflow step
// can be rewritten freely — which is exactly the edit this walks.
func (s *AuditDetailsSuite) TestDataTemplateRecordsTheSchemaNotACount() {
	tenant := s.InsertTenant("aud-det-dt", "Audit Details DT").String()
	admin := s.As(tenant)

	var entity, obType, wf, tpl, loose, task struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Umbrella Ltd", "country": "Ireland", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "VAT", "code": "VAT-IE", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	// A tight schema: one figure, mandatory, 0 … 1,000,000, two decimals.
	r = admin.POST(s.T(), "/data-templates", map[string]any{
		"name": "IE VAT figures", "templateType": "VAT",
		"description": "Prepared by Jane Doe, jane.doe@probe.test",
		"fields": []map[string]any{
			{
				"id": "vatPayable", "name": "VAT payable", "fieldType": "numeric", "mandatory": true,
				"numericValidation": map[string]any{
					"min": 0, "max": 1000000, "decimalPlaces": 2, "formatAsCurrency": true,
				},
			},
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &tpl)

	// Attached to a real workflow step, so the template is in use.
	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "Quarterly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "quarterly", "financialYear": "2026", "selectedPeriods": []string{"Q1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days",
			"offsetValue": 20, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = admin.POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Enter figures", "taskType": "preparation",
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 3,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
		"dataTemplateId": tpl.ID,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &task)

	// The loosening: still id-and-type compatible, so the API allows it on an
	// in-use template — mandatory off, the range widened by two orders of
	// magnitude, six decimals, the currency formatting dropped.
	admin.PUT(s.T(), "/data-templates/"+tpl.ID, map[string]any{
		"name": "IE VAT figures", "templateType": "VAT",
		"fields": []map[string]any{
			{
				"id": "vatPayable", "name": "VAT payable", "fieldType": "numeric", "mandatory": false,
				"numericValidation": map[string]any{
					"min": -99999999, "max": 99999999, "decimalPlaces": 6, "formatAsCurrency": false,
				},
			},
		},
	}).AssertStatus(s.T(), http.StatusOK)

	// A second, unreferenced template proves the delete side and the shape gate
	// on a field id — `max=64` with no shape rule, so it may hold an address.
	r = admin.POST(s.T(), "/data-templates", map[string]any{
		"name": "Scratch", "templateType": "Custom",
		"fields": []map[string]any{
			{"id": "jane.doe@probe.test", "name": "Contact", "fieldType": "text", "mandatory": false},
			{"id": "memo", "name": "Memo", "fieldType": "text", "mandatory": true},
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &loose)
	admin.DELETE(s.T(), "/data-templates/"+loose.ID).AssertStatus(s.T(), http.StatusNoContent)

	entries := s.trail(tenant)

	// ---- created: the schema it was born with, the labels withheld. ----
	created := s.entryFor(entries, "data_template.created", tpl.ID)
	s.requireChange(created, "templateType", nil, "VAT")
	s.requireChange(created, "name", nil, "set")
	s.requireChange(created, "description", nil, "set")
	s.Require().Equal([]any{map[string]any{
		"id": "vatPayable", "fieldType": "numeric", "mandatory": true,
		"numericValidation": map[string]any{
			"min": float64(0), "max": float64(1000000), "decimalPlaces": float64(2),
			"allowDecimals": true, "formatAsCurrency": true,
		},
	}}, s.fieldsOf(created)["fields"]["to"])

	// ---- updated: the rules that were in force, and the ones that replaced
	// them. Under a field count both sides read {"fields": 1}. ----
	updated := s.entryFor(entries, "data_template.updated", tpl.ID)
	s.requireUnrecorded(updated, "templateType") // untouched
	s.requireUnrecorded(updated, "name")
	change := s.fieldsOf(updated)["fields"]
	s.Require().Equal([]any{map[string]any{
		"id": "vatPayable", "fieldType": "numeric", "mandatory": true,
		"numericValidation": map[string]any{
			"min": float64(0), "max": float64(1000000), "decimalPlaces": float64(2),
			"allowDecimals": true, "formatAsCurrency": true,
		},
	}}, change["from"], "the rules the already-entered figures were validated against")
	s.Require().Equal([]any{map[string]any{
		"id": "vatPayable", "fieldType": "numeric", "mandatory": false,
		"numericValidation": map[string]any{
			"min": float64(-99999999), "max": float64(99999999), "decimalPlaces": float64(6),
			"allowDecimals": true, "formatAsCurrency": false,
		},
	}}, change["to"])

	// ---- deleted: what left, and the gate on a field id that is not a name. ----
	deleted := s.entryFor(entries, "data_template.deleted", loose.ID)
	s.requireChange(deleted, "templateType", "Custom", nil)
	s.Require().Equal([]any{
		map[string]any{"id": "redacted", "fieldType": "text", "mandatory": false},
		map[string]any{"id": "memo", "fieldType": "text", "mandatory": true},
	}, s.fieldsOf(deleted)["fields"]["from"],
		"an identifier-shaped id is quoted; an address is not")

	// The workflow task that pointed at the template names it by id, so the two
	// records join.
	taskCreated := s.entryFor(entries, "workflow_task.created", task.ID)
	s.requireChange(taskCreated, "dataTemplateId", nil, tpl.ID)

	s.requireNoLeak(entries, "IE VAT figures", "VAT payable", "jane.doe@probe.test",
		"Jane Doe", "Scratch", "Contact", "Memo", "Umbrella Ltd")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// ---- the three cascade deletes ----

// A delete is the one operation nothing can undo: entities, workflows and
// workflow tasks are hard-deleted, there is no history table, and resource_id
// afterwards points at a row that is gone. The envelope used to carry a census
// of what cascaded — how much left — and nothing about what the row WAS. This
// walks all three and asserts the before side of each.
func (s *AuditDetailsSuite) TestCascadeDeletesRecordWhatLeftNotJustHowMuch() {
	tenant := s.InsertTenant("aud-det-del", "Audit Details DEL").String()
	admin := s.As(tenant)

	var entity, obType, wf, task struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Soylent NV", "legalName": "Soylent Naamloze Vennootschap",
		"country": "Belgium", "taxResidency": "Belgium",
		"financialYearEnd": "06-30", "fiscalCalendarPattern": "454",
		"fiscalWeekEndDay": "sunday", "fiscalYearEndRule": "last",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "CIT", "code": "CIT-BE", "template": "CIT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = admin.POST(s.T(), "/workflows", map[string]any{
		"name": "Annual CIT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2026",
		"selectedPeriods": []string{"M1", "M2"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "months",
			"offsetValue": 6, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = admin.POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Sign off", "taskType": "review",
		"roleLabel": "Maria's review", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 2,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &task)

	// Two instances exist below the step; none is approved, so all three
	// deletes are permitted.
	admin.POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	admin.DELETE(s.T(), "/workflow-tasks/"+task.ID).AssertStatus(s.T(), http.StatusNoContent)
	admin.DELETE(s.T(), "/workflows/"+wf.ID).AssertStatus(s.T(), http.StatusNoContent)
	admin.DELETE(s.T(), "/entities/"+entity.ID).AssertStatus(s.T(), http.StatusNoContent)

	entries := s.trail(tenant)

	// ---- workflow_task.deleted: the census AND the step. ----
	taskDeleted := s.entryFor(entries, "workflow_task.deleted", task.ID)
	s.Require().Equal(float64(2), s.countOf(taskDeleted, "taskInstances"),
		"the census still says how much left")
	s.requireChange(taskDeleted, "approvalRequired", true, nil)
	s.requireChange(taskDeleted, "taskType", "review", nil)
	s.requireChange(taskDeleted, "dueDateOffsetValue", float64(2), nil)
	s.requireChange(taskDeleted, "workflowId", wf.ID, nil)
	s.requireChange(taskDeleted, "roleLabel", "set", nil) // a name; never quoted

	// ---- workflow.deleted: what it filed, for whom, over which periods. ----
	wfDeleted := s.entryFor(entries, "workflow.deleted", wf.ID)
	s.Require().Equal(float64(0), s.countOf(wfDeleted, "blobsQueuedForReclaim"))
	s.requireChange(wfDeleted, "entityId", entity.ID, nil)
	s.requireChange(wfDeleted, "obligationTypeId", obType.ID, nil)
	s.requireChange(wfDeleted, "periodicity", "monthly", nil)
	s.requireChange(wfDeleted, "financialYear", "2026", nil)
	s.requireChange(wfDeleted, "status", "active", nil) // start() activated it
	s.requireChange(wfDeleted, "name", "set", nil)
	s.Require().Equal([]any{"M1", "M2"}, s.fieldsOf(wfDeleted)["selectedPeriods"]["from"])
	gone, ok := s.fieldsOf(wfDeleted)["dueDateRule"]["from"].(map[string]any)
	s.Require().True(ok, "the rule that dated every instance must be recorded")
	s.Require().Equal(float64(6), gone["offsetValue"])

	// ---- entity.deleted: the fiscal calendar that decided the legal dates. ----
	entityDeleted := s.entryFor(entries, "entity.deleted", entity.ID)
	s.Require().Equal(float64(0), s.countOf(entityDeleted, "workflows"),
		"the workflow was already gone, and the census still runs")
	s.requireChange(entityDeleted, "fiscalCalendarPattern", "454", nil)
	s.requireChange(entityDeleted, "financialYearEnd", "06-30", nil)
	s.requireChange(entityDeleted, "fiscalWeekEndDay", "sunday", nil)
	s.requireChange(entityDeleted, "fiscalYearEndRule", "last", nil)
	s.requireChange(entityDeleted, "status", "active", nil)
	s.requireChange(entityDeleted, "name", "set", nil)
	s.requireChange(entityDeleted, "legalName", "set", nil)

	// A one-sided envelope carries no "to" anywhere.
	for _, e := range []audit.Entry{taskDeleted, wfDeleted, entityDeleted} {
		for name, change := range s.fieldsOf(e) {
			s.Require().NotContains(change, "to",
				"%s.%s must record the before side only", e.Action, name)
		}
	}

	s.requireNoLeak(entries, "Soylent NV", "Soylent Naamloze Vennootschap", "Belgium",
		"Annual CIT", "Sign off", "Maria's review")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// ---- free text one level down, inside the rule objects ----

// The whitelists withhold a name, a jurisdiction and a country because the
// contract bounds their length and nothing else. Two rule objects are quoted
// whole beside them, and each holds exactly one member of the same kind: a
// custom fiscal period's `code` (`max=16`) and an additional deadline's `type`
// (`max=50`). Both now go through the identifier-shape gate: a value that looks
// like a code is quoted, anything else is recorded as "redacted" — and the
// boundaries and offsets around it, which are what actually compute a date, are
// untouched.
func (s *AuditDetailsSuite) TestFreeTextInsideRuleObjectsNeverReachesTheEnvelope() {
	tenant := s.InsertTenant("aud-det-rule", "Audit Details RULE").String()
	admin := s.As(tenant)

	const (
		periodContact  = "j.doe@probe.test"           // 16 chars: inside `max=16`
		chaseInstruct  = "chase J. Doe jd@probe.test" // 26 chars: inside `max=50`
		periodDisplay  = "Trading period one"
		secondPeriodID = "Q1 adj" // a space is not a code shape either
	)

	var entity, obType, obligation struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Cyberdyne Oy", "country": "Finland", "financialYearEnd": "12-31",
		"fiscalCalendarPattern": "custom",
		"customPeriods": []map[string]any{
			{"code": "P1", "name": periodDisplay, "startDate": "01-01", "endDate": "06-30"},
			{"code": periodContact, "name": "Second", "startDate": "07-01", "endDate": "12-31"},
			{"code": secondPeriodID, "name": "Third", "startDate": "12-01", "endDate": "12-15"},
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = admin.POST(s.T(), "/obligation-types",
		map[string]any{"name": "WHT", "code": "WHT-FI", "template": "WHT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = admin.POST(s.T(), "/entity-obligations", map[string]any{
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"currency": "EUR", "periodicity": "quarterly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end", "offsetUnit": "days",
			"offsetValue": 30, "offsetDirection": "after",
			"weekendAdjustment": "next-business-day",
			"additionalDeadlines": []map[string]any{
				{"type": "advance_payment", "months": 0, "days": 5},
				{"type": chaseInstruct, "months": 1, "days": 0},
			},
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obligation)

	// Moving one offset proves the gated members still diff on both sides.
	admin.PUT(s.T(), "/entity-obligations/"+obligation.ID, map[string]any{
		"currency": "EUR", "periodicity": "quarterly", "status": "active",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end", "offsetUnit": "days",
			"offsetValue": 30, "offsetDirection": "after",
			"weekendAdjustment": "next-business-day",
			"additionalDeadlines": []map[string]any{
				{"type": "advance_payment", "months": 0, "days": 12},
				{"type": chaseInstruct, "months": 1, "days": 0},
			},
		},
	}).AssertStatus(s.T(), http.StatusOK)

	entries := s.trail(tenant)

	// ---- the fiscal calendar: boundaries kept, codes gated. ----
	created := s.entryFor(entries, "entity.created", entity.ID)
	s.Require().Equal([]any{
		map[string]any{"code": "P1", "startDate": "01-01", "endDate": "06-30"},
		map[string]any{"code": "redacted", "startDate": "07-01", "endDate": "12-31"},
		map[string]any{"code": "redacted", "startDate": "12-01", "endDate": "12-15"},
	}, s.fieldsOf(created)["customPeriods"]["to"],
		"a code-shaped code is quoted; an address and a phrase are not")
	s.requireChange(created, "fiscalCalendarPattern", nil, "custom")

	// ---- the deadline rule: offsets kept, the one free-text member gated. ----
	obCreated := s.entryFor(entries, "entity_obligation.created", obligation.ID)
	rule, ok := s.fieldsOf(obCreated)["deadlineRule"]["to"].(map[string]any)
	s.Require().True(ok, "the deadline rule must be recorded")
	s.Require().Equal(float64(30), rule["offsetValue"], "the rest of the rule is untouched")
	s.Require().Equal("next-business-day", rule["weekendAdjustment"])
	s.Require().Equal([]any{
		map[string]any{"type": "advance_payment", "months": float64(0), "days": float64(5)},
		map[string]any{"type": "redacted", "months": float64(1), "days": float64(0)},
	}, rule["additionalDeadlines"])

	// ---- and the change to a gated entry's offset is still a change. ----
	obUpdated := s.entryFor(entries, "entity_obligation.updated", obligation.ID)
	ruleChange := s.fieldsOf(obUpdated)["deadlineRule"]
	before, ok := ruleChange["from"].(map[string]any)
	s.Require().True(ok, "the superseded rule must be recorded: %v", ruleChange)
	after, ok := ruleChange["to"].(map[string]any)
	s.Require().True(ok, "the new rule must be recorded: %v", ruleChange)
	s.Require().Equal([]any{
		map[string]any{"type": "advance_payment", "months": float64(0), "days": float64(5)},
		map[string]any{"type": "redacted", "months": float64(1), "days": float64(0)},
	}, before["additionalDeadlines"])
	s.Require().Equal([]any{
		map[string]any{"type": "advance_payment", "months": float64(0), "days": float64(12)},
		map[string]any{"type": "redacted", "months": float64(1), "days": float64(0)},
	}, after["additionalDeadlines"])

	s.requireNoLeak(entries, periodContact, chaseInstruct, periodDisplay, secondPeriodID,
		"jd@probe.test", "J. Doe", "Cyberdyne Oy")
	s.Require().NoError(audit.VerifyChain(entries),
		"the hash chain must verify across the richer envelopes")
}

// countOf reads a count that sits BESIDE the change set — a cascade census key
// on a delete envelope. `fields` maps a field name to a {from, to} pair, and a
// count is not a before/after value, so the two never collide.
func (s *AuditDetailsSuite) countOf(e audit.Entry, key string) float64 {
	var payload map[string]any
	s.Require().NoError(json.Unmarshal(e.Details, &payload),
		"details must decode: %s", string(e.Details))
	value, ok := payload[key]
	s.Require().True(ok, "%s must record the %s census: %s", e.Action, key, string(e.Details))
	count, ok := value.(float64)
	s.Require().True(ok, "%s.%s must be a number: %v", e.Action, key, value)
	return count
}
