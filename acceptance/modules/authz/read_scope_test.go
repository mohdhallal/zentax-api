package authz_test

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// Read-side scope (ADR-0012, increment B-3). Before this increment every
// authorization check in the API sat on a write path: a grant scoped to one
// entity subtree narrowed what its holder could CHANGE and nothing about what
// they could SEE. An audit, signed in as the seeded France-scoped preparer,
// listed all five entities of the group, read the compliance heatmap for the
// German subsidiary, exported the whole group as raw rows and downloaded a
// German entity's tax document.
//
// The fixture below is that shape, generalised: a holding with two national
// subtrees, one of them two levels deep, plus a project workflow that belongs
// to no entity at all. Everything is read twice — once as a preparer scoped to
// the French subtree, once as a tenant-wide administrator — because the
// interesting property is not only that the scoped reader is narrowed, but
// that the unscoped one is not.

type scopeFixture struct {
	group, fr, frSub, de string // entity ids
	obType               string // tenant-level obligation catalogue entry
	frEO, frSubEO, deEO  string // entity obligations
	frWF, frSubWF, deWF  string // recurring workflows
	tenantWF             string // project workflow with NO entity
	frTask, deTask       string // workflow task templates
	frInst, deInst       string // task instances
	frDoc, deDoc         string // documents
	frDocVer, deDocVer   string // current version ids
}

func (s *AuthzSuite) postID(tenant, path string, body any) string {
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), path, body)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

func (s *AuthzSuite) mkObligation(tenant, entityID, obType string) string {
	return s.postID(tenant, "/entity-obligations", map[string]any{
		"entityId": entityID, "obligationTypeId": obType, "periodicity": "monthly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end",
			"offsetUnit": "days", "offsetValue": 20, "offsetDirection": "after",
		},
	})
}

func (s *AuthzSuite) mkWorkflow(tenant, name, entityID, obType string) string {
	return s.postID(tenant, "/workflows", map[string]any{
		"name": name, "workflowCategory": "recurring",
		"entityId": entityID, "obligationTypeId": obType,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
}

func (s *AuthzSuite) mkTask(tenant, workflowID string) string {
	return s.postID(tenant, "/workflow-tasks", map[string]any{
		"workflowId": workflowID, "name": "Prepare", "taskType": "preparation", "approvalRequired": false,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before", "orderIndex": 0,
	})
}

// startAndInstance starts the workflow and returns its single instance's id,
// read back as the ADMIN (a scoped reader could not see the German one).
func (s *AuthzSuite) startAndInstance(tenant, workflowID string) string {
	s.As(tenant).POST(s.T(), "/workflows/"+workflowID+"/start", nil).
		AssertStatus(s.T(), http.StatusCreated)
	var list []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+workflowID).DecodeData(s.T(), &list)
	s.Require().Len(list, 1)
	return list[0].ID
}

func (s *AuthzSuite) uploadDoc(tenant, workflowID, name string) (docID, versionID string) {
	r := s.As(tenant).POSTMultipart(s.T(), "/workflows/"+workflowID+"/documents",
		map[string]string{"documentType": "draft_return", "category": "compliance"},
		acceptance.MultipartFile{FileName: name, ContentType: "application/pdf",
			Content: []byte("%PDF-1.4\n% " + name + "\n")})
	r.AssertStatus(s.T(), http.StatusCreated)
	var v struct {
		ID        string `json:"id"`
		VersionID string `json:"versionId"`
	}
	r.DecodeData(s.T(), &v)
	return v.ID, v.VersionID
}

func (s *AuthzSuite) seedScopeFixture(tenant string) scopeFixture {
	var f scopeFixture
	f.group = s.mkEntity(tenant, "Group Holding", "")
	f.fr = s.mkEntity(tenant, "France SAS", f.group)
	f.frSub = s.mkEntity(tenant, "France Sub SARL", f.fr)
	f.de = s.mkEntity(tenant, "Deutschland GmbH", f.group)
	f.obType = s.mkObligationType(tenant, "VAT-RS")

	f.frEO = s.mkObligation(tenant, f.fr, f.obType)
	f.frSubEO = s.mkObligation(tenant, f.frSub, f.obType)
	f.deEO = s.mkObligation(tenant, f.de, f.obType)

	f.frWF = s.mkWorkflow(tenant, "FR VAT 2025", f.fr, f.obType)
	f.frSubWF = s.mkWorkflow(tenant, "FR Sub VAT 2025", f.frSub, f.obType)
	f.deWF = s.mkWorkflow(tenant, "DE VAT 2025", f.de, f.obType)

	// A project workflow belonging to no entity: tenant-level work, which only
	// a tenant-wide grant may reach — the same rule the write side applies.
	f.tenantWF = s.postID(tenant, "/workflows", map[string]any{
		"name": "Group restructuring", "workflowCategory": "project", "projectType": "restructuring",
		"startDate": "2025-01-15", "endDate": "2025-03-31",
	})

	f.frTask = s.mkTask(tenant, f.frWF)
	f.deTask = s.mkTask(tenant, f.deWF)
	s.mkTask(tenant, f.frSubWF)

	f.frInst = s.startAndInstance(tenant, f.frWF)
	f.deInst = s.startAndInstance(tenant, f.deWF)
	s.startAndInstance(tenant, f.frSubWF)

	// Figures on both instances, so the tax-financial report and the tax-data
	// export have something to aggregate and the scope can be asserted on
	// roll-ups rather than on empty sets.
	s.setTaxData(tenant, f.frInst, 1100)
	s.setTaxData(tenant, f.deInst, 2200)

	f.frDoc, f.frDocVer = s.uploadDoc(tenant, f.frWF, "fr-vat.pdf")
	f.deDoc, f.deDocVer = s.uploadDoc(tenant, f.deWF, "de-vat.pdf")
	return f
}

func (s *AuthzSuite) setTaxData(tenant, instanceID string, outputVat int) {
	s.As(tenant).PUT(s.T(), "/task-instances/"+instanceID, map[string]any{
		"status":  "in_progress",
		"taxData": map[string]any{"outputVat": outputVat, "inputVat": 100},
	}).AssertStatus(s.T(), http.StatusOK)
}

// names + total of a paginated list, as the given requester sees it.
func (s *AuthzSuite) page(rb *acceptance.RequestBuilder, path, field string) ([]string, int) {
	r := rb.GET(s.T(), path)
	r.AssertStatus(s.T(), http.StatusOK)
	var env struct {
		Status     bool             `json:"status"`
		Data       []map[string]any `json:"data"`
		Pagination map[string]any   `json:"pagination"`
	}
	s.Require().NoError(json.Unmarshal([]byte(r.BodyString()), &env), r.BodyString())
	s.Require().True(env.Status, r.BodyString())
	out := make([]string, 0, len(env.Data))
	for _, row := range env.Data {
		v, _ := row[field].(string)
		out = append(out, v)
	}
	total := -1
	if t, ok := env.Pagination["total"].(float64); ok {
		total = int(t)
	}
	return out, total
}

func (s *AuthzSuite) getData(rb *acceptance.RequestBuilder, path string, out any) {
	r := rb.GET(s.T(), path)
	r.AssertStatus(s.T(), http.StatusOK)
	r.DecodeData(s.T(), out)
}

// TestReadScope_EntitiesAndTheirDomainLists: every list and single get of the
// seven read modules is narrowed to the grant's entity subtree, and the
// pagination total is narrowed with the page — a narrowed page reporting the
// tenant's total would leak the size of what it hid.
func (s *AuthzSuite) TestReadScope_EntitiesAndTheirDomainLists() {
	tenant := s.InsertTenant("rs-lists", "Read Scope Lists").String()
	f := s.seedScopeFixture(tenant)
	fr := s.AsScopedRole(tenant, "preparer", f.fr)
	admin := s.As(tenant)

	// ---- entities: the subtree, and ONLY the subtree. The ancestors above the
	// scope root are deliberately not added back (see the decision recorded in
	// modules/entities/repositories/pg/sql.go).
	names, total := s.page(fr, "/entities?limit=100", "name")
	s.Require().ElementsMatch([]string{"France SAS", "France Sub SARL"}, names)
	s.Require().Equal(2, total, "the total must describe the narrowed set")

	names, total = s.page(admin, "/entities?limit=100", "name")
	s.Require().Len(names, 4)
	s.Require().Equal(4, total, "a tenant-wide grant keeps reading the whole tenant")

	// The scope root itself is readable; its parent and the sibling subtree are
	// not there at all (404, not 403: a narrowed read does not confirm what it
	// cannot show).
	fr.GET(s.T(), "/entities/"+f.fr).AssertStatus(s.T(), http.StatusOK)
	fr.GET(s.T(), "/entities/"+f.frSub).AssertStatus(s.T(), http.StatusOK)
	fr.GET(s.T(), "/entities/"+f.de).AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/entities/"+f.group).AssertStatus(s.T(), http.StatusNotFound)
	admin.GET(s.T(), "/entities/"+f.de).AssertStatus(s.T(), http.StatusOK)

	// The SHAPE of what it cannot read survives: the scope root still reports
	// the parent it hangs off, so a scoped reader can tell a parent exists
	// without reading it — which is what keeps the group tree renderable.
	var root struct {
		ParentEntityID *string `json:"parentEntityId"`
	}
	s.getData(fr, "/entities/"+f.fr, &root)
	s.Require().NotNil(root.ParentEntityID)
	s.Require().Equal(f.group, *root.ParentEntityID)

	// The derived read of an entity (its fiscal periods) is narrowed with it.
	fr.GET(s.T(), "/entities/"+f.fr+"/periods?periodicity=monthly&financialYear=2025").
		AssertStatus(s.T(), http.StatusOK)
	fr.GET(s.T(), "/entities/"+f.de+"/periods?periodicity=monthly&financialYear=2025").
		AssertStatus(s.T(), http.StatusNotFound)

	// ---- entity obligations.
	ids, total := s.page(fr, "/entity-obligations?limit=100", "id")
	s.Require().ElementsMatch([]string{f.frEO, f.frSubEO}, ids)
	s.Require().Equal(2, total)
	fr.GET(s.T(), "/entity-obligations/"+f.deEO).AssertStatus(s.T(), http.StatusNotFound)
	admin.GET(s.T(), "/entity-obligations/"+f.deEO).AssertStatus(s.T(), http.StatusOK)

	// ---- workflows: the two in the subtree. The German one and the
	// tenant-level project are both absent, for different reasons.
	names, total = s.page(fr, "/workflows?limit=100", "name")
	s.Require().ElementsMatch([]string{"FR VAT 2025", "FR Sub VAT 2025"}, names)
	s.Require().Equal(2, total)
	fr.GET(s.T(), "/workflows/"+f.deWF).AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/workflows/"+f.frWF).AssertStatus(s.T(), http.StatusOK)
	fr.GET(s.T(), "/workflows/"+f.frWF+"/preview").AssertStatus(s.T(), http.StatusOK)
	// Preview is the one read that already consulted the authorizer before this
	// increment (generator.PreviewWorkflow asks EnsureWorkflow for
	// workflow:read), so it refuses with 403 rather than hiding the row. Left as
	// it is: a working authorization check is not worth churning for symmetry.
	// The rule is therefore explicit — a read narrowed by the repository
	// predicate answers 404 (the row is not in the caller's view), a read gated
	// by an explicit Ensure* answers 403.
	fr.GET(s.T(), "/workflows/"+f.deWF+"/preview").AssertStatus(s.T(), http.StatusForbidden)

	// A tenant-level resource needs a TENANT-WIDE grant — it is not invisible
	// to everyone. The administrator reads the entity-less project workflow;
	// the scoped preparer cannot.
	names, total = s.page(admin, "/workflows?limit=100", "name")
	s.Require().Len(names, 4)
	s.Require().Equal(4, total)
	s.Require().Contains(names, "Group restructuring")
	admin.GET(s.T(), "/workflows/"+f.tenantWF).AssertStatus(s.T(), http.StatusOK)
	fr.GET(s.T(), "/workflows/"+f.tenantWF).AssertStatus(s.T(), http.StatusNotFound)

	// ---- workflow task templates: reached through their workflow's entity.
	ids, total = s.page(fr, "/workflow-tasks?limit=100", "id")
	s.Require().NotContains(ids, f.deTask)
	s.Require().Contains(ids, f.frTask)
	s.Require().Equal(2, total)
	fr.GET(s.T(), "/workflow-tasks/"+f.deTask).AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/workflow-tasks/"+f.frTask).AssertStatus(s.T(), http.StatusOK)

	// ---- task instances.
	ids, total = s.page(fr, "/task-instances?limit=100", "id")
	s.Require().NotContains(ids, f.deInst)
	s.Require().Contains(ids, f.frInst)
	s.Require().Equal(2, total)
	fr.GET(s.T(), "/task-instances/"+f.deInst).AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/task-instances/"+f.frInst).AssertStatus(s.T(), http.StatusOK)

	// ---- the tenant-level CATALOGUE stays readable. An obligation type has no
	// owning entity, and a scoped preparer cannot render their own workflow
	// without it, so it is deliberately never narrowed.
	ids, _ = s.page(fr, "/obligation-types?limit=100", "id")
	s.Require().Contains(ids, f.obType)
	fr.GET(s.T(), "/obligation-types/"+f.obType).AssertStatus(s.T(), http.StatusOK)
}

// TestReadScope_DocumentsIncludingTheBytes: the audit's worst finding was not a
// list but a download — a scoped preparer pulled the actual bytes of another
// subsidiary's tax document. Every documents read path is narrowed: the list,
// the single get, the two sub-lists, the version list and both download paths.
func (s *AuthzSuite) TestReadScope_DocumentsIncludingTheBytes() {
	tenant := s.InsertTenant("rs-docs", "Read Scope Docs").String()
	f := s.seedScopeFixture(tenant)
	fr := s.AsScopedRole(tenant, "preparer", f.fr)
	admin := s.As(tenant)

	ids, total := s.page(fr, "/documents?limit=100", "id")
	s.Require().Equal([]string{f.frDoc}, ids)
	s.Require().Equal(1, total)

	ids, total = s.page(admin, "/documents?limit=100", "id")
	s.Require().ElementsMatch([]string{f.frDoc, f.deDoc}, ids)
	s.Require().Equal(2, total)

	fr.GET(s.T(), "/documents/"+f.frDoc).AssertStatus(s.T(), http.StatusOK)
	fr.GET(s.T(), "/documents/"+f.deDoc).AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/documents/"+f.deDoc+"/versions").AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/documents/"+f.frDoc+"/versions").AssertStatus(s.T(), http.StatusOK)

	// The bytes. Both download routes, and the version one by id too.
	own := fr.GET(s.T(), "/documents/"+f.frDoc+"/download")
	own.AssertStatus(s.T(), http.StatusOK)
	s.Require().True(strings.HasPrefix(own.BodyString(), "%PDF-1.4"), "own document still downloads")

	// The version-by-id download route is a second, separate path to the same
	// bytes. Its refusal below is not vacuous: the caller's own version
	// downloads through it here.
	ownVer := fr.GET(s.T(), "/documents/"+f.frDoc+"/versions/"+f.frDocVer+"/download")
	ownVer.AssertStatus(s.T(), http.StatusOK)
	s.Require().True(strings.HasPrefix(ownVer.BodyString(), "%PDF-1.4"))

	fr.GET(s.T(), "/documents/"+f.deDoc+"/download").AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/documents/"+f.deDoc+"/versions/"+f.deDocVer+"/download").
		AssertStatus(s.T(), http.StatusNotFound)
	admin.GET(s.T(), "/documents/"+f.deDoc+"/download").AssertStatus(s.T(), http.StatusOK)
	admin.GET(s.T(), "/documents/"+f.deDoc+"/versions/"+f.deDocVer+"/download").
		AssertStatus(s.T(), http.StatusOK)

	// The sub-lists answer 404 for an out-of-scope parent rather than an empty
	// page: an empty page still confirms the parent exists.
	fr.GET(s.T(), "/workflows/"+f.deWF+"/documents").AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/task-instances/"+f.deInst+"/documents").AssertStatus(s.T(), http.StatusNotFound)
	fr.GET(s.T(), "/workflows/"+f.frWF+"/documents").AssertStatus(s.T(), http.StatusOK)
	fr.GET(s.T(), "/task-instances/"+f.frInst+"/documents").AssertStatus(s.T(), http.StatusOK)
}

// TestReadScope_Reports: the reports SQL is hand-written and consulted no
// authorization at all — it is where the whole-group export came from. All
// seven report reads are narrowed, aggregates and exact totals included.
func (s *AuthzSuite) TestReadScope_Reports() {
	tenant := s.InsertTenant("rs-reports", "Read Scope Reports").String()
	f := s.seedScopeFixture(tenant)
	fr := s.AsScopedRole(tenant, "preparer", f.fr)
	admin := s.As(tenant)

	// ---- compliance heatmap: one row per entity. The German subsidiary was
	// legible to the France-scoped preparer before this increment.
	type heatmap struct {
		Cells []struct {
			RowID    string `json:"rowId"`
			RowLabel string `json:"rowLabel"`
		} `json:"cells"`
	}
	var hm heatmap
	s.getData(fr, "/reports/compliance-heatmap", &hm)
	s.Require().NotEmpty(hm.Cells)
	for _, c := range hm.Cells {
		s.Require().NotEqual(f.de, c.RowID, "the German subtree must not appear: %s", c.RowLabel)
	}
	hm = heatmap{}
	s.getData(admin, "/reports/compliance-heatmap", &hm)
	rows := map[string]bool{}
	for _, c := range hm.Cells {
		rows[c.RowID] = true
	}
	s.Require().True(rows[f.de], "the administrator still sees every entity")

	// ---- raw export, all three datasets: rows AND the exact total.
	type export struct {
		Rows []struct {
			EntityName *string `json:"entityName"`
		} `json:"rows"`
		TotalCount int `json:"totalCount"`
	}
	for _, dataset := range []string{"workflows", "tasks", "tax-data"} {
		var scopedEx, adminEx export
		s.getData(fr, "/reports/export-raw?dataset="+dataset+"&limit=100", &scopedEx)
		s.getData(admin, "/reports/export-raw?dataset="+dataset+"&limit=100", &adminEx)
		for _, row := range scopedEx.Rows {
			if row.EntityName != nil {
				s.Require().NotEqual("Deutschland GmbH", *row.EntityName,
					"%s: the export must not cross the subtree", dataset)
			}
		}
		s.Require().Equal(len(scopedEx.Rows), scopedEx.TotalCount,
			"%s: the total must describe the narrowed set", dataset)
		s.Require().Less(scopedEx.TotalCount, adminEx.TotalCount,
			"%s: the administrator must still export more than the scoped reader", dataset)
	}

	// ---- compliance status: rows, the summary cards and the filtered total.
	type complianceStatus struct {
		Rows []struct {
			EntityID   string `json:"entityId"`
			EntityName string `json:"entityName"`
		} `json:"rows"`
		Summary struct {
			Total int `json:"total"`
		} `json:"summary"`
		TotalCount int `json:"totalCount"`
	}
	var cs, csAdmin complianceStatus
	s.getData(fr, "/reports/compliance-status?limit=100", &cs)
	s.getData(admin, "/reports/compliance-status?limit=100", &csAdmin)
	for _, row := range cs.Rows {
		s.Require().NotEqual(f.de, row.EntityID, "compliance status leaked %s", row.EntityName)
	}
	s.Require().Equal(len(cs.Rows), cs.TotalCount)
	s.Require().Equal(len(cs.Rows), cs.Summary.Total,
		"the summary counts the population the page was cut from — narrowed too")
	s.Require().Less(cs.Summary.Total, csAdmin.Summary.Total)

	// ---- tax financial: rows, the GROUPING SETS roll-ups and the grand total.
	type taxFinancial struct {
		Rows []struct {
			EntityID string `json:"entityId"`
		} `json:"rows"`
		Aggregated []struct {
			Key string `json:"key"`
		} `json:"aggregated"`
		Summary struct {
			RecordCount int `json:"recordCount"`
		} `json:"summary"`
	}
	var tf taxFinancial
	s.getData(fr, "/reports/tax-financial?groupBy=entity&limit=100", &tf)
	for _, row := range tf.Rows {
		s.Require().NotEqual(f.de, row.EntityID)
	}
	for _, g := range tf.Aggregated {
		s.Require().NotEqual(f.de, g.Key, "a roll-up must not aggregate over hidden rows")
	}

	// ---- the enriched task feed and its tile counts must agree with each
	// other AND with the scope.
	feedIDs, feedTotal := s.page(fr, "/reports/task-instances?limit=100", "id")
	s.Require().NotContains(feedIDs, f.deInst)
	s.Require().Contains(feedIDs, f.frInst)
	s.Require().Equal(len(feedIDs), feedTotal)

	var summary struct {
		Total int `json:"total"`
	}
	s.getData(fr, "/reports/task-summary", &summary)
	s.Require().Equal(feedTotal, summary.Total, "the tiles and the feed must describe one set")
	var adminSummary struct {
		Total int `json:"total"`
	}
	s.getData(admin, "/reports/task-summary", &adminSummary)
	s.Require().Greater(adminSummary.Total, summary.Total)

	// ---- workflow stats: the one report route gated by workflow:read. The
	// response is an object keyed by workflow id.
	stats := map[string]any{}
	s.getData(fr, "/reports/workflow-stats", &stats)
	s.Require().Contains(stats, f.frWF)
	s.Require().NotContains(stats, f.deWF, "workflow stats leaked the German workflow")
	s.Require().NotContains(stats, f.tenantWF, "a scoped grant cannot reach tenant-level work")
	s.Require().Len(stats, 2)

	stats = map[string]any{}
	s.getData(admin, "/reports/workflow-stats", &stats)
	s.Require().Len(stats, 4, "the administrator still sees every workflow")
}

// TestReadScope_WritesStillRefusedNotHidden: narrowing the reads must not
// weaken the write-side rule that was already there. A scoped preparer is
// still refused (403) on a capability they do not hold, and the subtree rule
// on writes still answers 403 rather than turning into a 404 that would hide
// which check failed.
func (s *AuthzSuite) TestReadScope_WritesStillRefusedNotHidden() {
	tenant := s.InsertTenant("rs-writes", "Read Scope Writes").String()
	f := s.seedScopeFixture(tenant)
	frManager := s.AsScopedRole(tenant, "manager", f.fr)

	// In scope: a manager scoped to France may still write there.
	frManager.PUT(s.T(), "/entities/"+f.frSub, map[string]any{
		"name": "France Sub SARL", "country": "France",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	}).AssertStatus(s.T(), http.StatusOK)

	// Out of scope: refused by the authorizer, which runs before the read.
	frManager.PUT(s.T(), "/entities/"+f.de, map[string]any{
		"name": "Deutschland GmbH", "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	}).AssertStatus(s.T(), http.StatusForbidden)

	// A scoped writer's own write still reads its result back. The create and
	// update responses are rendered through the read-scoped document view, so
	// narrowing it would have broken the upload a scoped preparer is there to
	// do — in scope, it must still round-trip.
	scopedPreparer := s.AsScopedRole(tenant, "preparer", f.fr)
	r := scopedPreparer.POSTMultipart(s.T(), "/workflows/"+f.frWF+"/documents",
		map[string]string{"documentType": "draft_return", "category": "compliance"},
		acceptance.MultipartFile{FileName: "own.pdf", ContentType: "application/pdf",
			Content: []byte("%PDF-1.4\n% own\n")})
	r.AssertStatus(s.T(), http.StatusCreated)
	var own struct {
		ID         string `json:"id"`
		EntityName string `json:"entityName"`
	}
	r.DecodeData(s.T(), &own)
	s.Require().Equal("France SAS", own.EntityName)
	scopedPreparer.GET(s.T(), "/documents/"+own.ID+"/download").AssertStatus(s.T(), http.StatusOK)

	// Uploading into the sibling subtree is refused by the authorizer, which
	// runs before the read — 403, unchanged.
	scopedPreparer.POSTMultipart(s.T(), "/workflows/"+f.deWF+"/documents",
		map[string]string{"documentType": "draft_return", "category": "compliance"},
		acceptance.MultipartFile{FileName: "de.pdf", ContentType: "application/pdf",
			Content: []byte("%PDF-1.4\n% de\n")}).
		AssertStatus(s.T(), http.StatusForbidden)

	// A capability the role does not hold is still 403, not 404. A preparer
	// holds every READ capability of the domain modules but never audit:read,
	// so the audit trail stays refused on capability grounds — narrowing the
	// domain reads must not turn that into a hidden-row 404.
	//
	// This is NOT coverage of the audit trail's read scope, and for one
	// increment it was mistaken for it: a preparer never reaches the route, so
	// nothing here exercised a scoped reader that can, and the trail went
	// unnarrowed. It is asserted against a scoped REVIEWER, the role an external
	// advisor is given, in read_scope_audit_test.go.
	s.AsScopedRole(tenant, "preparer", f.fr).
		GET(s.T(), "/audit-log").
		AssertStatus(s.T(), http.StatusForbidden)
}
