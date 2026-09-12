package auditlog_test

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// The trail narrows to the reader's entity subtree (ADR-0012 B-3). This file
// covers the two questions that belong to the ledger rather than to the
// authorization suite: what happens to an entry that resolves NO entity, and
// that a tenant-wide reader keeps reading the complete ledger.
//
// The decision, and the reason it is fail-closed: a narrowed read scope is the
// union of entity subtrees, and `platform/authz/readscope.go` states that a row
// whose owning entity is NULL is not in it — "a tenant-level resource needs a
// tenant-wide grant, exactly as it does for writes". Two kinds of audit entry
// resolve no entity: the tenant-level ones (a member invited, the obligation
// catalogue, the tenant record, data templates) and the events of a resource
// that has since been DELETED, whose owning row is gone. Both are withheld from
// a principal scoped to one subtree — who cannot read the members, the
// catalogue or the deleted resource either — and both stay fully visible to a
// principal holding a tenant-wide audit:read grant, which is the principal a
// compliance audit or an ADR-0008 chain verification reads the ledger as.

func (s *AuditLogSuite) postID(rb *acceptance.RequestBuilder, path string, body any) string {
	var out struct {
		ID string `json:"id"`
	}
	r := rb.POST(s.T(), path, body)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

func hasAction(rows []auditRow, action string) bool {
	for _, row := range rows {
		if row.Action == action {
			return true
		}
	}
	return false
}

func resourceIDs(rows []auditRow) map[string]bool {
	out := map[string]bool{}
	for _, row := range rows {
		out[row.ResourceID] = true
	}
	return out
}

func (s *AuditLogSuite) TestTrailNarrowsToTheReadScopeAndKeepsEntriesWithNoEntity() {
	tenant := s.InsertTenant("al-scope", "Audit Log Scope").String()
	admin := s.As(tenant)

	// Two sibling entities, a tenant-level obligation type, a workflow under
	// each — and a third workflow under the scope root that is then DELETED, so
	// its events survive in the ledger with no owning row left to resolve.
	entA := s.postID(admin, "/entities", map[string]any{
		"name": "Alpha SAS", "country": "France", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	entB := s.postID(admin, "/entities", map[string]any{
		"name": "Beta GmbH", "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	obType := s.postID(admin, "/obligation-types",
		map[string]any{"name": "VAT", "code": "VAT-SCOPE", "template": "VAT"})

	mkWorkflow := func(name, entityID string) string {
		return s.postID(admin, "/workflows", map[string]any{
			"name": name, "workflowCategory": "recurring",
			"entityId": entityID, "obligationTypeId": obType,
			"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
			"dueDateRule": map[string]any{
				"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
			},
		})
	}
	wfA := mkWorkflow("Alpha VAT 2025", entA)
	wfB := mkWorkflow("Beta VAT 2025", entB)
	doomed := mkWorkflow("Alpha VAT 2024", entA)
	admin.DELETE(s.T(), "/workflows/"+doomed).AssertStatus(s.T(), http.StatusNoContent)

	// A tenant-level event that reaches no entity by any join.
	admin.POST(s.T(), "/members", map[string]any{
		"email": "advisor@alpha.test", "name": "Advisor", "role": "reviewer",
	}).AssertStatus(s.T(), http.StatusCreated)

	// ---- The scoped reader: a reviewer on Alpha's subtree. Reviewer is the
	// scopeable role holding audit:read — the role an external advisor gets.
	scoped := s.AsScopedRole(tenant, "reviewer", entA)
	rows, pg := s.list(scoped, "?limit=100")
	s.Require().Equal(len(rows), pg.Total, "the total must narrow with the page")
	s.Require().NotEmpty(rows, "a narrowed trail is narrowed, not empty")

	ids := resourceIDs(rows)
	s.Require().True(ids[entA], "the scope root's own events must be readable")
	s.Require().True(ids[wfA])
	s.Require().False(ids[entB], "the sibling entity's events must not be")
	s.Require().False(ids[wfB])
	s.Require().False(ids[obType], "tenant-level catalogue events resolve no entity")
	s.Require().False(ids[doomed], "an event whose resource was deleted resolves no entity")
	s.Require().False(hasAction(rows, "member.invited"), "a tenant-level member event is not in a narrowed scope")
	s.Require().False(hasAction(rows, "workflow.deleted"))
	for _, row := range rows {
		if row.WorkflowName != nil {
			s.Require().Equal("Alpha VAT 2025", *row.WorkflowName)
		}
	}

	// ---- The tenant-wide reader: nothing has vanished. Asserted as a REVIEWER,
	// not as the admin, so the property is about the grant's scope and not about
	// the role.
	wide := s.AsRole(tenant, "reviewer")
	wideRows, widePg := s.list(wide, "?limit=100")
	s.Require().Equal(len(wideRows), widePg.Total)
	s.Require().Greater(widePg.Total, pg.Total)

	wideIDs := resourceIDs(wideRows)
	for what, id := range map[string]string{
		"the sibling entity":               entB,
		"its workflow":                     wfB,
		"the tenant-level catalogue":       obType,
		"the deleted workflow's own trail": doomed,
	} {
		s.Require().True(wideIDs[id], "a tenant-wide grant must keep reading %s", what)
	}
	s.Require().True(hasAction(wideRows, "member.invited"), "the member invite must not vanish for everyone")
	s.Require().True(hasAction(wideRows, "workflow.deleted"), "the delete must not vanish for everyone")

	// The workflow filter aimed at the deleted workflow answers for the
	// tenant-wide reader and not for the scoped one — no existence oracle.
	_, widePg = s.list(wide, "?workflowId="+doomed)
	s.Require().NotZero(widePg.Total)
	deletedRows, scopedPg := s.list(scoped, "?workflowId="+doomed)
	s.Require().Empty(deletedRows)
	s.Require().Zero(scopedPg.Total)
}
