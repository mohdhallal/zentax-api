package scheduler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // the pgx driver these tests open sqlx with
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests need a real Postgres: an advisory lock is the mechanism under
// test, and a fake would prove nothing about it. They run against the same
// scratch database as the acceptance suite:
//
//	TEST_DATABASE_URL=postgres://zentax_test_app:zentax-test-app@localhost:5433/zentax_test?sslmode=disable \
//	  go test ./platform/scheduler/...
//
// Each test locks its OWN namespace (a fresh UUID), so a run never contends
// with another run, with the acceptance suite, or with the real deployment's
// "zentax:scheduler" lease.

func testDB(t *testing.T) *sqlx.DB {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set: skipping the Postgres leadership tests")
	}
	db, err := sqlx.Open("pgx", url)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx))

	t.Cleanup(func() { _ = db.Close() })
	return db
}

// holders asks Postgres itself how many sessions hold the namespace's advisory
// lock — the ground truth the election exists to control. A bigint advisory key
// is split across pg_locks.classid (high 32 bits) and objid (low 32).
func holders(t *testing.T, db *sqlx.DB, namespace string) int {
	t.Helper()

	key := uint64(LockKey(namespace)) //nolint:gosec // the same round trip the lock itself makes.
	var n int
	require.NoError(t, db.Get(&n, `
		SELECT count(*) FROM pg_locks
		WHERE locktype = 'advisory' AND granted
		  AND classid = $1::oid AND objid = $2::oid AND objsubid = 1`,
		int64(key>>32), int64(key&0xFFFFFFFF)))
	return n
}

// Two schedulers, one database: exactly one of them runs anything. This is the
// property the whole in-process design rests on — several API tasks start the
// same jobs, and the lock decides which task is the runner.
func TestTwoSchedulersOnOneDatabaseElectExactlyOneRunner(t *testing.T) {
	namespace := "zentax:test:" + uuid.NewString()

	// Two connection pools stand in for two API tasks.
	dbA, dbB := testDB(t), testDB(t)

	tasks := make([]*task, 0, 2)
	for _, db := range []*sqlx.DB{dbA, dbB} {
		tk := &task{work: &counter{}}
		tk.sched = New(NewAdvisoryLease(db, namespace), &spyRecorder{}, fastSettings())
		require.NoError(t, tk.sched.Register(Job{Name: "work", Interval: testTick, Run: tk.work.run}))
		tasks = append(tasks, tk)
	}
	for _, tk := range tasks {
		tk.stop = start(t, tk.sched)
	}

	require.Eventually(t, func() bool { return tasks[0].work.count() > 0 || tasks[1].work.count() > 0 },
		5*time.Second, testTick, "neither task ever became the runner")

	// Let both loops turn many times over.
	time.Sleep(settle)

	leader, follower := tasks[0], tasks[1]
	if follower.sched.IsLeader() {
		leader, follower = follower, leader
	}
	require.True(t, leader.sched.IsLeader())
	assert.False(t, follower.sched.IsLeader(), "exactly one task may believe it is the runner")
	assert.Zero(t, follower.work.count(), "the follower ran work it does not own")
	assert.Equal(t, 1, holders(t, dbA, namespace), "exactly one session may hold the advisory lock")

	// Handing over: the runner stops, and the survivor picks the work up
	// without waiting for anything to expire.
	leader.stop()
	require.Eventually(t, func() bool { return follower.work.count() > 0 },
		5*time.Second, testTick, "the survivor never took over after the runner stopped")
	assert.True(t, follower.sched.IsLeader())
	assert.Equal(t, 1, holders(t, dbA, namespace), "leadership moved, it did not multiply")
}

// task is one stand-in API process.
type task struct {
	sched *Scheduler
	work  *counter
	stop  func()
}

// The lease itself: one holder at a time, idempotent for the holder, and given
// back on release.
func TestAdvisoryLeaseIsExclusiveIdempotentAndReleasable(t *testing.T) {
	namespace := "zentax:test:" + uuid.NewString()
	dbA, dbB := testDB(t), testDB(t)
	ctx := context.Background()

	leaseA := NewAdvisoryLease(dbA, namespace)
	leaseB := NewAdvisoryLease(dbB, namespace)

	held, err := leaseA.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, held)
	assert.Equal(t, 1, holders(t, dbA, namespace))

	// The holder re-confirming does not take a second lock (which would need a
	// second unlock to release).
	held, err = leaseA.Acquire(ctx)
	require.NoError(t, err)
	assert.True(t, held)
	assert.Equal(t, 1, holders(t, dbA, namespace), "re-confirming must not stack locks")

	// A second process is refused — and that refusal is NOT an error.
	held, err = leaseB.Acquire(ctx)
	require.NoError(t, err)
	assert.False(t, held, "two processes must never hold the runner lease at once")

	leaseA.Release(ctx)
	assert.Zero(t, holders(t, dbA, namespace))

	held, err = leaseB.Acquire(ctx)
	require.NoError(t, err)
	assert.True(t, held, "a released lease is immediately available")
	leaseB.Release(ctx)
	assert.Zero(t, holders(t, dbB, namespace))
}

// A lease nobody took is safe to release, and a scheduler that never led holds
// nothing to give back.
func TestReleasingAnUnheldLeaseIsHarmless(t *testing.T) {
	namespace := "zentax:test:" + uuid.NewString()
	db := testDB(t)

	lease := NewAdvisoryLease(db, namespace)
	lease.Release(context.Background())
	assert.Zero(t, holders(t, db, namespace))
}
