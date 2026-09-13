package outbox

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // the pgx driver these tests open sqlx with
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// These tests need a real Postgres: row-level security, SKIP LOCKED and the
// state guard are the subjects, and none of them exists in a fake. They run
// against the same scratch database as the acceptance suite, as the same
// NOSUPERUSER + NOBYPASSRLS role the API uses in production — which is what
// makes the cross-tenant assertions mean anything:
//
//	TEST_DATABASE_URL=postgres://zentax_test_app:zentax-test-app@localhost:5433/zentax_test?sslmode=disable \
//	  go test ./platform/outbox/...

type harness struct {
	db     *sqlx.DB
	exec   *database.Exec
	store  *Store
	queue  *Queue
	tenant string
	user   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set: skipping the Postgres outbox tests")
	}
	db, err := sqlx.Open("pgx", url)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx))

	exec := database.NewExec(db)
	h := &harness{db: db, exec: exec, store: NewStore(exec, exec), queue: NewQueue(exec)}
	h.tenant, h.user = h.seedTenant(t)
	t.Cleanup(func() {
		h.cleanup(t, h.tenant)
		_ = db.Close()
	})
	return h
}

// seedTenant provisions a tenant and one user, the way the control plane would.
//
// Both rows are written by ONE statement on purpose: this is the same scratch
// database the acceptance suite truncates between its tests, and a run of that
// suite landing between two separate inserts would take the tenant away before
// the user could reference it.
func (h *harness) seedTenant(t *testing.T) (tenantID, userID string) {
	t.Helper()

	slug := "outbox-" + uuid.NewString()[:12]
	var row struct {
		TenantID string `db:"tenant_id"`
		UserID   string `db:"id"`
	}
	require.NoError(t, h.db.Get(&row, `
		WITH seeded_tenant AS (
		    INSERT INTO tenants (slug, name) VALUES ($1, 'Outbox Test') RETURNING id
		)
		INSERT INTO users (tenant_id, email, name, status)
		SELECT id, $2, 'Outbox Tester', 'active' FROM seeded_tenant
		RETURNING tenant_id, id`,
		slug, slug+"@test.local"))
	return row.TenantID, row.UserID
}

// cleanup removes what the test made. Audit entries cannot be deleted by the
// app role (the trail is append-only by policy), so a tenant that recorded one
// is left behind for the next TRUNCATE-based teardown rather than forced away.
func (h *harness) cleanup(t *testing.T, tenantID string) {
	t.Helper()

	_ = h.asTenant(tenantID, h.user, func(ctx context.Context) error {
		_, err := h.exec.ExecContext(ctx, `DELETE FROM outbox_messages`)
		return err
	})
	_, _ = h.db.Exec(`DELETE FROM users WHERE tenant_id = $1`, tenantID)
	_, _ = h.db.Exec(`DELETE FROM tenants WHERE id = $1`, tenantID)
}

// asTenant runs fn on a transaction bound to a tenant and an acting user —
// exactly what the HTTP Tx seam does for a request.
func (h *harness) asTenant(tenantID, userID string, fn func(ctx context.Context) error) error {
	ctx := app.WithTenantID(context.Background(), tenantID)
	ctx = app.WithRequester(ctx, &app.Requester{Kind: app.RequesterUser, ID: userID})
	return h.exec.WithinTransaction(ctx, fn)
}

// enqueue queues one message as the harness's tenant and returns its receipt.
func (h *harness) enqueue(t *testing.T, m NewMessage) Receipt {
	t.Helper()
	var r Receipt
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		var err error
		r, err = h.queue.Enqueue(ctx, m)
		return err
	}))
	return r
}

func reminder(recipient string) NewMessage {
	return NewMessage{
		Template:  "deadline.reminder",
		Recipient: recipient,
		Payload:   map[string]any{"taskId": uuid.NewString(), "dueDate": "2026-10-15"},
	}
}

// find returns the claimed message with the given id, or fails: a claim is a
// global sweep, so a test asserts on ITS messages rather than on the batch.
func find(t *testing.T, msgs []Message, id string) Message {
	t.Helper()
	for _, m := range msgs {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("message %s was not in the claimed batch of %d", id, len(msgs))
	return Message{}
}

func (h *harness) statusOf(t *testing.T, id string) (status string, attempts int, lastError *string) {
	t.Helper()
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.QueryRowxContext(ctx,
			`SELECT status, attempts, last_error FROM outbox_messages WHERE id = $1`, id).
			Scan(&status, &attempts, &lastError)
	}))
	return status, attempts, lastError
}

// --- the transactional half ------------------------------------------------

// The property the whole pattern exists for: the message and the change that
// owes it commit together, or neither does.
func TestEnqueueRollsBackWithTheChangeThatOwedIt(t *testing.T) {
	h := newHarness(t)

	boom := assert.AnError
	err := h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		if _, err := h.queue.Enqueue(ctx, reminder("cfo@example.test")); err != nil {
			return err
		}
		return boom // the mutation this notification belonged to failed.
	})
	require.ErrorIs(t, err, boom)

	var queued int
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &queued, `SELECT count(*) FROM outbox_messages`)
	}))
	assert.Zero(t, queued, "a rolled-back change must not leave a message to send")
}

func TestEnqueueStampsTenantActorAndSchedule(t *testing.T) {
	h := newHarness(t)

	due := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	r := h.enqueue(t, NewMessage{
		Template:  "deadline.reminder",
		Recipient: "cfo@example.test",
		Payload:   map[string]any{"taskId": "t-1"},
		DueAt:     due,
	})
	require.NotEmpty(t, r.ID)
	assert.False(t, r.Deduplicated)

	var row struct {
		TenantID      string    `db:"tenant_id"`
		CreatedBy     string    `db:"created_by"`
		Channel       string    `db:"channel"`
		Status        string    `db:"status"`
		Attempts      int       `db:"attempts"`
		DueAt         time.Time `db:"due_at"`
		NextAttemptAt time.Time `db:"next_attempt_at"`
		Payload       []byte    `db:"payload"`
	}
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &row,
			`SELECT tenant_id, created_by, channel, status, attempts, due_at, next_attempt_at, payload
			 FROM outbox_messages WHERE id = $1`, r.ID)
	}))

	assert.Equal(t, h.tenant, row.TenantID, "the tenant comes from the transaction, never from the caller")
	assert.Equal(t, h.user, row.CreatedBy, "actor-by-ID: whose change owed the message")
	assert.Equal(t, ChannelEmail, row.Channel)
	assert.Equal(t, string(StatusPending), row.Status)
	assert.Zero(t, row.Attempts)
	assert.WithinDuration(t, due, row.DueAt, time.Millisecond)
	assert.Equal(t, row.DueAt, row.NextAttemptAt, "a message is claimable from the instant it is due")
	assert.JSONEq(t, `{"taskId":"t-1"}`, string(row.Payload))
}

// The producer half of at-least-once: re-running a producer enqueues nothing.
func TestEnqueueIsIdempotentForADedupeKey(t *testing.T) {
	h := newHarness(t)

	key := "reminder:" + uuid.NewString()
	first := h.enqueue(t, NewMessage{Template: "deadline.reminder", Recipient: "cfo@example.test", DedupeKey: key})
	second := h.enqueue(t, NewMessage{Template: "deadline.reminder", Recipient: "cfo@example.test", DedupeKey: key})

	assert.Equal(t, first.ID, second.ID, "the re-run must name the message that already exists")
	assert.False(t, first.Deduplicated)
	assert.True(t, second.Deduplicated)

	var queued int
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &queued, `SELECT count(*) FROM outbox_messages WHERE dedupe_key = $1`, key)
	}))
	assert.Equal(t, 1, queued)
}

func TestEnqueueRejectsAnUnsendableMessage(t *testing.T) {
	h := newHarness(t)

	err := h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		_, err := h.queue.Enqueue(ctx, NewMessage{Template: "deadline.reminder"})
		return err
	})
	require.ErrorContains(t, err, "recipient")

	err = h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		_, err := h.queue.Enqueue(ctx, NewMessage{Recipient: "cfo@example.test"})
		return err
	})
	require.ErrorContains(t, err, "template")
}

// --- tenancy ---------------------------------------------------------------

// A tenant sees its own queue and nothing else — the ordinary ADR-0004 rule,
// enforced by RLS rather than by a WHERE clause anyone could forget.
func TestATenantCannotSeeAnotherTenantsMessages(t *testing.T) {
	h := newHarness(t)
	otherTenant, otherUser := h.seedTenant(t)
	t.Cleanup(func() { h.cleanup(t, otherTenant) })

	mine := h.enqueue(t, reminder("cfo@example.test"))

	var visible int
	require.NoError(t, h.asTenant(otherTenant, otherUser, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &visible, `SELECT count(*) FROM outbox_messages WHERE id = $1`, mine.ID)
	}))
	assert.Zero(t, visible)

	// The same dedupe key in another tenant is a different message: the
	// idempotency key is scoped, not global.
	key := "shared:" + uuid.NewString()
	a := h.enqueue(t, NewMessage{Template: "t", Recipient: "a@example.test", DedupeKey: key})
	var b Receipt
	require.NoError(t, h.asTenant(otherTenant, otherUser, func(ctx context.Context) error {
		var err error
		b, err = h.queue.Enqueue(ctx, NewMessage{Template: "t", Recipient: "b@example.test", DedupeKey: key})
		return err
	}))
	assert.NotEqual(t, a.ID, b.ID)
	assert.False(t, b.Deduplicated)
}

// The delivery loop's cross-tenant door: it reads what no tenant transaction
// can, through the explicit app.outbox_runner scope and nothing else.
func TestTheRunnerClaimsAcrossTenants(t *testing.T) {
	h := newHarness(t)
	otherTenant, otherUser := h.seedTenant(t)
	t.Cleanup(func() { h.cleanup(t, otherTenant) })

	mine := h.enqueue(t, reminder("cfo@example.test"))
	var theirs Receipt
	require.NoError(t, h.asTenant(otherTenant, otherUser, func(ctx context.Context) error {
		var err error
		theirs, err = h.queue.Enqueue(ctx, reminder("controller@other.test"))
		return err
	}))

	claimed, err := h.store.ClaimDue(context.Background(), 100, time.Minute)
	require.NoError(t, err)

	a := find(t, claimed, mine.ID)
	b := find(t, claimed, theirs.ID)
	assert.Equal(t, h.tenant, a.TenantID)
	assert.Equal(t, otherTenant, b.TenantID)
	assert.Equal(t, "cfo@example.test", a.Recipient, "the runner gets what it needs to send")
}

// The door is one-way: the runner may read and settle, never author. A job that
// could write a message could address one at any tenant.
func TestTheRunnerCannotAuthorAMessage(t *testing.T) {
	h := newHarness(t)

	err := h.store.withinRunnerTx(context.Background(), func(ctx context.Context) error {
		_, err := h.exec.ExecContext(ctx,
			`INSERT INTO outbox_messages (tenant_id, template, recipient) VALUES ($1, 'forged', 'attacker@example.test')`,
			h.tenant)
		return err
	})
	require.Error(t, err)
	pgErr := database.IsPgError(err)
	require.NotNil(t, pgErr)
	assert.Equal(t, "42501", pgErr.Code, "row-level security must refuse the insert")
}

// --- claiming --------------------------------------------------------------

func TestClaimLeasesTheMessageAndChargesTheAttempt(t *testing.T) {
	h := newHarness(t)
	r := h.enqueue(t, reminder("cfo@example.test"))
	ctx := context.Background()

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, find(t, claimed, r.ID).Attempts, "the claim charges the attempt it is about to make")

	// A leased message is invisible to the next pass: two runners overlapping
	// (or one runner still working) cannot send it twice.
	again, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	for _, m := range again {
		assert.NotEqual(t, r.ID, m.ID, "a leased message must not be claimed again")
	}

	status, attempts, _ := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusPending), status, "a claim is a lease, not a settlement")
	assert.Equal(t, 1, attempts)
}

// Two runners racing on the same rows must never be handed the same message —
// the property that makes the claim safe even if leadership were ever wrong.
func TestClaimStepsOverRowsAnotherRunnerHolds(t *testing.T) {
	h := newHarness(t)
	held := h.enqueue(t, reminder("held@example.test"))
	free := h.enqueue(t, reminder("free@example.test"))

	// Stand in for the other runner: a transaction holding one row's lock.
	other, err := h.db.Beginx()
	require.NoError(t, err)
	defer func() { _ = other.Rollback() }()
	_, err = other.Exec(`SELECT set_config('app.outbox_runner', 'on', true)`)
	require.NoError(t, err)
	var lockedID string
	require.NoError(t, other.Get(&lockedID,
		`SELECT id FROM outbox_messages WHERE id = $1 FOR UPDATE`, held.ID))

	claimed, err := h.store.ClaimDue(context.Background(), 100, time.Minute)
	require.NoError(t, err)

	find(t, claimed, free.ID) // the free one is ours
	for _, m := range claimed {
		assert.NotEqual(t, held.ID, m.ID, "SKIP LOCKED must step over the other runner's row")
	}
}

// --- handing a claim back ---------------------------------------------------

// The other half of the claim. A pass claims a batch and charges every row an
// attempt up front; the rows it never reaches — because one send went quiet and
// spent the budget — must go back exactly as they were, or a slow provider
// exhausts the retry budget of every message queued behind it without one of
// them ever reaching a transport.
func TestReleaseRefundsTheClaimAndRequeuesAtOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	tried := h.enqueue(t, reminder("tried@example.test"))
	unreached := h.enqueue(t, reminder("unreached@example.test"))

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, find(t, claimed, unreached.ID).Attempts, "the claim charged it before the pass ran out")

	require.NoError(t, h.store.ReleaseClaim(ctx, []Message{find(t, claimed, unreached.ID)}))

	status, attempts, lastErr := h.statusOf(t, unreached.ID)
	assert.Equal(t, string(StatusPending), status)
	assert.Zero(t, attempts, "an attempt the transport never saw is not an attempt")
	assert.Nil(t, lastErr, "nothing failed, so nothing is reported against it")

	// And the next runner takes it AT ONCE rather than waiting the lease out —
	// which is what makes a shutdown mid-batch cost seconds, not minutes.
	again, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, find(t, again, unreached.ID).Attempts, "it is attempt one all over again")
	for _, m := range again {
		assert.NotEqual(t, tried.ID, m.ID, "the message that WAS tried keeps the lease it is being sent under")
	}
}

// A hand-back is safe to write late: it matches only rows still carrying the
// attempt count this runner's claim gave them.
func TestReleaseLeavesAMessageAnotherRunnerHasSinceTaken(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := h.enqueue(t, reminder("cfo@example.test"))

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	stale := find(t, claimed, r.ID)

	// Time passes: the message is rescheduled and a second runner claims it.
	require.NoError(t, h.store.Reschedule(ctx, stale, time.Now().UTC().Add(-time.Second),
		Reason{Class: ReasonTimeout, Detail: "delivery timed out after 10s"}))
	again, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2, find(t, again, r.ID).Attempts)

	require.NoError(t, h.store.ReleaseClaim(ctx, []Message{stale}))

	_, attempts, _ := h.statusOf(t, r.ID)
	assert.Equal(t, 2, attempts, "a late hand-back must not refund an attempt that is now somebody else's")

	var stillLeased bool
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &stillLeased,
			`SELECT next_attempt_at > NOW() FROM outbox_messages WHERE id = $1`, r.ID)
	}))
	assert.True(t, stillLeased, "nor drop the lease the other runner is working under")
}

// The whole chain against the real queue: a runner goes down while one send is
// still on the wire, and the next runner finds the entire batch exactly as it
// was — pending, unspent, claimable at once. Nothing waits out a lease, and
// nothing has been charged for an attempt no transport ever saw.
func TestARunnerThatGoesDownMidBatchStrandsNothing(t *testing.T) {
	h := newHarness(t)
	first := h.enqueue(t, reminder("one@example.test"))
	second := h.enqueue(t, reminder("two@example.test"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel) // the process starts going down mid-send.

	d := NewDispatcher(h.store, &hangingSender{}, nil, Settings{
		BatchSize: 100, ClaimLease: time.Minute, SendTimeout: 5 * time.Second, BatchBudget: 30 * time.Second,
	})
	require.NoError(t, d.DeliverDue(ctx))

	for _, id := range []string{first.ID, second.ID} {
		status, attempts, lastErr := h.statusOf(t, id)
		assert.Equal(t, string(StatusPending), status, "an interrupted pass settles nothing")
		assert.Zero(t, attempts, "and charges nothing")
		assert.Nil(t, lastErr)
	}

	next, err := h.store.ClaimDue(context.Background(), 100, time.Minute)
	require.NoError(t, err)
	find(t, next, first.ID)
	find(t, next, second.ID)
}

// The state guard refuses ANY update to a settled row, so a hand-back that
// raced a settle would raise ZT027 and fail the whole batch's statement. It
// matches pending rows only, and is a no-op instead.
func TestReleaseCannotDisturbASettledMessage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := h.enqueue(t, reminder("cfo@example.test"))

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	msg := find(t, claimed, r.ID)
	require.NoError(t, h.store.MarkSent(ctx, msg))

	require.NoError(t, h.store.ReleaseClaim(ctx, []Message{msg}))

	status, attempts, _ := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusSent), status)
	assert.Equal(t, 1, attempts)
}

// --- settling --------------------------------------------------------------

func TestMarkSentSettlesTheMessage(t *testing.T) {
	h := newHarness(t)
	r := h.enqueue(t, reminder("cfo@example.test"))
	ctx := context.Background()

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.NoError(t, h.store.MarkSent(ctx, find(t, claimed, r.ID)))

	status, _, lastErr := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusSent), status)
	assert.Nil(t, lastErr)
}

func TestRescheduleKeepsTheMessageOwed(t *testing.T) {
	h := newHarness(t)
	r := h.enqueue(t, reminder("cfo@example.test"))
	ctx := context.Background()

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	msg := find(t, claimed, r.ID)

	// Back onto the queue, sooner than the lease would have allowed.
	retryAt := time.Now().UTC().Add(-time.Second)
	require.NoError(t, h.store.Reschedule(ctx, msg, retryAt,
		Reason{Class: ReasonTransient, Detail: "smtp: 421 service unavailable"}))

	status, attempts, lastErr := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusPending), status, "a transient failure leaves the message owed")
	assert.Equal(t, 1, attempts)
	require.NotNil(t, lastErr)
	assert.Contains(t, *lastErr, "421")

	// And it is claimable again at the instant it was rescheduled for.
	again, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 2, find(t, again, r.ID).Attempts)
}

// Giving up is the one delivery outcome that is a fact about the tenant, so it
// is the one that reaches the audit trail — on the same transaction as the
// state change, and carrying no personal data.
func TestMarkDeadRecordsTheTenantsEvidence(t *testing.T) {
	h := newHarness(t)
	h.store.WithAudit(audit.NewRecorder(h.exec))

	r := h.enqueue(t, reminder("cfo@example.test"))
	ctx := context.Background()
	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)

	require.NoError(t, h.store.MarkDead(ctx, find(t, claimed, r.ID),
		Reason{Class: ReasonPermanent, Detail: "550 5.1.1 <cfo@example.test>: no such user"}))

	status, _, lastErr := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusDead), status)
	require.NotNil(t, lastErr)
	assert.Contains(t, *lastErr, "550", "the operator's detail is kept on the row")

	var entry struct {
		Action       string          `db:"action"`
		ResourceType string          `db:"resource_type"`
		ResourceID   string          `db:"resource_id"`
		ActorID      string          `db:"actor_id"`
		Details      json.RawMessage `db:"details"`
	}
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &entry,
			`SELECT action, resource_type, resource_id, actor_id, details FROM audit_log WHERE tenant_id = $1`, h.tenant)
	}))

	assert.Equal(t, AuditAction, entry.Action)
	assert.Equal(t, AuditResource, entry.ResourceType)
	assert.Equal(t, r.ID, entry.ResourceID)
	assert.Equal(t, h.user, entry.ActorID, "attributed to whoever's change owed the message")
	assert.JSONEq(t,
		`{"attempts":1,"channel":"email","reason":"permanent_failure","template":"deadline.reminder"}`,
		string(entry.Details))
	assert.NotContains(t, string(entry.Details), "cfo@example.test",
		"the trail must never carry the recipient or the provider's echo of it")
}

// --- immutability ----------------------------------------------------------

func TestASettledMessageIsFinal(t *testing.T) {
	h := newHarness(t)
	r := h.enqueue(t, reminder("cfo@example.test"))
	ctx := context.Background()

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.NoError(t, h.store.MarkSent(ctx, find(t, claimed, r.ID)))

	err = h.store.withinRunnerTx(ctx, func(ctx context.Context) error {
		_, err := h.exec.ExecContext(ctx,
			`UPDATE outbox_messages SET status = 'pending', sent_at = NULL WHERE id = $1`, r.ID)
		return err
	})
	require.Error(t, err)
	pgErr := database.IsPgError(err)
	require.NotNil(t, pgErr)
	assert.Equal(t, "ZT027", pgErr.Code, "a message that was sent cannot be re-opened")
}

func TestAddressingAndContentAreFrozenAtEnqueue(t *testing.T) {
	h := newHarness(t)
	r := h.enqueue(t, reminder("cfo@example.test"))

	err := h.store.withinRunnerTx(context.Background(), func(ctx context.Context) error {
		_, err := h.exec.ExecContext(ctx,
			`UPDATE outbox_messages SET recipient = 'attacker@example.test' WHERE id = $1`, r.ID)
		return err
	})
	require.Error(t, err)
	pgErr := database.IsPgError(err)
	require.NotNil(t, pgErr)
	assert.Equal(t, "ZT027", pgErr.Code, "the runner may move delivery state and nothing else")
}

// --- pruning ---------------------------------------------------------------

func TestPruneClearsSettledMessagesAndNeverPendingOnes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	settled := h.enqueue(t, reminder("sent@example.test"))
	stillOwed := h.enqueue(t, reminder("owed@example.test"))

	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.NoError(t, h.store.MarkSent(ctx, find(t, claimed, settled.ID)))

	// The settled row cannot be aged by hand — it is final, as another test
	// proves — so the retention window is shortened instead of the row
	// backdated. A millisecond-old settlement is "past the window" here in the
	// same way a month-old one is in production.
	time.Sleep(5 * time.Millisecond)
	removed, err := h.store.Prune(ctx, time.Millisecond)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, removed, int64(1))

	var remaining int
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &remaining,
			`SELECT count(*) FROM outbox_messages WHERE id = ANY($1)`,
			[]string{settled.ID, stillOwed.ID})
	}))
	assert.Equal(t, 1, remaining, "the delivered message is gone; the owed one is untouched")

	status, _, _ := h.statusOf(t, stillOwed.ID)
	assert.Equal(t, string(StatusPending), status)
}
