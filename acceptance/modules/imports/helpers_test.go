package imports_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// ImportsSuite drives spreadsheet ingest (PB-C5) end to end over HTTP against a
// real Postgres: upload → dry run → commit, the bounds, the all-or-nothing
// rule, idempotency, entity-subtree scope, and the audit trail the whole thing
// has to leave behind.
type ImportsSuite struct {
	acceptance.Suite
}

func TestImportsSuite(t *testing.T) {
	suite.Run(t, new(ImportsSuite))
}

// ── fixtures ────────────────────────────────────────────────────────────────

// entityCSV builds an entities file. The header is written in a customer's
// spelling ("Entity Name", "Financial Year End") rather than in the API's field
// names, so every test also exercises the header binding a real export needs.
func entityCSV(rows ...[]string) []byte {
	return csvFile("Entity Name,Country,Parent,Financial Year End,Legal Name", rows...)
}

func obligationCSV(rows ...[]string) []byte {
	return csvFile("Entity,Obligation Type,Frequency,Filing Offset Days,Currency", rows...)
}

// csvFile is a file whose COLUMNS the test chooses. Which columns a file
// carries is the whole subject of the merge rule — a sheet that is the
// authority on names and countries has two columns, not nine — so those tests
// write their own header rather than inheriting one.
func csvFile(header string, rows ...[]string) []byte {
	lines := []string{header}
	for _, row := range rows {
		lines = append(lines, strings.Join(row, ","))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// ── responses ───────────────────────────────────────────────────────────────

type summary struct {
	Rows      int `json:"rows"`
	Creates   int `json:"creates"`
	Updates   int `json:"updates"`
	Unchanged int `json:"unchanged"`
	Invalid   int `json:"invalid"`
}

type batchJSON struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	Committable bool    `json:"committable"`
	Checksum    string  `json:"checksum"`
	Summary     summary `json:"summary"`
	CommittedAt *string `json:"committedAt"`
}

type rowJSON struct {
	Row           int          `json:"row"`
	Action        string       `json:"action"`
	Key           string       `json:"key"`
	TargetID      *string      `json:"targetId"`
	ChangedFields []string     `json:"changedFields"`
	Changes       []changeJSON `json:"changes"`
	Record        struct {
		Name                  string  `json:"name"`
		Country               string  `json:"country"`
		LegalName             *string `json:"legalName"`
		TaxResidency          *string `json:"taxResidency"`
		FiscalCalendarPattern string  `json:"fiscalCalendarPattern"`
		FinancialYearEnd      *string `json:"financialYearEnd"`
		FiscalWeekEndDay      string  `json:"fiscalWeekEndDay"`
		FiscalYearEndRule     string  `json:"fiscalYearEndRule"`
	} `json:"record"`
	Issues []struct {
		Severity string `json:"severity"`
		Field    string `json:"field"`
		Message  string `json:"message"`
	} `json:"issues"`
}

// changeJSON is one field an update would move, with the value stored now and
// the value the file would write — the difference between a preview and a
// promise.
type changeJSON struct {
	Field string  `json:"field"`
	From  *string `json:"from"`
	To    *string `json:"to"`
}

type dryRunJSON struct {
	Batch  batchJSON `json:"batch"`
	Rows   []rowJSON `json:"rows"`
	Issues []struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"fileIssues"`
	Columns struct {
		HeaderRow int               `json:"headerRow"`
		Bound     map[string]string `json:"bound"`
		Ignored   []struct {
			Column string `json:"column"`
			Header string `json:"header"`
		} `json:"notImported"`
	} `json:"columns"`
}

// ── drivers ─────────────────────────────────────────────────────────────────

// upload posts a file as the tenant's admin and returns the dry run.
func (s *ImportsSuite) upload(tenant, kind string, content []byte) dryRunJSON {
	return s.uploadAs(s.As(tenant), kind, content)
}

func (s *ImportsSuite) uploadAs(builder *acceptance.RequestBuilder, kind string, content []byte) dryRunJSON {
	resp := builder.POSTMultipart(s.T(), "/imports/"+kind, nil, acceptance.MultipartFile{
		FileName: "book.csv", ContentType: "text/csv", Content: content,
	})
	resp.AssertStatus(s.T(), http.StatusCreated)
	var out dryRunJSON
	resp.DecodeData(s.T(), &out)
	return out
}

func (s *ImportsSuite) commit(tenant, kind, batchID string) batchJSON {
	resp := s.As(tenant).POST(s.T(), "/imports/"+kind+"/"+batchID+"/commit", nil)
	resp.AssertStatus(s.T(), http.StatusOK)
	var out batchJSON
	resp.DecodeData(s.T(), &out)
	return out
}

// seedObligationType creates the obligation type an obligations file refers to.
// The importer never creates one — an unknown code is a row error — so every
// obligation test has to put the catalogue in place first, exactly as a pilot
// would.
func (s *ImportsSuite) seedObligationType(tenant, code, name string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.As(tenant).POST(s.T(), "/obligation-types", map[string]any{
		"name": name, "code": code, "template": "VAT",
	})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

// ── assertions on the data ──────────────────────────────────────────────────

// entityRow is an entity's whole importable surface, because the question the
// merge tests ask is what a commit left ALONE — and a column the assertion does
// not read is a column an import could quietly have emptied.
type entityRow struct {
	ID           string  `db:"id"`
	Name         string  `db:"name"`
	Country      string  `db:"country"`
	ParentID     *string `db:"parent_entity_id"`
	YearEnd      *string `db:"financial_year_end"`
	LegalName    *string `db:"legal_name"`
	TaxResidency *string `db:"tax_residency"`
	Pattern      string  `db:"fiscal_calendar_pattern"`
	WeekEndDay   string  `db:"fiscal_week_end_day"`
	YearEndRule  string  `db:"fiscal_year_end_rule"`
}

// entities reads a tenant's entities straight from Postgres, inside the tenant
// GUC so RLS applies — the state of the world, not the API's view of it.
func (s *ImportsSuite) entities(tenant string) []entityRow {
	var rows []entityRow
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&rows,
			`SELECT id, name, country, parent_entity_id, financial_year_end, legal_name,
			        tax_residency, fiscal_calendar_pattern, fiscal_week_end_day, fiscal_year_end_rule
			   FROM entities ORDER BY name`))
	})
	return rows
}

// entitiesByName is the same read, addressed the way a test talks about it.
func (s *ImportsSuite) entitiesByName(tenant string) map[string]entityRow {
	byName := map[string]entityRow{}
	for _, row := range s.entities(tenant) {
		byName[row.Name] = row
	}
	return byName
}

// obligationRow is an entity obligation as it is stored, with the deadline rule
// as text: the rule is what dates a statutory filing, so a test that claims an
// import left it alone has to look at it.
type obligationRow struct {
	ID                 string  `db:"id"`
	TaxReferenceNumber *string `db:"tax_reference_number"`
	Jurisdiction       *string `db:"jurisdiction"`
	Currency           *string `db:"currency"`
	Periodicity        string  `db:"periodicity"`
	DeadlineRule       string  `db:"deadline_rule"`
}

func (s *ImportsSuite) obligations(tenant string) []obligationRow {
	var rows []obligationRow
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&rows,
			`SELECT id, tax_reference_number, jurisdiction, currency, periodicity,
			        deadline_rule::text AS deadline_rule
			   FROM entity_obligations ORDER BY id`))
	})
	return rows
}

func (s *ImportsSuite) countRows(tenant, table string) int {
	var n int
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&n, `SELECT COUNT(*)::int FROM `+table)) //nolint:gosec // fixed literals only
	})
	return n
}

// auditActions is the tenant's audit trail as (action, resource_type) pairs in
// chain order — what the trail SAYS happened.
func (s *ImportsSuite) auditActions(tenant string) []string {
	var actions []string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&actions,
			`SELECT action FROM audit_log WHERE tenant_id = $1 ORDER BY seq`, tenant))
	})
	return actions
}

func (s *ImportsSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

func (s *ImportsSuite) rowByNumber(rows []rowJSON, number int) rowJSON {
	for _, row := range rows {
		if row.Row == number {
			return row
		}
	}
	s.Require().Failf("row not in report", "no row %d among %d rows", number, len(rows))
	return rowJSON{}
}

// multipartFile wraps raw bytes as the "file" part.
func multipartFile(content []byte) acceptance.MultipartFile {
	return acceptance.MultipartFile{FileName: "book.csv", ContentType: "text/csv", Content: content}
}

// multipartEntities is a minimal, valid entities file — for the tests that are
// about who may upload rather than about what the file says.
func multipartEntities() acceptance.MultipartFile {
	return multipartFile(entityCSV([]string{"Capability Probe Ltd", "Ireland", "", "12-31", ""}))
}

func itoa(n int) string { return strconv.Itoa(n) }

// strp is a pointer to a literal, for the change list's optional values — where
// nil is "empty" and has to be distinguishable from the empty string.
func strp(s string) *string { return &s }

// requireIssue asserts that one of a row's issues carries the substring. A row
// may legitimately raise several — a refusal and a remark about something else
// entirely — and asserting on the first would make the test depend on the order
// the reading half happens to check things in.
func (s *ImportsSuite) requireIssue(row rowJSON, substring string) {
	for _, issue := range row.Issues {
		if strings.Contains(issue.Message, substring) {
			return
		}
	}
	s.Require().Failf("issue not raised", "row %d raised %v, none containing %q", row.Row, issueMessages(row), substring)
}

// requireError asserts the row was refused and says why.
func (s *ImportsSuite) requireError(row rowJSON, substring string) {
	s.Require().Equal("invalid", row.Action, "row %d: %v", row.Row, issueMessages(row))
	s.requireIssue(row, substring)
}

func issueMessages(row rowJSON) []string {
	out := make([]string, 0, len(row.Issues))
	for _, issue := range row.Issues {
		out = append(out, issue.Severity+": "+issue.Message)
	}
	return out
}

func countAction(actions []string, want string) int {
	n := 0
	for _, a := range actions {
		if a == want {
			n++
		}
	}
	return n
}
