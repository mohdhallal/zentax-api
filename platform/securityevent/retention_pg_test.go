package securityevent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the pgx driver these tests open sqlx with
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Retention is a property of the DATABASE, so it is proved against a database.
// Nothing here is assertable in a fake: that a partition past the window is
// gone, that one inside it is untouched, that the DEFAULT backstop survives a
// run that dropped its neighbour, and that the floor on the window is enforced
// by the schema and not only by the Go that usually calls it.
//
// They run against the same scratch database as the acceptance suite, AS THE
// SAME NOSUPERUSER + NOBYPASSRLS ROLE THE API USES — which is the whole point
// here. That role does not own security_events and Postgres has no DROP
// privilege to grant it, so a test that ran as the owner would prove the drop
// works for somebody who was never going to be the one doing it:
//
//	TEST_DATABASE_URL=postgres://zentax_test_app:zentax-test-app@localhost:5433/zentax_test?sslmode=disable \
//	  go test ./platform/securityevent/...

type retentionHarness struct {
	db     *sqlx.DB
	exec   *database.Exec
	reader *Reader
}

func newRetentionHarness(t *testing.T) *retentionHarness {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set: skipping the Postgres retention tests")
	}
	db, err := sqlx.Open("pgx", url)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx))
	t.Cleanup(func() { _ = db.Close() })

	exec := database.NewExec(db)
	return &retentionHarness{db: db, exec: exec, reader: NewReader(exec, exec)}
}

// monthStart is the first instant of the UTC month n months from the current
// one, which is how the maintenance function reasons about the window: it
// derives its cutoff from date_trunc('month', now()), never from a caller.
func monthStart(offset int) time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, offset, 0)
}

func partitionName(month time.Time) string {
	return fmt.Sprintf("security_events_y%04dm%02d", month.Year(), int(month.Month()))
}

// appendAt writes one event into a given month, bypassing the Recorder's clock.
// The INSERT policy is unconditional (the writer runs before any tenant is
// known), so the app role can do this; nothing can ever delete it again, which
// is the point of the table and the reason retention is a partition drop.
func (h *retentionHarness) appendAt(t *testing.T, at time.Time, digest string) {
	t.Helper()
	_, err := h.db.ExecContext(context.Background(), `
		INSERT INTO security_events
		    (occurred_at, event, outcome, method, reason, subject_digest, client_ip, client_ip_source)
		VALUES ($1, $2, 'failure', 'password', $3, $4, '203.0.113.7'::inet, 'peer')`,
		at.UTC(), EventLoginFailed, ReasonNoSuchPrincipal, digest)
	require.NoError(t, err)
}

func (h *retentionHarness) countByDigest(t *testing.T, digest string) int {
	t.Helper()
	entries, err := h.reader.List(context.Background(), Filter{SubjectDigest: digest, Limit: MaxReadLimit})
	require.NoError(t, err)
	return len(entries)
}

func (h *retentionHarness) partitionExists(t *testing.T, name string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, h.db.QueryRowxContext(context.Background(),
		`SELECT EXISTS (
		     SELECT 1 FROM pg_class c
		       JOIN pg_inherits i ON i.inhrelid = c.oid
		      WHERE i.inhparent = 'security_events'::regclass AND c.relname = $1)`,
		name).Scan(&exists))
	return exists
}

// assertPrivilegesMatchTheParent compares what the CONNECTED role may do to a
// partition with what it may do to security_events itself. It asks as the app
// role, through has_table_privilege, which is the question that actually
// matters: not "is there an ACL entry" but "can the process that has to read and
// write this table still do so".
func (h *retentionHarness) assertPrivilegesMatchTheParent(t *testing.T, part string) {
	t.Helper()
	for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
		var onParent, onPartition bool
		require.NoError(t, h.db.QueryRowxContext(context.Background(),
			`SELECT has_table_privilege(current_user, 'security_events', $1),
			        has_table_privilege(current_user, $2, $1)`,
			priv, part).Scan(&onParent, &onPartition))
		assert.Equal(t, onParent, onPartition,
			"%s on %s must match %s on security_events: a runtime-created month with no grants is a table the API cannot address directly",
			priv, part, priv)
	}
}

func randomDigest(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	require.NoError(t, err)
	return hex.EncodeToString(buf)
}

// TestAMonthPastTheWindowIsDroppedAndAMonthInsideItIsNot is the whole control in
// one test, and it is deliberately one test rather than three: the three facts
// only mean something TOGETHER. A job that drops everything also drops the month
// past the window, and a job that drops nothing also leaves the month inside it.
//
// The window is moved rather than the clock, because the clock is the one thing
// the maintenance function will not take from a caller: it derives its cutoff
// from now() so that no caller can ask it to age out an arbitrary point in
// time. Thirteen months keeps the month at M-13; twelve does not. Everything
// else about the run is identical.
func TestAMonthPastTheWindowIsDroppedAndAMonthInsideItIsNot(t *testing.T) {
	h := newRetentionHarness(t)
	ctx := context.Background()

	expiring := monthStart(-13) // kept under 13 months, past the window under 12
	surviving := monthStart(-12)
	// Older than any window this product will accept (the ceiling is 120
	// months), so no run will ever give this month a partition of its own and
	// its row is guaranteed to sit in security_events_default — which is exactly
	// the row the drop must leave alone even though it is the oldest of the
	// three.
	older := monthStart(-200)

	// A pass at the real window makes sure both months have a partition of their
	// own. This is the create half of the same invariant: a month with no
	// partition lands in security_events_default, where retention can never
	// reach it.
	opening, err := NewRetention(h.exec, RetentionSettings{RetentionMonths: 13, MonthsAhead: 1}).Run(ctx)
	require.NoError(t, err)
	require.NotContains(t, opening.Dropped, partitionName(expiring),
		"a month INSIDE the thirteen-month window must not be dropped by a run at thirteen months")
	require.True(t, h.partitionExists(t, partitionName(expiring)))
	require.True(t, h.partitionExists(t, partitionName(surviving)))

	// A month created at RUNTIME must be indistinguishable from one created by a
	// deploy. It is a brand-new table owned by the maintenance function's
	// definer, and Postgres gives a new table no grants at all, while
	// migrate.sh's `GRANT ... ON ALL TABLES` only ever reached the partitions
	// that existed when the deploy ran. Nothing notices while access is routed
	// through the parent — which is what makes this the kind of hole that
	// surfaces months later, as a permission-denied on a table nobody remembers
	// creating.
	h.assertPrivilegesMatchTheParent(t, partitionName(expiring))
	h.assertPrivilegesMatchTheParent(t, partitionName(surviving))

	// Three rows: one in each of those months, and one older than either, which
	// has no partition and therefore sits in the DEFAULT — the partition the
	// drop must never take, because it has no lower bound and could hold a row
	// from any month, including this one.
	digest := randomDigest(t)
	h.appendAt(t, expiring.AddDate(0, 0, 5), digest)
	h.appendAt(t, surviving.AddDate(0, 0, 5), digest)
	h.appendAt(t, older.AddDate(0, 0, 5), digest)
	require.Equal(t, 3, h.countByDigest(t, digest), "the three events must be in the stream to begin with")

	// The window closes by one month. The month at M-13 is now WHOLLY past it —
	// its newest possible row is older than the promise — so it goes.
	res, err := NewRetention(h.exec, RetentionSettings{RetentionMonths: 12, MonthsAhead: 1}).Run(ctx)
	require.NoError(t, err)

	assert.Contains(t, res.Dropped, partitionName(expiring),
		"a month whose newest possible row is past the window must be dropped")
	assert.NotContains(t, res.Dropped, partitionName(surviving),
		"a month that can still hold a row inside the window must survive")
	assert.NotContains(t, res.Dropped, "security_events_default",
		"the default partition has no bounds and may hold a row from any month; it must never be dropped")
	assert.Empty(t, res.Blocked, "no month inside the window should be unreachable")

	assert.False(t, h.partitionExists(t, partitionName(expiring)), "the expired partition is gone from the catalogue")
	assert.True(t, h.partitionExists(t, partitionName(surviving)), "the month inside the window is untouched")
	assert.True(t, h.partitionExists(t, "security_events_default"), "the backstop survived a run that dropped its neighbour")

	// One row stopped existing and two did not — including the one in the
	// default partition, which is older than BOTH and still there. That is the
	// difference between "retention" and "delete whatever looks old".
	assert.Equal(t, 2, h.countByDigest(t, digest),
		"exactly the event in the dropped month stopped existing — the address with it")

	// And the create half, on the only path where a partition is genuinely BORN
	// at runtime: the window re-opens, the month that was just dropped is made
	// again, and it comes back with the parent's privileges rather than as a
	// table the API can only reach through its parent.
	reopened, err := NewRetention(h.exec, RetentionSettings{RetentionMonths: 13, MonthsAhead: 1}).Run(ctx)
	require.NoError(t, err)
	require.Contains(t, reopened.Created, partitionName(expiring),
		"a month inside the window with no partition must be given one")
	h.assertPrivilegesMatchTheParent(t, partitionName(expiring))
}

// TestARunWithNothingToDoIsQuiet. The drop set only moves on the first of a
// month, so this is what almost every run of this job looks like: it must cost
// one catalogue query, change nothing, and say nothing. A job that announces
// every six hours that a month boundary has not been crossed is a job whose real
// messages — a dropped month, a month it could not reach — nobody reads.
func TestARunWithNothingToDoIsQuiet(t *testing.T) {
	h := newRetentionHarness(t)
	ctx := context.Background()

	retention := NewRetention(h.exec, RetentionSettings{RetentionMonths: 13, MonthsAhead: 1})

	// The first pass may well have work: it is what brings the invariant up to
	// date on a database that has not seen this job before.
	_, err := retention.Run(ctx)
	require.NoError(t, err)

	// The second cannot. Nothing has changed, so nothing is created, nothing is
	// dropped, and the result carries no line for anyone to log.
	res, err := retention.Run(ctx)
	require.NoError(t, err)
	assert.True(t, res.Empty(), "a second pass over an already-maintained stream must do nothing: %+v", res)
}

// TestMonthsAreUTCMonthsWhateverTheSessionTimezone. date_trunc('month', now())
// truncates in the SESSION's timezone, and a DATE literal becomes a TIMESTAMPTZ
// partition bound in the session's timezone too — so on a connection that is not
// UTC this job would compute a different cutoff AND cut partitions at local
// midnights, which do not meet the UTC midnights every partition the migration
// created starts and ends on. Where the mismatch does not overlap (and so is not
// refused) it leaves a silent gap of a few hours each month, and a row falling
// into that gap lands in the default partition, where retention can never reach
// it: the failure would be a slow leak of exactly the personal datum the window
// exists to bound.
//
// The connection here is deliberately hostile: one pinned connection at UTC+14,
// the largest offset there is.
func TestMonthsAreUTCMonthsWhateverTheSessionTimezone(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set: skipping the Postgres retention tests")
	}
	db, err := sqlx.Open("pgx", url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// One connection, so the SET below governs every statement that follows.
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))
	_, err = db.ExecContext(ctx, `SET TimeZone = 'Pacific/Kiritimati'`) // UTC+14
	require.NoError(t, err)

	// A fourteen-month window reaches one month further back than the decided
	// one, so the month it needs is the one month that does not exist yet. Any
	// later pass at thirteen months drops it again, so the test cleans up after
	// itself by the same mechanism it is testing.
	from, to := monthStart(-14), monthStart(-13)
	res, err := NewRetention(database.NewExec(db), RetentionSettings{RetentionMonths: 14, MonthsAhead: 1}).Run(ctx)
	require.NoError(t, err)
	require.Contains(t, res.Created, partitionName(from))

	var lower, upper time.Time
	require.NoError(t, db.QueryRowxContext(ctx, `
		SELECT (regexp_match(pg_get_expr(c.relpartbound, c.oid),
		           '^FOR VALUES FROM \(''([^'']+)''\) TO \(''([^'']+)''\)$'))[1]::timestamptz,
		       (regexp_match(pg_get_expr(c.relpartbound, c.oid),
		           '^FOR VALUES FROM \(''([^'']+)''\) TO \(''([^'']+)''\)$'))[2]::timestamptz
		  FROM pg_class c WHERE c.relname = $1`, partitionName(from)).Scan(&lower, &upper))

	assert.True(t, lower.UTC().Equal(from),
		"the partition must start at the UTC month boundary, not the connection's: got %s, want %s", lower.UTC(), from)
	assert.True(t, upper.UTC().Equal(to),
		"and end at the next one: got %s, want %s", upper.UTC(), to)
}

// TestTheWindowHasAFloorInTheSchemaAndNotOnlyInGo. The floor is what stops an
// environment variable from quietly shortening a retention promise about
// personal data below a SOC 2 audit period. Go refuses it before the call, which
// is the error an operator will actually see — but the function is reachable by
// anything holding the app role, so the floor that matters is the one in the
// database.
func TestTheWindowHasAFloorInTheSchemaAndNotOnlyInGo(t *testing.T) {
	h := newRetentionHarness(t)
	ctx := context.Background()

	_, err := NewRetention(h.exec, RetentionSettings{RetentionMonths: 11, MonthsAhead: 1}).Run(ctx)
	require.Error(t, err, "Go must refuse a window under the floor")
	assert.Contains(t, err.Error(), "12-month floor")

	// And the database refuses it on its own account, for a caller that never
	// went through Go.
	var action, name string
	err = h.db.QueryRowxContext(ctx,
		`SELECT action, partition_name FROM security_events_maintain_partitions(11, 1)`).Scan(&action, &name)
	require.Error(t, err, "the schema must refuse a window under the floor whoever asks")
	assert.Contains(t, err.Error(), "ZT032")
}
