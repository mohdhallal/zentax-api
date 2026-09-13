package securityevent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // the pgx driver these tests open sqlx with
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// These tests need a real Postgres, because every property they assert lives in
// the database and in none of them exists in a fake: an INSERT that works with
// NO TENANT BOUND, an append-only table, a CHECK that refuses anything but a hex
// digest, and RLS read policies. They run against the same scratch database as
// the acceptance suite, as the same NOSUPERUSER + NOBYPASSRLS role the API uses
// in production — which is what makes the isolation assertions mean anything:
//
//	TEST_DATABASE_URL=postgres://zentax_test_app:zentax-test-app@localhost:5433/zentax_test?sslmode=disable \
//	  go test ./platform/securityevent/...

type harness struct {
	db       *sqlx.DB
	exec     *database.Exec
	recorder *Recorder
	reader   *Reader
	tenant   string
	user     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set: skipping the Postgres security-event tests")
	}
	db, err := sqlx.Open("pgx", url)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx))

	exec := database.NewExec(db)
	digest, err := NewDigest(testKey())
	require.NoError(t, err)

	h := &harness{
		db:       db,
		exec:     exec,
		recorder: NewRecorder(exec).WithDigest(digest),
		reader:   NewReader(exec, exec),
	}
	h.tenant, h.user = h.seedTenant(t)
	t.Cleanup(func() {
		h.cleanup(t)
		_ = db.Close()
	})
	return h
}

func (h *harness) seedTenant(t *testing.T) (tenantID, userID string) {
	t.Helper()
	slug := "sec-" + uuid.NewString()[:8]
	require.NoError(t, h.db.QueryRowx(
		`INSERT INTO tenants (slug, name) VALUES ($1, 'Security Events') RETURNING id`, slug).Scan(&tenantID))
	require.NoError(t, h.db.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status) VALUES ($1, $2, 'Probe', 'active') RETURNING id`,
		tenantID, "sec-"+uuid.NewString()+"@test.local").Scan(&userID))
	return tenantID, userID
}

// cleanup removes this test's rows. security_events admits no DELETE at all —
// which is the point of it — so the rows are left where they are and only the
// tenant goes; there is no foreign key from the stream, deliberately, so the
// events survive the tenant exactly as they would after an off-boarding.
func (h *harness) cleanup(t *testing.T) {
	t.Helper()
	_, err := h.db.Exec(`DELETE FROM users WHERE tenant_id = $1`, h.tenant)
	assert.NoError(t, err)
	_, err = h.db.Exec(`DELETE FROM tenants WHERE id = $1`, h.tenant)
	assert.NoError(t, err)
}

// mine reads back this harness's own events through the cross-tenant door.
func (h *harness) mine(t *testing.T, ctx context.Context, f Filter) []Entry {
	t.Helper()
	entries, err := h.reader.List(ctx, f)
	require.NoError(t, err)
	return entries
}

// THE EVENT THE DOMAIN TRAIL CANNOT HOLD. A failed login for an address that
// resolves to no account has no tenant and no actor, so audit_log's tenant-keyed
// append policy and NOT NULL actor_id would both refuse it. Here it lands, with
// no tenant bound on the connection at all, and it is still correlatable —
// by the keyed digest of the address, which the row holds instead of the address.
func TestUnidentifiedFailureIsRecordedWithNeitherTenantNorActor(t *testing.T) {
	h := newHarness(t)
	ctx := WithClientAddr(context.Background(), "203.0.113.7:51000")

	// Unique per run: the table admits no DELETE, so a fixed address would
	// accumulate rows across runs and the count below would drift.
	stranger := "nobody-" + uuid.NewString() + "@example.invalid"
	require.NoError(t, h.recorder.Record(ctx, Event{
		Event:   EventLoginFailed,
		Outcome: OutcomeFailure,
		Method:  MethodPassword,
		Reason:  ReasonNoSuchPrincipal,
		Subject: stranger,
	}))

	digest, err := NewDigest(testKey())
	require.NoError(t, err)
	entries := h.mine(t, context.Background(), Filter{SubjectDigest: digest.Of(stranger)})
	require.Len(t, entries, 1)

	got := entries[0]
	assert.Equal(t, EventLoginFailed, got.Event)
	assert.Equal(t, ReasonNoSuchPrincipal, got.Reason.String)
	assert.False(t, got.TenantID.Valid, "an unknown address belongs to no tenant")
	assert.False(t, got.PrincipalID.Valid, "an unknown address names no principal")
	assert.Equal(t, "203.0.113.7", got.ClientIP.String, "the stream has to be able to say from where")
	assert.Equal(t, AddrFromPeer, got.ClientIPSource.String,
		"WithClientAddr binds a socket peer; the row has to say that is what it is")

	// And the address itself is nowhere on the row — asserted against the WHOLE
	// row rendered as text, not against the columns this package happens to
	// select, so a future column cannot quietly become somewhere to put it.
	raw := strings.ToLower(h.rawRow(t, got.EventID))
	assert.NotContains(t, raw, strings.ToLower(stranger),
		"the submitted address must never be stored in the clear")
	assert.NotContains(t, raw, "example.invalid", "not even the domain half of it")
}

// rawRow renders one whole stored row as text, through the stream door.
func (h *harness) rawRow(t *testing.T, eventID string) string {
	t.Helper()
	tx, err := h.db.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.security_stream', 'on', true)`)
	require.NoError(t, err)

	var raw string
	require.NoError(t, tx.QueryRowx(
		`SELECT e::text FROM security_events e WHERE event_id = $1`, eventID).Scan(&raw))
	return raw
}

// PROVENANCE REACHES THE COLUMN, and the column's vocabulary is closed by a
// CHECK rather than by this package's good intentions. The three values are
// worth different amounts to a reader — an unforgeable socket peer, an address a
// trusted proxy wrote, and our own hop standing in for a client it never named —
// and a row that could not tell them apart would make the load balancer look
// like the caller in every deployment that has one.
func TestTheAddressCarriesItsProvenance(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	digest, err := NewDigest(testKey())
	require.NoError(t, err)

	// Read back by the per-run subject digest, never by the address: the table
	// admits no DELETE, so a fixed address would accumulate rows across runs and
	// the count below would drift.
	record := func(addr, source string) Entry {
		t.Helper()
		subject := "provenance-" + uuid.NewString() + "@example.invalid"
		require.NoError(t, h.recorder.Record(WithClient(ctx, addr, source), Event{
			Event: EventLoginFailed, Outcome: OutcomeFailure, Method: MethodPassword,
			Reason: ReasonNoSuchPrincipal, Subject: subject,
		}))
		entries := h.mine(t, ctx, Filter{SubjectDigest: digest.Of(subject)})
		require.Len(t, entries, 1)
		return entries[0]
	}

	for _, tc := range []struct{ source, addr string }{
		{AddrFromPeer, "203.0.113.11"},
		{AddrFromForwarded, "203.0.113.12"},
		{AddrFromProxy, "10.0.0.13"},
	} {
		got := record(tc.addr, tc.source)
		assert.Equal(t, tc.addr, got.ClientIP.String)
		assert.Equal(t, tc.source, got.ClientIPSource.String)
	}

	// A source this build does not know is dropped rather than written: the
	// column is a closed vocabulary, and the insert must not fail a refusal that
	// has already happened.
	got := record("203.0.113.14", "somebody-invented-this")
	assert.Equal(t, "203.0.113.14", got.ClientIP.String, "the address still lands")
	assert.False(t, got.ClientIPSource.Valid)
}

// The invitation credential is a method of its own in the database as well as in
// Go: the CHECK on the column is what makes the vocabulary closed, and this is
// the test that says the new value is actually in it.
func TestAnInviteRedemptionIsStorable(t *testing.T) {
	h := newHarness(t)
	ctx := WithClient(context.Background(), "203.0.113.15", AddrFromForwarded)

	require.NoError(t, h.recorder.Record(ctx, Event{
		Event:       EventInviteAccepted,
		Outcome:     OutcomeSuccess,
		Method:      MethodInviteToken,
		TenantID:    h.tenant,
		PrincipalID: h.user,
	}))

	// By principal, not by event name: this harness's user is unique to the run,
	// and the stream is a table nothing can delete from.
	entries := h.mine(t, context.Background(), Filter{PrincipalID: h.user})
	require.Len(t, entries, 1)
	assert.Equal(t, EventInviteAccepted, entries[0].Event)
	assert.Equal(t, MethodInviteToken, entries[0].Method)
	assert.Equal(t, h.user, entries[0].PrincipalID.String)
	assert.Equal(t, "203.0.113.15", entries[0].ClientIP.String)
	assert.Equal(t, AddrFromForwarded, entries[0].ClientIPSource.String)
}

// The digest is the correlator: several attempts against one address count as
// one address, and an attempt against a different one does not join them.
func TestAttemptsAgainstOneAddressAreCountableWithoutStoringIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	digest, err := NewDigest(testKey())
	require.NoError(t, err)

	target := "victim-" + uuid.NewString() + "@example.invalid"
	other := "someone-else-" + uuid.NewString() + "@example.invalid"
	for range 3 {
		require.NoError(t, h.recorder.Record(ctx, Event{
			Event: EventLoginFailed, Outcome: OutcomeFailure, Method: MethodPassword,
			Reason: ReasonNoSuchPrincipal, Subject: target,
		}))
	}
	require.NoError(t, h.recorder.Record(ctx, Event{
		Event: EventLoginFailed, Outcome: OutcomeFailure, Method: MethodPassword,
		Reason: ReasonNoSuchPrincipal, Subject: other,
	}))

	assert.Len(t, h.mine(t, ctx, Filter{SubjectDigest: digest.Of(target)}), 3)
	assert.Len(t, h.mine(t, ctx, Filter{SubjectDigest: digest.Of(other)}), 1)
	// Spelling is not a second subject: the same address in another case must
	// count against the same total, or the number an investigator reads is low.
	assert.Len(t, h.mine(t, ctx, Filter{SubjectDigest: digest.Of(strings.ToUpper(target))}), 3)
}

// An identified attempt correlates by account and carries NO digest: an account
// named by id needs no second pseudonym for the same person, and one fewer
// derived identifier in an append-only store is one fewer thing to explain.
func TestIdentifiedAttemptCorrelatesByAccountAndNotByAddress(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	require.NoError(t, h.recorder.Record(ctx, Event{
		Event: EventLoginFailed, Outcome: OutcomeFailure, Method: MethodPassword,
		Reason: ReasonBadCredential, TenantID: h.tenant, PrincipalID: h.user,
		Subject: "the-address-of-a-known-account@test.local",
	}))

	entries := h.mine(t, ctx, Filter{PrincipalID: h.user})
	require.Len(t, entries, 1)
	assert.Equal(t, h.tenant, entries[0].TenantID.String)
	assert.False(t, entries[0].SubjectDigest.Valid,
		"a principal named by id must not also be pseudonymised by address")
}

// APPEND-ONLY, twice over. The RLS policy set has no UPDATE or DELETE policy, so
// a statement affects zero rows; the trigger refuses outright, which is what
// still holds for a role that bypasses RLS and — the reason it exists at all —
// what survives migrate.sh re-granting UPDATE and DELETE on every run.
func TestTheStreamCannotBeRewrittenOrErased(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	require.NoError(t, h.recorder.Record(ctx, Event{
		Event: EventLoginSucceeded, Outcome: OutcomeSuccess, Method: MethodPassword,
		TenantID: h.tenant, PrincipalID: h.user,
	}))
	entries := h.mine(t, ctx, Filter{PrincipalID: h.user})
	require.Len(t, entries, 1)
	id := entries[0].EventID

	res, err := h.db.Exec(`UPDATE security_events SET outcome = 'failure' WHERE event_id = $1`, id)
	if err == nil {
		affected, _ := res.RowsAffected()
		assert.Zero(t, affected, "RLS must leave an UPDATE matching nothing")
	} else {
		assert.Contains(t, err.Error(), "append-only")
	}

	res, err = h.db.Exec(`DELETE FROM security_events WHERE event_id = $1`, id)
	if err == nil {
		affected, _ := res.RowsAffected()
		assert.Zero(t, affected, "RLS must leave a DELETE matching nothing")
	} else {
		assert.Contains(t, err.Error(), "append-only")
	}

	after := h.mine(t, ctx, Filter{PrincipalID: h.user})
	require.Len(t, after, 1)
	assert.Equal(t, OutcomeSuccess, after[0].Outcome, "the row must be exactly as it was written")
}

// A tenant sees its own identified events and NOTHING ELSE — in particular not
// the tenant-less rows, which belong to no customer and would tell one customer
// about probes aimed at the product.
func TestATenantSeesItsOwnEventsAndNoneOfTheTenantLessOnes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	require.NoError(t, h.recorder.Record(ctx, Event{
		Event: EventLoginSucceeded, Outcome: OutcomeSuccess, Method: MethodPassword,
		TenantID: h.tenant, PrincipalID: h.user,
	}))
	require.NoError(t, h.recorder.Record(ctx, Event{
		Event: EventLoginFailed, Outcome: OutcomeFailure, Method: MethodPassword,
		Reason: ReasonNoSuchPrincipal, Subject: "stranger-" + uuid.NewString() + "@example.invalid",
	}))

	// As a tenant would see it: the ordinary tenant GUC, no stream door.
	tx, err := h.db.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, h.tenant)
	require.NoError(t, err)

	var visible []struct {
		Event    string `db:"event"`
		TenantID string `db:"tenant_id"`
	}
	require.NoError(t, tx.Select(&visible, `SELECT event, tenant_id FROM security_events`))
	require.Len(t, visible, 1)
	assert.Equal(t, EventLoginSucceeded, visible[0].Event)
	assert.Equal(t, h.tenant, visible[0].TenantID)
}

// Without either door — no tenant GUC and no stream GUC — the table shows
// nothing at all. That is what makes the stream door an explicit act rather
// than the absence of a policy.
func TestWithoutADoorTheStreamIsInvisible(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	require.NoError(t, h.recorder.Record(ctx, Event{
		Event: EventLoginSucceeded, Outcome: OutcomeSuccess, Method: MethodPassword,
		TenantID: h.tenant, PrincipalID: h.user,
	}))

	var count int
	require.NoError(t, h.db.QueryRowx(`SELECT count(*) FROM security_events`).Scan(&count))
	assert.Zero(t, count, "a connection that has asked for nothing must see nothing")
}

// The correlation id is caller-supplied when X-Request-Id is sent, and the
// column is a UUID so that it cannot become the door through which caller text
// reaches an append-only store. A header somebody made up must cost the
// correlation, not the event.
func TestAFabricatedRequestIdCostsTheCorrelationAndNotTheEvent(t *testing.T) {
	h := newHarness(t)
	ctx := app.WithRequestId(context.Background(), "'; DROP TABLE security_events; -- jane@example.com")

	require.NoError(t, h.recorder.Record(ctx, Event{
		Event: EventLoginSucceeded, Outcome: OutcomeSuccess, Method: MethodPassword,
		TenantID: h.tenant, PrincipalID: h.user,
	}))

	entries := h.mine(t, context.Background(), Filter{PrincipalID: h.user})
	require.Len(t, entries, 1)
	assert.False(t, entries[0].RequestID.Valid)
}

// The hex CHECK is the schema-level half of "the address is never stored in the
// clear": nothing that is not 64 hex characters can occupy that column, so a
// future writer cannot put an address there by mistake.
func TestTheDigestColumnRefusesAnythingButAHexDigest(t *testing.T) {
	h := newHarness(t)

	_, err := h.db.Exec(
		`INSERT INTO security_events (event, outcome, method, reason, subject_digest)
		 VALUES ('auth.login.failed', 'failure', 'password', 'no_such_principal', $1)`,
		"jane.doe@example.com                                            ")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "check")
}

// One correlator, never two: a row may name a principal or digest an address,
// not both.
func TestARowMayNotCarryBothCorrelators(t *testing.T) {
	h := newHarness(t)
	digest, err := NewDigest(testKey())
	require.NoError(t, err)

	_, err = h.db.Exec(
		`INSERT INTO security_events (event, outcome, method, reason, principal_id, subject_digest)
		 VALUES ('auth.login.failed', 'failure', 'password', 'bad_credential', $1, $2)`,
		h.user, digest.Of("admin@acme.com"))
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "one_correlator")
}
