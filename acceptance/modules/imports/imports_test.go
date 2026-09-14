package imports_test

import (
	"net/http"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// TestCleanFileCommitsExactlyWhatTheDryRunPromised is the headline claim: the
// dry run says what will happen, and that is what happens.
//
// The file is a small group — a parent and two subsidiaries, one of which names
// the parent that the SAME file creates — so the commit also has to order its
// writes rather than following the file.
func (s *ImportsSuite) TestCleanFileCommitsExactlyWhatTheDryRunPromised() {
	tenant := s.InsertTenant("imp-clean", "Import Clean").String()

	// The subsidiary comes FIRST in the file, so file order is not write order.
	file := entityCSV(
		[]string{"Acme Deutschland GmbH", "Germany", "Acme Group NV", "12-31", "Acme Deutschland Gesellschaft"},
		[]string{"Acme Group NV", "Netherlands", "", "12-31", ""},
		[]string{"Acme France SAS", "France", "Acme Group NV", "06-30", ""},
	)

	dry := s.upload(tenant, "entities", file)
	s.Require().Equal("validated", dry.Batch.Status)
	s.Require().True(dry.Batch.Committable)
	s.Require().Equal(summary{Rows: 3, Creates: 3}, dry.Batch.Summary)
	s.Require().Len(dry.Rows, 3)
	for _, row := range dry.Rows {
		s.Require().Equal("create", row.Action, "row %d", row.Row)
		s.Require().Nil(row.TargetID)
	}
	// The dry run says how it read the file, which is the only way a customer
	// can catch a column bound to the wrong field.
	s.Require().Equal(1, dry.Columns.HeaderRow)
	s.Require().Equal("Entity Name", dry.Columns.Bound["name"])
	s.Require().Equal("Parent", dry.Columns.Bound["parent"])

	// Nothing has been written yet: a dry run is a dry run.
	s.Require().Empty(s.entities(tenant))

	committed := s.commit(tenant, "entities", dry.Batch.ID)
	s.Require().Equal("committed", committed.Status)
	s.Require().False(committed.Committable)
	s.Require().NotNil(committed.CommittedAt)
	// The summary is frozen at the dry run and survives the commit unchanged —
	// the promise and the receipt are the same row.
	s.Require().Equal(summary{Rows: 3, Creates: 3}, committed.Summary)

	// Three entities exist, and the hierarchy the file described was built:
	// the parent was written before the children that name it.
	rows := s.entities(tenant)
	s.Require().Len(rows, 3)
	byName := map[string]entityRow{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	group := byName["Acme Group NV"]
	s.Require().Nil(group.ParentID, "the group is top-level")
	s.Require().NotNil(byName["Acme Deutschland GmbH"].ParentID)
	s.Require().Equal(group.ID, *byName["Acme Deutschland GmbH"].ParentID)
	s.Require().Equal(group.ID, *byName["Acme France SAS"].ParentID)
	s.Require().Equal("06-30", *byName["Acme France SAS"].YearEnd)
}

// TestSecondImportOfTheSameFileChangesNothing is idempotency, proven rather
// than asserted: the second upload plans every row as 'unchanged', the commit
// writes no statement at all, and the audit trail gains no per-record entry.
func (s *ImportsSuite) TestSecondImportOfTheSameFileChangesNothing() {
	tenant := s.InsertTenant("imp-idem", "Import Idempotent").String()
	file := entityCSV(
		[]string{"Globex Holding BV", "Netherlands", "", "12-31", ""},
		[]string{"Globex Iberia SL", "Spain", "Globex Holding BV", "12-31", ""},
	)

	first := s.upload(tenant, "entities", file)
	s.Require().Equal(summary{Rows: 2, Creates: 2}, first.Batch.Summary)
	s.commit(tenant, "entities", first.Batch.ID)

	before := s.entities(tenant)
	s.Require().Len(before, 2)
	actionsBefore := s.auditActions(tenant)
	s.Require().Equal(2, countAction(actionsBefore, "entity.created"))

	// The very same bytes again.
	second := s.upload(tenant, "entities", file)
	s.Require().Equal(first.Batch.Checksum, second.Batch.Checksum, "the same file hashes the same")
	s.Require().NotEqual(first.Batch.ID, second.Batch.ID)
	s.Require().Equal(summary{Rows: 2, Unchanged: 2}, second.Batch.Summary,
		"a repeated import creates nothing and changes nothing")
	for _, row := range second.Rows {
		s.Require().Equal("unchanged", row.Action)
		s.Require().NotNil(row.TargetID, "an unchanged row names the record it matched")
		s.Require().Empty(row.ChangedFields)
	}

	s.commit(tenant, "entities", second.Batch.ID)

	after := s.entities(tenant)
	s.Require().Equal(before, after, "the second commit changed no row of the tax book")

	actionsAfter := s.auditActions(tenant)
	s.Require().Equal(2, countAction(actionsAfter, "entity.created"), "no entity was created twice")
	s.Require().Zero(countAction(actionsAfter, "entity.updated"), "and none was rewritten with its own values")
	// The operation itself is still recorded — an import that did nothing is a
	// fact an auditor may need.
	s.Require().Equal(2, countAction(actionsAfter, "import.committed"))
}

// TestEditedRowIsReportedAsAnUpdateAndOnlyThat proves the middle case: a second
// file that differs in one cell updates exactly that record, and the dry run
// names the field it will move before anything happens.
func (s *ImportsSuite) TestEditedRowIsReportedAsAnUpdateAndOnlyThat() {
	tenant := s.InsertTenant("imp-update", "Import Update").String()

	first := s.upload(tenant, "entities", entityCSV(
		[]string{"Initech Oy", "Finland", "", "12-31", ""},
		[]string{"Initech Sverige AB", "Sweden", "", "12-31", ""},
	))
	s.commit(tenant, "entities", first.Batch.ID)

	// One cell moves: Initech Oy's financial year end.
	second := s.upload(tenant, "entities", entityCSV(
		[]string{"Initech Oy", "Finland", "", "06-30", ""},
		[]string{"Initech Sverige AB", "Sweden", "", "12-31", ""},
	))
	s.Require().Equal(summary{Rows: 2, Updates: 1, Unchanged: 1}, second.Batch.Summary)
	changed := s.rowByNumber(second.Rows, 2) // header is row 1
	s.Require().Equal("update", changed.Action)
	s.Require().Equal([]string{"financialYearEnd"}, changed.ChangedFields,
		"the dry run names the field it will move, and only that field")
	// And says what it will move it FROM: a change list without the old value
	// cannot tell a correction from an erasure.
	s.Require().Equal([]changeJSON{{
		Field: "financialYearEnd", From: strp("12-31"), To: strp("06-30"),
	}}, changed.Changes)

	s.commit(tenant, "entities", second.Batch.ID)

	byName := map[string]entityRow{}
	for _, row := range s.entities(tenant) {
		byName[row.Name] = row
	}
	s.Require().Equal("06-30", *byName["Initech Oy"].YearEnd)
	s.Require().Equal("12-31", *byName["Initech Sverige AB"].YearEnd)

	actions := s.auditActions(tenant)
	s.Require().Equal(1, countAction(actions, "entity.updated"), "exactly one record was rewritten")
}

// TestOneBadRowCommitsNothingAtAll is the all-or-nothing rule.
//
// A file of three good rows and one bad one imports NOTHING: the batch is
// rejected at upload, the commit is refused, and the tenant's tax book is
// untouched. A partially imported tax book is worse than a rejected file.
func (s *ImportsSuite) TestOneBadRowCommitsNothingAtAll() {
	tenant := s.InsertTenant("imp-bad", "Import Bad").String()

	dry := s.upload(tenant, "entities", entityCSV(
		[]string{"Umbrella Holdings Ltd", "United Kingdom", "", "12-31", ""},
		[]string{"Umbrella Labs Ltd", "United Kingdom", "Umbrella Holdings Ltd", "12-31", ""},
		[]string{"Umbrella Broken Ltd", "United Kingdom", "", "whenever", ""}, // not a date at all
		[]string{"Umbrella Nordics AB", "Sweden", "Umbrella Holdings Ltd", "12-31", ""},
	))

	s.Require().Equal("rejected", dry.Batch.Status)
	s.Require().False(dry.Batch.Committable)
	s.Require().Equal(summary{Rows: 4, Creates: 3, Invalid: 1}, dry.Batch.Summary)

	broken := s.rowByNumber(dry.Rows, 4)
	s.Require().Equal("invalid", broken.Action)
	s.Require().NotEmpty(broken.Issues)
	s.Require().Equal("financialYearEnd", broken.Issues[0].Field)
	s.Require().Equal("error", broken.Issues[0].Severity)

	// The three good rows are still REPORTED — the customer sees that the rest
	// of the file is fine — but reporting is not importing.
	s.Require().Equal("create", s.rowByNumber(dry.Rows, 2).Action)

	resp := s.As(tenant).POST(s.T(), "/imports/entities/"+dry.Batch.ID+"/commit", nil)
	resp.AssertStatus(s.T(), http.StatusConflict)
	s.Require().Contains(resp.BodyString(), "cannot be imported")

	s.Require().Empty(s.entities(tenant), "not one of the three good rows was written")
	s.Require().Zero(countAction(s.auditActions(tenant), "entity.created"))
	// The refused attempt is still on the record: the dry run was recorded even
	// though nothing came of it.
	s.Require().Equal(1, countAction(s.auditActions(tenant), "import.validated"))
}

// TestCommittedBatchCannotBeCommittedTwice: a plan is applied once. The
// database holds the rule (SQLSTATE ZT036) beneath the use case's own check.
func (s *ImportsSuite) TestCommittedBatchCannotBeCommittedTwice() {
	tenant := s.InsertTenant("imp-twice", "Import Twice").String()
	dry := s.upload(tenant, "entities", entityCSV(
		[]string{"Vandelay Industries", "United States", "", "12-31", ""},
	))
	s.commit(tenant, "entities", dry.Batch.ID)

	resp := s.As(tenant).POST(s.T(), "/imports/entities/"+dry.Batch.ID+"/commit", nil)
	resp.AssertStatus(s.T(), http.StatusConflict)
	s.Require().Len(s.entities(tenant), 1, "no second copy of anything")
}

// TestPlanIsRefusedWhenTheRecordMovedUnderIt is the honesty guarantee under
// concurrency: the dry run planned an update from a specific version of a
// record, somebody edited that record in between, and the commit refuses the
// WHOLE file rather than writing over an edit nobody was shown.
func (s *ImportsSuite) TestPlanIsRefusedWhenTheRecordMovedUnderIt() {
	tenant := s.InsertTenant("imp-stale", "Import Stale").String()

	first := s.upload(tenant, "entities", entityCSV(
		[]string{"Stark Industries", "United States", "", "12-31", ""},
		[]string{"Stark Europe GmbH", "Germany", "", "12-31", ""},
	))
	s.commit(tenant, "entities", first.Batch.ID)

	// A file that would change one of them.
	second := s.upload(tenant, "entities", entityCSV(
		[]string{"Stark Industries", "Ireland", "", "12-31", ""},
		[]string{"Stark Europe GmbH", "Germany", "", "12-31", ""},
	))
	s.Require().Equal(summary{Rows: 2, Updates: 1, Unchanged: 1}, second.Batch.Summary)

	// Meanwhile somebody edits that very record through the ordinary API.
	var target entityRow
	for _, row := range s.entities(tenant) {
		if row.Name == "Stark Industries" {
			target = row
		}
	}
	s.Require().NotEmpty(target.ID)
	s.As(tenant).PUT(s.T(), "/entities/"+target.ID, map[string]any{
		"name": "Stark Industries", "country": "United States", "financialYearEnd": "03-31",
		"fiscalCalendarPattern": "standard", "status": "active",
	}).AssertStatus(s.T(), http.StatusOK)

	resp := s.As(tenant).POST(s.T(), "/imports/entities/"+second.Batch.ID+"/commit", nil)
	resp.AssertStatus(s.T(), http.StatusConflict)
	s.Require().Contains(resp.BodyString(), "edited since")
	s.Require().Contains(resp.BodyString(), "Nothing was imported")

	// The hand edit stands; the import wrote nothing over it.
	byName := map[string]entityRow{}
	for _, row := range s.entities(tenant) {
		byName[row.Name] = row
	}
	s.Require().Equal("United States", byName["Stark Industries"].Country)
	s.Require().Equal("03-31", *byName["Stark Industries"].YearEnd)
}

// TestScopedPrincipalCannotImportOutsideItsSubtree: an import is the same power
// as a create, exercised many times, so it is bounded by the same scope. A
// manager scoped to one branch may import under it and may not import beside
// it, and the dry run says which rows those are BEFORE anything happens.
func (s *ImportsSuite) TestScopedPrincipalCannotImportOutsideItsSubtree() {
	tenant := s.InsertTenant("imp-scope", "Import Scope").String()

	// Two branches, created by the tenant admin.
	root := s.upload(tenant, "entities", entityCSV(
		[]string{"Northern Group AB", "Sweden", "", "12-31", ""},
		[]string{"Southern Group SpA", "Italy", "", "12-31", ""},
	))
	s.commit(tenant, "entities", root.Batch.ID)

	var northern string
	for _, row := range s.entities(tenant) {
		if row.Name == "Northern Group AB" {
			northern = row.ID
		}
	}
	s.Require().NotEmpty(northern)

	scoped := s.AsScopedRole(tenant, "manager", northern)

	// A file that reaches into both branches: one row under Northern (allowed),
	// one under Southern (not), and one new top-level entity (not — a root is a
	// tenant-level write).
	dry := s.uploadAs(scoped, "entities", entityCSV(
		[]string{"Northern Finance AB", "Sweden", "Northern Group AB", "12-31", ""},
		[]string{"Southern Finance Srl", "Italy", "Southern Group SpA", "12-31", ""},
		[]string{"Rogue Holding Ltd", "Jersey", "", "12-31", ""},
	))

	s.Require().Equal("rejected", dry.Batch.Status)
	s.Require().Equal(summary{Rows: 3, Creates: 1, Invalid: 2}, dry.Batch.Summary)
	s.Require().Equal("create", s.rowByNumber(dry.Rows, 2).Action, "in-subtree row is fine")

	s.requireError(s.rowByNumber(dry.Rows, 3), "permission")
	s.requireError(s.rowByNumber(dry.Rows, 4), "permission")

	// And the batch cannot be committed, so not even the row they WERE allowed
	// to write is written: all or nothing applies to a scope refusal too.
	resp := scoped.POST(s.T(), "/imports/entities/"+dry.Batch.ID+"/commit", nil)
	resp.AssertStatus(s.T(), http.StatusConflict)
	s.Require().Len(s.entities(tenant), 2, "the tax book still holds only the two branches")
}

// TestUploadNeedsTheWriteCapability: a preparer may work tasks and may not
// define the compliance program, so they may not import one either. The
// tenant-wide gate is the route's declared capability, refused before the
// handler runs and before a byte of the file is read.
func (s *ImportsSuite) TestUploadNeedsTheWriteCapability() {
	tenant := s.InsertTenant("imp-cap", "Import Capability").String()

	resp := s.AsRole(tenant, "preparer").POSTMultipart(s.T(), "/imports/entities", nil, multipartEntities())
	resp.AssertStatus(s.T(), http.StatusForbidden)

	// A viewer cannot even read the history.
	s.AsRole(tenant, "viewer").
		POSTMultipart(s.T(), "/imports/entities", nil, multipartEntities()).
		AssertStatus(s.T(), http.StatusForbidden)

	s.Require().Zero(s.countRows(tenant, "import_batches"), "a refused upload creates no batch")
}

// TestAuditTrailNamesEveryRecordAndTheFileThatBroughtIt: the trail has to answer
// both questions an auditor asks — "what has ever happened to this entity" and
// "what did that import do" — and the per-record entries must be
// indistinguishable from the ones a hand-typed create leaves.
func (s *ImportsSuite) TestAuditTrailNamesEveryRecordAndTheFileThatBroughtIt() {
	tenant := s.InsertTenant("imp-audit", "Import Audit").String()

	dry := s.upload(tenant, "entities", entityCSV(
		[]string{"Wayne Enterprises", "United States", "", "12-31", ""},
		[]string{"Wayne Tech GmbH", "Germany", "Wayne Enterprises", "12-31", ""},
	))
	s.commit(tenant, "entities", dry.Batch.ID)

	actions := s.auditActions(tenant)
	s.Require().Equal(1, countAction(actions, "import.validated"))
	s.Require().Equal(1, countAction(actions, "import.committed"))
	s.Require().Equal(2, countAction(actions, "entity.created"),
		"one envelope per record, not one for the batch")

	// The per-record entries name the records themselves, so "show me every
	// change to this entity" finds them.
	created := s.entities(tenant)
	var resourceIDs []string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&resourceIDs,
			`SELECT resource_id FROM audit_log WHERE action = 'entity.created' ORDER BY seq`))
	})
	for _, entity := range created {
		s.Require().Contains(resourceIDs, entity.ID)
	}

	// The envelope carries what CHANGED, not merely that something did, and it
	// obeys the PII rule: the fiscal year end is quoted (the contract fixes its
	// shape), the company name is recorded as present and never quoted.
	var details []string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&details,
			`SELECT details::text FROM audit_log WHERE action = 'entity.created' ORDER BY seq`))
	})
	s.Require().Len(details, 2)
	for _, payload := range details {
		compact := strings.NewReplacer(" ", "", "\n", "").Replace(payload)
		s.Require().Contains(compact, `"financialYearEnd":{"to":"12-31"}`,
			"a value whose shape the contract fixes is quoted")
		s.Require().Contains(compact, `"name":{"to":"set"}`,
			"the company name is recorded as present and never quoted")
		s.Require().Contains(compact, `"country":{"to":"set"}`)
		s.Require().NotContains(payload, "Wayne", "no customer free text reaches the trail")
	}

	// The import envelope names the file by checksum — server-computed — and
	// never by the name the customer gave it.
	var importDetails string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&importDetails,
			`SELECT details::text FROM audit_log WHERE action = 'import.committed'`))
	})
	s.Require().Contains(importDetails, dry.Batch.Checksum)
	s.Require().NotContains(importDetails, "book.csv")

	// And the whole chain still verifies with the import's entries in it.
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		entries, err := audit.LoadChain(s.T().Context(), tx, tenant)
		s.Require().NoError(err)
		s.Require().NotEmpty(entries)
		s.Require().NoError(audit.VerifyChain(entries))
	})
}

// TestObligationsImportNeedsItsEntitiesAndItsTypes: the obligations file is the
// second half of a pilot's tax book, and it imports against records that must
// already exist. An unknown entity or obligation type is a row error naming the
// value, never a silently created record.
func (s *ImportsSuite) TestObligationsImportNeedsItsEntitiesAndItsTypes() {
	tenant := s.InsertTenant("imp-oblig", "Import Obligations").String()
	s.seedObligationType(tenant, "VAT-RET", "VAT return")

	entities := s.upload(tenant, "entities", entityCSV(
		[]string{"Cyberdyne Systems", "United States", "", "12-31", ""},
	))
	s.commit(tenant, "entities", entities.Batch.ID)

	dry := s.upload(tenant, "entity-obligations", obligationCSV(
		[]string{"Cyberdyne Systems", "VAT-RET", "Monthly", "20", "USD"},
		[]string{"Skynet Ltd", "VAT-RET", "Monthly", "20", "USD"},     // unknown entity
		[]string{"Cyberdyne Systems", "CIT-ANN", "Annual", "", "USD"}, // unknown obligation type
	))
	s.Require().Equal("rejected", dry.Batch.Status)
	s.Require().Equal(summary{Rows: 3, Creates: 1, Invalid: 2}, dry.Batch.Summary)
	s.requireError(s.rowByNumber(dry.Rows, 3), "import the entities first")
	s.requireError(s.rowByNumber(dry.Rows, 4), "no obligation type matches")

	// With the file corrected, it commits — and a second run of it is a no-op.
	good := obligationCSV([]string{"Cyberdyne Systems", "VAT-RET", "Monthly", "20", "USD"})
	clean := s.upload(tenant, "entity-obligations", good)
	s.Require().Equal(summary{Rows: 1, Creates: 1}, clean.Batch.Summary)
	s.commit(tenant, "entity-obligations", clean.Batch.ID)
	s.Require().Equal(1, s.countRows(tenant, "entity_obligations"))

	again := s.upload(tenant, "entity-obligations", good)
	s.Require().Equal(summary{Rows: 1, Unchanged: 1}, again.Batch.Summary)
	s.commit(tenant, "entity-obligations", again.Batch.ID)
	s.Require().Equal(1, s.countRows(tenant, "entity_obligations"), "no second copy")

	actions := s.auditActions(tenant)
	s.Require().Equal(1, countAction(actions, "entity_obligation.created"))
	s.Require().Zero(countAction(actions, "entity_obligation.updated"))
}

// TestFileTooLargeIsRefusedBeforeItIsParsed and its row-count sibling are the
// bounds. Both answer 413 and neither leaves a batch behind, because refusing
// an unbounded import has to be cheaper than accepting one.
func (s *ImportsSuite) TestBoundsAreEnforcedBeforeAnythingIsParsed() {
	tenant := s.InsertTenant("imp-bounds", "Import Bounds").String()

	// Well past the acceptance harness's file cap.
	huge := make([]byte, 3*1024*1024)
	for i := range huge {
		huge[i] = 'a'
	}
	resp := s.As(tenant).POSTMultipart(s.T(), "/imports/entities", nil, multipartFile(huge))
	resp.AssertStatus(s.T(), http.StatusRequestEntityTooLarge)
	s.Require().Contains(resp.BodyString(), "FILE_TOO_LARGE")

	// Under the byte cap, over the row cap (the harness sets a small one).
	rows := make([][]string, 0, 64)
	for i := 0; i < 64; i++ {
		rows = append(rows, []string{"Row Entity " + strings.Repeat("x", i%5) + itoa(i), "Germany", "", "12-31", ""})
	}
	resp = s.As(tenant).POSTMultipart(s.T(), "/imports/entities", nil, multipartFile(entityCSV(rows...)))
	resp.AssertStatus(s.T(), http.StatusRequestEntityTooLarge)

	s.Require().Zero(s.countRows(tenant, "import_batches"), "a refused upload stores nothing")
	s.Require().Empty(s.entities(tenant))
}

// TestTenantIsolation: a batch belongs to the tenant that uploaded it, and
// another tenant cannot see it, read its plan or commit it.
func (s *ImportsSuite) TestTenantIsolation() {
	tenantA := s.InsertTenant("imp-iso-a", "Import Iso A").String()
	tenantB := s.InsertTenant("imp-iso-b", "Import Iso B").String()

	dry := s.upload(tenantA, "entities", entityCSV(
		[]string{"Hooli Inc", "United States", "", "12-31", ""},
	))

	s.As(tenantB).GET(s.T(), "/imports/entities/"+dry.Batch.ID).AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenantB).GET(s.T(), "/imports/entities/"+dry.Batch.ID+"/rows").AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenantB).POST(s.T(), "/imports/entities/"+dry.Batch.ID+"/commit", nil).AssertStatus(s.T(), http.StatusNotFound)

	// The kind is part of a batch's identity too: an entities batch is not
	// reachable through the obligations routes.
	s.As(tenantA).GET(s.T(), "/imports/entity-obligations/"+dry.Batch.ID).AssertStatus(s.T(), http.StatusNotFound)

	s.Require().Empty(s.entities(tenantB))
}

// TestRowsReportIsPagedAndInFileOrder: the report is read as a document, and a
// row number in it is a row number in the customer's spreadsheet.
func (s *ImportsSuite) TestRowsReportIsPagedAndInFileOrder() {
	tenant := s.InsertTenant("imp-rows", "Import Rows").String()
	dry := s.upload(tenant, "entities", entityCSV(
		[]string{"Paged One Ltd", "United Kingdom", "", "12-31", ""},
		[]string{"Paged Two Ltd", "United Kingdom", "", "12-31", ""},
		[]string{"Paged Three Ltd", "United Kingdom", "", "12-31", ""},
	))

	resp := s.As(tenant).GET(s.T(), "/imports/entities/"+dry.Batch.ID+"/rows?limit=2&offset=0")
	resp.AssertStatus(s.T(), http.StatusOK)
	var page []rowJSON
	resp.DecodeData(s.T(), &page)
	s.Require().Len(page, 2)
	s.Require().Equal(2, page[0].Row, "the header is row 1, so the first data row is 2")
	s.Require().Equal(3, page[1].Row)
	s.Require().Contains(resp.BodyString(), `"total":3`)
	s.Require().Contains(resp.BodyString(), `"hasMore":true`)
}

// TestWarningsReachTheReportWithoutBlockingTheCommit: a row can be legal and
// still not be what the customer meant. The dry run says so, the row still
// imports, and the remark is on the record — dropping it from a valid row would
// leave the customer to discover it in a filing.
func (s *ImportsSuite) TestWarningsReachTheReportWithoutBlockingTheCommit() {
	tenant := s.InsertTenant("imp-warn", "Import Warnings").String()
	s.seedObligationType(tenant, "VAT-RET", "VAT return")

	entities := s.upload(tenant, "entities", entityCSV(
		[]string{"Soylent Corp", "United States", "", "12-31", ""},
	))
	s.commit(tenant, "entities", entities.Batch.ID)

	// No deadline columns at all: the obligation can be recorded, but nothing
	// will compute a filing date from it until a rule is set.
	dry := s.upload(tenant, "entity-obligations", obligationCSV(
		[]string{"Soylent Corp", "VAT-RET", "Monthly", "", "USD"},
	))
	s.Require().Equal("validated", dry.Batch.Status, "a warning never refuses a file")
	s.Require().Equal(summary{Rows: 1, Creates: 1}, dry.Batch.Summary)

	row := s.rowByNumber(dry.Rows, 2)
	s.Require().Equal("create", row.Action)
	s.requireIssue(row, "deadline")
	s.Require().Equal("warning", row.Issues[0].Severity)

	// And the warning is stored with the plan, not merely computed for the
	// upload's own response: the row-by-row endpoint serves it back.
	resp := s.As(tenant).GET(s.T(), "/imports/entity-obligations/"+dry.Batch.ID+"/rows")
	resp.AssertStatus(s.T(), http.StatusOK)
	var stored []rowJSON
	resp.DecodeData(s.T(), &stored)
	s.Require().Len(stored, 1)
	s.requireIssue(stored[0], "deadline")

	s.commit(tenant, "entity-obligations", dry.Batch.ID)
	s.Require().Equal(1, s.countRows(tenant, "entity_obligations"))
}

// ── an update merges; it does not replace ───────────────────────────────────
//
// The three tests below are one customer's story, told three ways. A pilot
// loads their register in full. Months later they export names and countries
// out of their own system and import that on top, because those are the two
// columns they maintain there. What must NOT happen is the thing a replace
// would do: erase the legal names, the parents, the tax residencies and the
// 4-4-5 calendars they never mentioned.

// fullRegisterCSV is the register as a pilot first loads it: every column
// filled in.
func fullRegisterCSV(rows ...[]string) []byte {
	return csvFile(
		"Entity Name,Country,Parent,Financial Year End,Legal Name,Tax Residency,Fiscal Calendar Pattern",
		rows...)
}

// meridianRegister is that file, for the tenant these tests share the shape of:
// a group and the subsidiary under it, both on a 4-4-5 calendar with a March
// year end.
func meridianRegister() []byte {
	return fullRegisterCSV(
		[]string{"Meridian Group", "United Kingdom", "", "03-31", "Meridian Group Holdings PLC", "United Kingdom", "445"},
		[]string{"Meridian UK Trading", "United Kingdom", "Meridian Group", "03-31", "Meridian UK Trading Limited", "United Kingdom", "445"},
	)
}

// TestASheetOfNamesAndCountriesChangesNothingElse: the file has no legal name,
// parent, tax residency, year end or calendar column, so it says nothing about
// any of them — and nothing is what happens to them. Every row reports as no
// change, and the commit writes no statement at all.
func (s *ImportsSuite) TestASheetOfNamesAndCountriesChangesNothingElse() {
	tenant := s.InsertTenant("imp-merge-quiet", "Import Merge Quiet").String()

	loaded := s.upload(tenant, "entities", meridianRegister())
	s.Require().Equal(summary{Rows: 2, Creates: 2}, loaded.Batch.Summary)
	s.commit(tenant, "entities", loaded.Batch.ID)

	before := s.entities(tenant)
	s.Require().Len(before, 2)
	trading := s.entitiesByName(tenant)["Meridian UK Trading"]
	s.Require().Equal("445", trading.Pattern, "the register really did land in full")
	s.Require().Equal("03-31", *trading.YearEnd)
	s.Require().NotNil(trading.ParentID)
	s.Require().Equal("United Kingdom", *trading.TaxResidency)

	// Months later: two columns, out of the system that owns those two columns.
	second := s.upload(tenant, "entities", csvFile("Entity Name,Country",
		[]string{"Meridian Group", "United Kingdom"},
		[]string{"Meridian UK Trading", "United Kingdom"},
	))
	s.Require().Equal(summary{Rows: 2, Unchanged: 2}, second.Batch.Summary,
		"a column the file does not have is a column the customer said nothing about")
	for _, row := range second.Rows {
		s.Require().Equal("unchanged", row.Action, "row %d", row.Row)
		s.Require().Empty(row.ChangedFields)
		s.Require().Empty(row.Changes)
	}

	s.commit(tenant, "entities", second.Batch.ID)

	s.Require().Equal(before, s.entities(tenant),
		"every legal name, parent, residency, year end and calendar is exactly as it was")
	s.Require().Zero(countAction(s.auditActions(tenant), "entity.updated"),
		"and no record was rewritten at all")
}

// TestAnEmptyCellInAColumnTheFileIncludedClearsThatFieldAndOnlyThat is the
// other half of the ruling. The customer adds a tax residency column and leaves
// it blank: that IS a statement — "these entities have no tax residency" — so
// the import makes exactly that change, says so before it does, and touches
// nothing else.
func (s *ImportsSuite) TestAnEmptyCellInAColumnTheFileIncludedClearsThatFieldAndOnlyThat() {
	tenant := s.InsertTenant("imp-merge-clear", "Import Merge Clear").String()

	loaded := s.upload(tenant, "entities", meridianRegister())
	s.commit(tenant, "entities", loaded.Batch.ID)
	before := s.entitiesByName(tenant)

	second := s.upload(tenant, "entities", csvFile("Entity Name,Country,Tax Residency",
		[]string{"Meridian Group", "United Kingdom", ""},
		[]string{"Meridian UK Trading", "United Kingdom", ""},
	))
	s.Require().Equal(summary{Rows: 2, Updates: 2}, second.Batch.Summary)
	for _, row := range second.Rows {
		s.Require().Equal("update", row.Action, "row %d", row.Row)
		s.Require().Equal([]string{"taxResidency"}, row.ChangedFields, "row %d", row.Row)
		// The preview says what it will do, from what to what, before the
		// customer agrees to it.
		s.Require().Equal([]changeJSON{{
			Field: "taxResidency", From: strp("United Kingdom"), To: nil,
		}}, row.Changes, "row %d", row.Row)
	}

	s.commit(tenant, "entities", second.Batch.ID)

	after := s.entitiesByName(tenant)
	for name, was := range before {
		now := after[name]
		s.Require().Nil(now.TaxResidency, "%s: the empty cell cleared the residency", name)
		// And the one change is the only change: everything the file had no
		// column for stands.
		was.TaxResidency = nil
		s.Require().Equal(was, now, "%s: nothing else moved", name)
	}
	s.Require().Equal(2, countAction(s.auditActions(tenant), "entity.updated"))
}

// TestACreateFromThatSameSheetTakesTheDocumentedDefaults: on a NEW record none
// of this arises — there is nothing to preserve — so the two-column sheet that
// leaves an existing entity alone gives a new one the defaults the column
// tables document.
func (s *ImportsSuite) TestACreateFromThatSameSheetTakesTheDocumentedDefaults() {
	tenant := s.InsertTenant("imp-merge-create", "Import Merge Create").String()

	dry := s.upload(tenant, "entities", csvFile("Entity Name,Country",
		[]string{"Meridian Ireland", "Ireland"},
	))
	s.Require().Equal(summary{Rows: 1, Creates: 1}, dry.Batch.Summary)

	// The dry run already shows the defaults it would write, so they are checked
	// before the commit rather than discovered after it.
	planned := s.rowByNumber(dry.Rows, 2)
	s.Require().Equal("standard", planned.Record.FiscalCalendarPattern)
	s.Require().Equal("saturday", planned.Record.FiscalWeekEndDay)
	s.Require().Equal("nearest", planned.Record.FiscalYearEndRule)

	s.commit(tenant, "entities", dry.Batch.ID)

	created := s.entitiesByName(tenant)["Meridian Ireland"]
	s.Require().Equal("Ireland", created.Country)
	s.Require().Equal("standard", created.Pattern, "blank means standard")
	s.Require().Equal("saturday", created.WeekEndDay)
	s.Require().Equal("nearest", created.YearEndRule)
	s.Require().Nil(created.YearEnd, "no year end column, and nothing to preserve")
	s.Require().Nil(created.LegalName)
	s.Require().Nil(created.TaxResidency)
	s.Require().Nil(created.ParentID, "a top-level entity")
}

// TestCorrectingAVATNumberKeepsTheFilingCalendar is the same rule on the record
// where breaking it costs the most. A four-column correction file — entity, tax
// type, VAT number, frequency — moves the VAT number and leaves the quarterly
// filing rule, the jurisdiction and the currency exactly where they are. It
// also raises no deadline remark: nothing happens to the deadlines, so there is
// nothing to remark on.
func (s *ImportsSuite) TestCorrectingAVATNumberKeepsTheFilingCalendar() {
	tenant := s.InsertTenant("imp-merge-oblig", "Import Merge Obligation").String()
	s.seedObligationType(tenant, "VAT-RET", "VAT return")

	entities := s.upload(tenant, "entities", entityCSV(
		[]string{"Meridian UK Trading", "United Kingdom", "", "03-31", ""},
	))
	s.commit(tenant, "entities", entities.Batch.ID)

	registered := s.upload(tenant, "entity-obligations", csvFile(
		"Entity,Obligation Type,Frequency,Filing Offset Months,Filing Offset Days,Weekend Adjustment,Jurisdiction,Currency,Tax Reference Number",
		[]string{"Meridian UK Trading", "VAT-RET", "quarterly", "1", "7", "next-business-day", "United Kingdom", "GBP", "GB998877665"},
	))
	s.Require().Equal(summary{Rows: 1, Creates: 1}, registered.Batch.Summary)
	s.commit(tenant, "entity-obligations", registered.Batch.ID)

	rule := s.obligations(tenant)[0].DeadlineRule
	s.Require().Contains(rule, `"period_offset"`)
	s.Require().Contains(rule, `"days": 7`)

	// The most mundane correction file a pilot sends second.
	correction := s.upload(tenant, "entity-obligations", csvFile(
		"Entity,Tax type,VAT number,Filing frequency",
		[]string{"Meridian UK Trading", "VAT-RET", "GB998877666", "quarterly"},
	))
	s.Require().Equal(summary{Rows: 1, Updates: 1}, correction.Batch.Summary)

	row := s.rowByNumber(correction.Rows, 2)
	s.Require().Equal([]changeJSON{{
		Field: "taxReferenceNumber", From: strp("GB998877665"), To: strp("GB998877666"),
	}}, row.Changes, "one column moved, so one field changes")
	s.Require().Empty(row.Issues,
		"and no remark about deadlines, because the deadline rule is not being touched")

	s.commit(tenant, "entity-obligations", correction.Batch.ID)

	after := s.obligations(tenant)
	s.Require().Len(after, 1)
	s.Require().Equal("GB998877666", *after[0].TaxReferenceNumber)
	s.Require().Equal("United Kingdom", *after[0].Jurisdiction, "the jurisdiction was not in the file")
	s.Require().Equal("GBP", *after[0].Currency, "nor the currency")
	s.Require().Equal(rule, after[0].DeadlineRule, "and the filing calendar is byte for byte what it was")
}

// TestTemplateIsServedByTheProduct: the column set is server authority, so the
// product hands it out rather than leaving it to a manual.
func (s *ImportsSuite) TestTemplateIsServedByTheProduct() {
	tenant := s.InsertTenant("imp-tmpl", "Import Template").String()

	resp := s.As(tenant).GET(s.T(), "/imports/entities/template")
	resp.AssertStatus(s.T(), http.StatusOK)
	var out struct {
		Kind   string `json:"kind"`
		Fields []struct {
			Name     string   `json:"name"`
			Required bool     `json:"required"`
			Aliases  []string `json:"aliases"`
		} `json:"fields"`
	}
	resp.DecodeData(s.T(), &out)
	s.Require().Equal("entities", out.Kind)
	s.Require().NotEmpty(out.Fields)

	required := map[string]bool{}
	for _, field := range out.Fields {
		if field.Required {
			required[field.Name] = true
		}
	}
	s.Require().True(required["name"])
	s.Require().True(required["country"])
}
