package correlation_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// CorrelationSuite is about the question an incident review actually asks:
// "this session signed in from an address nobody recognises — what did it
// change?"
//
// Answering it needs BOTH ADR-0008 streams and a field that joins them. The
// security stream knows a session began; the audit trail knows a row changed;
// until the credential reached the audit envelope, the only shared field was the
// actor, and two live sessions of one account — the owner's and an attacker's —
// were the same actor in the trail.
//
// The second half of the suite is the other side of the same envelope: the one
// value in it a CALLER supplies. audit_log is hash-chained and exported to a
// bucket that keeps it for a decade and lets nobody delete from it, so
// X-Request-Id must never be a place a caller can write text.
type CorrelationSuite struct {
	acceptance.Suite
}

func TestCorrelationSuite(t *testing.T) {
	suite.Run(t, new(CorrelationSuite))
}

const cookieName = "zentax_session"

type auditRow struct {
	Seq            int64  `db:"seq"`
	Action         string `db:"action"`
	ResourceID     string `db:"resource_id"`
	RequestID      string `db:"request_id"`
	CredentialID   string `db:"credential_id"`
	CredentialKind string `db:"credential_kind"`
	Raw            string `db:"raw"`
}

// trail reads the tenant's audit rows the way an operator with database access
// would, including the columns the read API does not expose. `raw` is the whole
// row rendered as text, so an assertion about what is NOT stored covers every
// column rather than the ones this struct names.
func (s *CorrelationSuite) trail(tenantID string) []auditRow {
	s.T().Helper()
	var rows []auditRow
	s.inTenant(tenantID, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&rows, `
			SELECT seq, action, resource_id, request_id,
			       COALESCE(credential_id::text, '') AS credential_id,
			       COALESCE(credential_kind, '')     AS credential_kind,
			       a::text                           AS raw
			  FROM audit_log a
			 WHERE tenant_id = $1
			 ORDER BY seq ASC`, tenantID))
	})
	return rows
}

func (s *CorrelationSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	s.T().Helper()
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

// login performs a REAL sign-in and returns the cookie plus the session id the
// security stream recorded for it. The id is read from the stream rather than
// from `sessions`, because the join under test is between the two STREAMS.
func (s *CorrelationSuite) login(email, password string) (token, sessionID string) {
	s.T().Helper()
	before := s.loginSessions()

	resp := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": email, "password": password})
	resp.AssertStatus(s.T(), http.StatusOK)
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			token = c.Value
		}
	}
	s.Require().NotEmpty(token, "login must set a session cookie")

	// The session this login minted is the one the stream did not name before
	// it — a set difference rather than "the newest row", because two logins
	// can land in the same microsecond.
	var fresh []string
	for id := range s.loginSessions() {
		if !before[id] {
			fresh = append(fresh, id)
		}
	}
	s.Require().Len(fresh, 1, "one login, one new session on the security stream")
	return token, fresh[0]
}

// loginSessions is every session the stream has recorded a successful login
// for, as a set.
func (s *CorrelationSuite) loginSessions() map[string]bool {
	s.T().Helper()
	var ids []string
	s.inStream(func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&ids, `
			SELECT session_id::text FROM security_events
			 WHERE event = 'auth.login.succeeded' AND session_id IS NOT NULL`))
	})
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	return seen
}

// inStream opens the security stream's explicit cross-tenant read door — the
// seam an investigator or a log shipper uses. There is no HTTP route onto that
// table by design.
func (s *CorrelationSuite) inStream(fn func(tx *sqlx.Tx)) {
	s.T().Helper()
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.security_stream', 'on', true)`)
	s.Require().NoError(err)
	fn(tx)
}

func (s *CorrelationSuite) createEntity(req *acceptance.RequestBuilder, name string) string {
	s.T().Helper()
	var entity struct {
		ID string `json:"id"`
	}
	r := req.POST(s.T(), "/entities", map[string]any{
		"name": name, "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)
	return entity.ID
}

// THE BLOCKER, END TO END. Two sessions of ONE account each change something.
// The trail must say which session made which change, and the investigator's
// query — a join of the two stores on the credential — must return one session's
// changes and not the other's.
func (s *CorrelationSuite) TestAChangeNamesTheSessionThatMadeItAndTheTwoStoresJoin() {
	tenant := s.InsertTenant("corr-session", "Correlation Tenant").String()
	userID := s.InsertUserWithPassword(uuid.MustParse(tenant), "chief@acme.com", "s3cret-password")

	// The session an investigation would start from, and a second one for the
	// same person — the case that makes attribution-by-actor useless.
	suspectToken, suspectSession := s.login("chief@acme.com", "s3cret-password")
	ownerToken, ownerSession := s.login("chief@acme.com", "s3cret-password")
	s.Require().NotEqual(suspectSession, ownerSession, "two logins are two sessions")

	suspectEntity := s.createEntity(s.Client.External().WithSession(suspectToken), "Exfiltrated GmbH")
	ownerEntity := s.createEntity(s.Client.External().WithSession(ownerToken), "Ordinary GmbH")

	rows := s.trail(tenant)
	s.Require().Len(rows, 2, "one entry per creation: %v", rows)

	byResource := map[string]auditRow{}
	for _, r := range rows {
		s.Require().Equal(app.CredentialSession, r.CredentialKind,
			"a change made through a session cookie is attributed to that session")
		s.Require().Equal(userID.String(), s.actorOf(tenant, r.Seq),
			"the principal is still named; the credential is in ADDITION to it")
		byResource[r.ResourceID] = r
	}
	s.Require().Equal(suspectSession, byResource[suspectEntity].CredentialID)
	s.Require().Equal(ownerSession, byResource[ownerEntity].CredentialID)
	s.Require().NotEqual(byResource[suspectEntity].CredentialID, byResource[ownerEntity].CredentialID,
		"two concurrent sessions of one account must not read as one actor")

	// THE INVESTIGATOR'S QUERY: start from the suspicious login on the security
	// stream, and ask the domain trail what that session then did. One
	// statement, both stores, joined on the credential.
	type finding struct {
		Action    string `db:"action"`
		Resource  string `db:"resource_id"`
		ClientIP  string `db:"client_ip"`
		SessionID string `db:"session_id"`
	}
	var found []finding
	s.inStream(func(tx *sqlx.Tx) {
		_, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant)
		s.Require().NoError(err)
		s.Require().NoError(tx.Select(&found, `
			SELECT a.action, a.resource_id::text AS resource_id,
			       host(e.client_ip) AS client_ip, e.session_id::text AS session_id
			  FROM security_events e
			  JOIN audit_log a ON a.credential_id = e.session_id
			 WHERE e.event = 'auth.login.succeeded'
			   AND e.session_id = $1::uuid
			 ORDER BY a.seq`, suspectSession))
	})
	s.Require().Len(found, 1,
		"the join must return exactly what the suspicious session changed: %v", found)
	s.Require().Equal("entity.created", found[0].Action)
	s.Require().Equal(suspectEntity, found[0].Resource,
		"the other session's change must not be attributed to this one")
	s.Require().Equal("127.0.0.1", found[0].ClientIP,
		"the join carries the address the session signed in from — the point of joining at all")

	// And the credential is INSIDE the hash: the chain still verifies from the
	// stored rows, so re-attributing a change to another session cannot be done
	// quietly.
	s.Require().NoError(audit.VerifyChain(s.chainOf(tenant)),
		"the chain must verify from the stored rows, credential included")
}

// actorOf reads one entry's actor, to show the credential is an ADDITION to
// attribution-by-principal rather than a replacement for it.
func (s *CorrelationSuite) actorOf(tenantID string, seq int64) string {
	s.T().Helper()
	var actor string
	s.inTenant(tenantID, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&actor,
			`SELECT actor_id::text FROM audit_log WHERE tenant_id = $1 AND seq = $2`, tenantID, seq))
	})
	return actor
}

func (s *CorrelationSuite) chainOf(tenantID string) []audit.Entry {
	s.T().Helper()
	exec := database.NewExec(s.DB)
	ctx := app.WithTenantID(context.Background(), tenantID)
	var entries []audit.Entry
	s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		entries, err = audit.LoadChain(ctx, exec, tenantID)
		return err
	}))
	return entries
}

// The machine half of the same hole: api_token.issued names a token, and until
// now nothing said which token made a later change — a tenant with two tokens
// for one service account could not tell them apart.
func (s *CorrelationSuite) TestAMachineChangeNamesTheTokenThatMadeIt() {
	tenant := s.InsertTenant("corr-token", "Correlation Machine").String()
	admin := s.As(tenant)

	var sa struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/service-accounts", map[string]any{"name": "nightly-filer", "role": "manager"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &sa)

	issue := func(label string) (id, token string) {
		var tok struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		resp := admin.POST(s.T(), "/service-accounts/"+sa.ID+"/tokens",
			map[string]any{"label": label, "expiresInDays": 90})
		resp.AssertStatus(s.T(), http.StatusCreated)
		resp.DecodeData(s.T(), &tok)
		return tok.ID, tok.Token
	}
	primaryID, primary := issue("primary")
	rotationID, rotation := issue("rotation")
	s.Require().NotEqual(primaryID, rotationID)

	byPrimary := s.createEntity(s.Client.External().WithBearer(primary), "Filed By Primary")
	byRotation := s.createEntity(s.Client.External().WithBearer(rotation), "Filed By Rotation")

	creations := map[string]auditRow{}
	for _, row := range s.trail(tenant) {
		if row.Action == "entity.created" {
			creations[row.ResourceID] = row
		}
	}
	s.Require().Equal(app.CredentialAPIToken, creations[byPrimary].CredentialKind,
		"a change made with a bearer token is attributed to that token, not merely to the service account")
	s.Require().Equal(primaryID, creations[byPrimary].CredentialID)
	s.Require().Equal(rotationID, creations[byRotation].CredentialID)

	s.Require().NoError(audit.VerifyChain(s.chainOf(tenant)))
}

// Scheduled work authenticates with no credential at all, and must still be
// able to write to the trail: the credential columns are absent together, which
// is what the column pair's CHECK says.
func (s *CorrelationSuite) TestAnEntryMayHaveNoCredentialAtAll() {
	tenant := s.InsertTenant("corr-none", "Correlation System").String()

	var actor string
	s.Require().NoError(s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status) VALUES ($1, $2, 'System', 'active') RETURNING id`,
		tenant, "system-"+uuid.NewString()+"@test.local").Scan(&actor))

	exec := database.NewExec(s.DB)
	ctx := app.WithTenantID(context.Background(), tenant)
	ctx = app.WithRequester(ctx, &app.Requester{Kind: app.RequesterUser, ID: actor})
	rec := audit.NewRecorder(exec)
	s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
		return rec.Record(ctx, "audit.exported", "tenant", tenant, map[string]any{"entries": 3})
	}))

	rows := s.trail(tenant)
	s.Require().Len(rows, 1)
	s.Require().Empty(rows[0].CredentialID)
	s.Require().Empty(rows[0].CredentialKind)
	s.Require().Empty(rows[0].RequestID, "no request, no correlation id")
	s.Require().NoError(audit.VerifyChain(s.chainOf(tenant)))
}

// THE MAJOR, END TO END: a hostile X-Request-Id. What is stored is a minted
// UUID, and no fragment of what the caller sent survives anywhere on the row —
// which matters because the row is hashed into an append-only chain and copied
// into a bucket that keeps it for ten years with no delete verb.
func (s *CorrelationSuite) TestAHostileRequestIdIsNeverStored() {
	tenant := s.InsertTenant("corr-reqid", "Correlation Request Id").String()

	hostile := []string{
		`'; DROP TABLE audit_log; -- jane.doe@example.com`,
		`{"injected":"envelope"}`,
		strings.Repeat("A", 4096),
		"trace-abc",
		"0f8fad5b-d9cb-469f-a165-70867728950", // one short of a UUID
	}

	for i, sent := range hostile {
		req := s.As(tenant).WithRequestID(sent)
		var entity struct {
			ID string `json:"id"`
		}
		resp := req.POST(s.T(), "/entities", map[string]any{
			"name": "Probe " + uuid.NewString(), "country": "Germany",
			"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
		})
		resp.AssertStatus(s.T(), http.StatusCreated)
		resp.DecodeData(s.T(), &entity)

		// The reply names the id the server minted, never the caller's text.
		reflected := resp.Header.Get("X-Request-Id")
		s.Require().NotEqual(sent, reflected)
		s.Require().Regexp(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, reflected)

		rows := s.trail(tenant)
		s.Require().Len(rows, i+1)
		stored := rows[i]
		s.Require().Equal(reflected, stored.RequestID,
			"the stored correlation id is the one the server minted and returned")
		s.Require().NotContains(stored.Raw, sent,
			"no part of the caller's header may reach the ledger")
		s.Require().NotContains(strings.ToLower(stored.Raw), "drop table")
		s.Require().NotContains(strings.ToLower(stored.Raw), "jane.doe@example.com")
	}

	// A legitimate caller's own UUID is still honoured — tracing across the
	// tiers is why the header exists — and it joins the security stream, whose
	// request_id column is a UUID.
	own := uuid.NewString()
	s.As(tenant).WithRequestID(strings.ToUpper(own)).POST(s.T(), "/entities", map[string]any{
		"name": "Traced GmbH", "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	}).AssertStatus(s.T(), http.StatusCreated)

	rows := s.trail(tenant)
	s.Require().Equal(own, rows[len(rows)-1].RequestID,
		"a canonical UUID is kept (lowercased, so it joins a UUID column)")

	s.Require().NoError(audit.VerifyChain(s.chainOf(tenant)))
}

// The other half of "a future path cannot reintroduce it": the column itself
// refuses caller text, so a writer that forgets the middleware — a job, a
// consumer, a migration, a mistake — cannot put it there either.
func (s *CorrelationSuite) TestTheColumnRefusesCallerTextEvenFromTheApplicationRole() {
	tenant := s.InsertTenant("corr-schema", "Correlation Schema").String()

	var actor string
	s.Require().NoError(s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status) VALUES ($1, $2, 'System', 'active') RETURNING id`,
		tenant, "schema-"+uuid.NewString()+"@test.local").Scan(&actor))

	insert := func(requestID string) error {
		tx, err := s.DB.Beginx()
		s.Require().NoError(err)
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant); err != nil {
			return err
		}
		_, err = tx.Exec(`
			INSERT INTO audit_log (event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
			                       occurred_at, request_id, prev_hash, hash, hash_version)
			VALUES (gen_random_uuid(), $1, 1, $2, 'entity.created', 'entity', gen_random_uuid(),
			        NOW(), $3, repeat('0', 64), repeat('0', 64), 3)`,
			tenant, actor, requestID)
		return err
	}

	for _, bogus := range []string{
		"'; DROP TABLE audit_log; --",
		"jane.doe@example.com",
		"0F8FAD5B-D9CB-469F-A165-70867728950E", // a UUID, but not the canonical spelling
	} {
		err := insert(bogus)
		s.Require().Error(err, "the column must refuse %q", bogus)
		s.Require().Contains(err.Error(), "audit_log_request_id_shape")
	}

	s.Require().NoError(insert(uuid.NewString()), "a canonical id is accepted")
	s.Require().NoError(insert(""), "an entry with no request keeps the empty string")

	// Half a credential is refused the same way: a join key pointing at no
	// table is not evidence, it is a broken reference.
	halfCredential := func(id, kind any) error {
		tx, err := s.DB.Beginx()
		s.Require().NoError(err)
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant); err != nil {
			return err
		}
		_, err = tx.Exec(`
			INSERT INTO audit_log (event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
			                       occurred_at, request_id, credential_id, credential_kind, prev_hash, hash, hash_version)
			VALUES (gen_random_uuid(), $1, 2, $2, 'entity.created', 'entity', gen_random_uuid(),
			        NOW(), '', $3, $4, repeat('0', 64), repeat('0', 64), 3)`,
			tenant, actor, id, kind)
		return err
	}
	err := halfCredential(uuid.NewString(), nil)
	s.Require().Error(err, "a credential id without a kind names no store")
	s.Require().Contains(err.Error(), "audit_log_credential_pair")

	err = halfCredential(uuid.NewString(), "cookie")
	s.Require().Error(err, "the kind is a closed vocabulary, never free text")
	s.Require().Contains(err.Error(), "audit_log_credential_kind")
}
