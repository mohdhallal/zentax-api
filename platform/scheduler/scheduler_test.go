package scheduler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/logger"
)

// The loop is timing-driven, so the tests drive it fast: a 2ms tick and a 2ms
// probe make a "tick" cheap enough to assert on with Eventually.
const (
	testTick  = 2 * time.Millisecond
	testProbe = 2 * time.Millisecond
	// settle is how long a test waits for something that should NOT happen.
	settle = 60 * time.Millisecond
)

func TestMain(m *testing.M) {
	logger.Init(&logger.Config{Level: "error", Writer: io.Discard})
	os.Exit(m.Run())
}

// --- doubles ---------------------------------------------------------------

type fakeLease struct {
	mu       sync.Mutex
	held     bool
	err      error
	acquires int
	releases int
}

func (f *fakeLease) Acquire(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquires++
	return f.held, f.err
}

func (f *fakeLease) Release(context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	f.held = false
}

func (f *fakeLease) Name() string { return "test-lease" }

func (f *fakeLease) set(held bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held, f.err = held, err
}

func (f *fakeLease) counts() (acquires, releases int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acquires, f.releases
}

type observation struct {
	client    string
	operation string
	elapsed   time.Duration
}

type spyRecorder struct {
	mu   sync.Mutex
	seen []observation
}

func (s *spyRecorder) ObserveDuration(client, operation string, elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, observation{client, operation, elapsed})
}

func (s *spyRecorder) count(operation string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, o := range s.seen {
		if o.operation == operation {
			n++
		}
	}
	return n
}

// counter is a job body that counts its runs.
type counter struct{ n atomic.Int64 }

func (c *counter) run(context.Context) error {
	c.n.Add(1)
	return nil
}

func (c *counter) count() int { return int(c.n.Load()) }

// wedged is a job that ignores its cancellation entirely: it returns only when
// its release channel is closed. It stands in for work stuck on a socket with
// no deadline — the case where cancelling the context achieves nothing at all.
type wedged struct {
	entered atomic.Int64
	release chan struct{}
}

func (w *wedged) run(context.Context) error {
	w.entered.Add(1)
	<-w.release
	return nil
}

func fastSettings() Settings {
	return Settings{Tick: testTick, ProbeInterval: testProbe, ReleaseTimeout: time.Second}
}

// impatientSettings gives a job almost no grace to wind up, so a test can watch
// the loop stop waiting for one.
func impatientSettings() Settings {
	s := fastSettings()
	s.AbandonGrace = 20 * time.Millisecond
	return s
}

// start runs the scheduler in the background and returns a stop function that
// cancels it and waits for Run to return.
func start(t *testing.T, s *Scheduler) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("scheduler did not stop within 2s of cancellation")
		}
	}
	t.Cleanup(stop)
	return stop
}

// --- registration ----------------------------------------------------------

func TestRegisterRejectsUnrunnableJobs(t *testing.T) {
	s := New(&fakeLease{}, &spyRecorder{}, fastSettings())

	require.Error(t, s.Register(Job{Interval: time.Second, Run: func(context.Context) error { return nil }}), "no name")
	require.Error(t, s.Register(Job{Name: "j", Run: func(context.Context) error { return nil }}), "no interval")
	require.Error(t, s.Register(Job{Name: "j", Interval: time.Second}), "no run function")

	require.NoError(t, s.Register(Job{Name: "j", Interval: time.Second, Run: func(context.Context) error { return nil }}))
	require.Error(t, s.Register(Job{Name: "j", Interval: time.Second, Run: func(context.Context) error { return nil }}), "duplicate name")

	assert.Equal(t, []string{"j"}, s.Jobs())
}

func TestRunWithoutJobsNeverCompetesForLeadership(t *testing.T) {
	lease := &fakeLease{held: true}
	s := New(lease, &spyRecorder{}, fastSettings())

	s.Run(context.Background()) // returns immediately: nothing to run.

	acquires, releases := lease.counts()
	assert.Zero(t, acquires, "an idle process must not take the runner lease")
	assert.Zero(t, releases)
	assert.False(t, s.IsLeader())
}

// --- leadership ------------------------------------------------------------

func TestLeaderRunsJobsAndReleasesTheLeaseOnStop(t *testing.T) {
	lease := &fakeLease{held: true}
	rec := &spyRecorder{}
	job := &counter{}

	s := New(lease, rec, fastSettings())
	require.NoError(t, s.Register(Job{Name: "work", Interval: testTick, Run: job.run}))

	stop := start(t, s)
	require.Eventually(t, func() bool { return job.count() >= 3 }, time.Second, testTick)
	assert.True(t, s.IsLeader())
	stop()

	_, releases := lease.counts()
	assert.Equal(t, 1, releases, "the lease is handed back exactly once on shutdown")
	assert.False(t, s.IsLeader())
	assert.GreaterOrEqual(t, rec.count("work"), 3, "every successful run is timed and counted")
}

// A task that is not the runner must be completely inert: no job body runs, and
// nothing is logged — losing an election is the normal state of every task but
// one, not an incident to report every tick.
func TestFollowerRunsNothingAndSaysNothing(t *testing.T) {
	var buf syncBuffer
	restore := captureLogs(&buf)
	defer restore()

	lease := &fakeLease{held: false}
	rec := &spyRecorder{}
	job := &counter{}

	s := New(lease, rec, fastSettings())
	require.NoError(t, s.Register(Job{Name: "work", Interval: testTick, Run: job.run}))

	stop := start(t, s)
	time.Sleep(settle) // many ticks' worth.
	logs := buf.String()
	stop()

	assert.Zero(t, job.count(), "a follower must do no work at all")
	assert.False(t, s.IsLeader())
	assert.Zero(t, rec.count(opElectionFailed), "losing an election is not a failure")

	acquires, releases := lease.counts()
	assert.Greater(t, acquires, 1, "a follower keeps offering itself")
	assert.Zero(t, releases, "a follower holds nothing to release")

	assert.Equal(t, 1, strings.Count(logs, "\n"), "a follower logs exactly the one startup line:\n"+logs)
	assert.NotContains(t, logs, "ERROR")
	assert.NotContains(t, logs, "WARN")
}

func TestLeadershipLostMidFlightStopsTheWork(t *testing.T) {
	lease := &fakeLease{held: true}
	job := &counter{}

	s := New(lease, &spyRecorder{}, fastSettings())
	require.NoError(t, s.Register(Job{Name: "work", Interval: testTick, Run: job.run}))

	start(t, s)
	require.Eventually(t, func() bool { return job.count() >= 2 }, time.Second, testTick)

	lease.set(false, nil)
	require.Eventually(t, func() bool { return !s.IsLeader() }, time.Second, testTick)

	stalled := job.count()
	time.Sleep(settle)
	assert.Equal(t, stalled, job.count(), "work stops the moment leadership does")
}

// A lease probe that ERRORS is a real fault (the database is unreachable) and is
// reported — but at the probe cadence, not on every tick, and it never runs work.
func TestLeaseFaultIsCountedAndStopsWork(t *testing.T) {
	lease := &fakeLease{held: false, err: errors.New("connection refused")}
	rec := &spyRecorder{}
	job := &counter{}

	s := New(lease, rec, Settings{Tick: time.Millisecond, ProbeInterval: 25 * time.Millisecond})
	require.NoError(t, s.Register(Job{Name: "work", Interval: time.Millisecond, Run: job.run}))

	start(t, s)
	require.Eventually(t, func() bool { return rec.count(opElectionFailed) >= 2 }, time.Second, testTick)

	assert.Zero(t, job.count())
	assert.False(t, s.IsLeader())
	// ~40 ticks in 40ms, but only ~2 probes: the fault is reported per probe.
	assert.Less(t, rec.count(opElectionFailed), 10, "a fault must not be reported once per tick")
}

// --- job behaviour ---------------------------------------------------------

// A panicking job is contained: the loop survives it, records it, and tries the
// job again next interval — a bad job can never take the API process down.
func TestPanickingJobIsContained(t *testing.T) {
	lease := &fakeLease{held: true}
	rec := &spyRecorder{}
	healthy := &counter{}

	s := New(lease, rec, fastSettings())
	require.NoError(t, s.Register(Job{Name: "boom", Interval: testTick, Run: func(context.Context) error {
		panic("scheduled work went wrong")
	}}))
	require.NoError(t, s.Register(Job{Name: "healthy", Interval: testTick, Run: healthy.run}))

	start(t, s)

	require.Eventually(t, func() bool { return rec.count("boom"+opSuffixPanicked) >= 2 }, time.Second, testTick,
		"the panicking job is retried, not abandoned")
	require.Eventually(t, func() bool { return healthy.count() >= 2 }, time.Second, testTick,
		"a panic in one job must not stop the others")
	assert.True(t, s.IsLeader())
}

func TestFailingJobIsCountedNotFatal(t *testing.T) {
	lease := &fakeLease{held: true}
	rec := &spyRecorder{}

	s := New(lease, rec, fastSettings())
	require.NoError(t, s.Register(Job{Name: "flaky", Interval: testTick, Run: func(context.Context) error {
		return errors.New("nope")
	}}))

	start(t, s)
	require.Eventually(t, func() bool { return rec.count("flaky"+opSuffixFailed) >= 2 }, time.Second, testTick)
	assert.Zero(t, rec.count("flaky"), "a failed run is not counted as a successful one")
}

func TestJobRunIsBoundedByItsTimeout(t *testing.T) {
	lease := &fakeLease{held: true}
	rec := &spyRecorder{}
	deadlines := make(chan error, 4)

	s := New(lease, rec, fastSettings())
	require.NoError(t, s.Register(Job{
		Name:     "slow",
		Interval: 10 * time.Millisecond,
		Timeout:  5 * time.Millisecond,
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			select {
			case deadlines <- ctx.Err():
			default:
			}
			return ctx.Err()
		},
	}))

	start(t, s)
	select {
	case err := <-deadlines:
		assert.ErrorIs(t, err, context.DeadlineExceeded, "the job's own timeout, not the process's")
	case <-time.After(time.Second):
		t.Fatal("the slow job was never cut off")
	}

	require.Eventually(t, func() bool { return rec.count("slow"+opSuffixFailed) >= 1 }, time.Second, testTick)
	assert.Zero(t, rec.count("slow"+opSuffixOverran), "a job that stops when it is told is not an abandoned one")
}

// A job can only be ASKED to stop. One that does not — stuck on a socket with no
// deadline, say — used to hold the loop's only goroutine for as long as it liked,
// taking every other job with it and, worse, holding the runner lease: a lease is
// handed back by a Run that returns, so a wedged job on one task meant no other
// task could ever take the work over. The loop now stops waiting for it.
func TestAJobThatIgnoresItsCancellationIsAbandoned(t *testing.T) {
	lease := &fakeLease{held: true}
	rec := &spyRecorder{}
	stuck := &wedged{release: make(chan struct{})}
	healthy := &counter{}

	s := New(lease, rec, impatientSettings())
	require.NoError(t, s.Register(Job{
		Name: "stuck", Interval: testTick, Timeout: 5 * time.Millisecond, Run: stuck.run,
	}))
	require.NoError(t, s.Register(Job{Name: "healthy", Interval: testTick, Run: healthy.run}))

	t.Cleanup(func() { close(stuck.release) }) // released after the loop has stopped.
	start(t, s)

	require.Eventually(t, func() bool { return rec.count("stuck"+opSuffixOverran) >= 1 }, time.Second, testTick,
		"the loop must not wait on a job that will not come back")
	require.Eventually(t, func() bool { return healthy.count() >= 3 }, time.Second, testTick,
		"one wedged job must not stop every other job")

	assert.Equal(t, int64(1), stuck.entered.Load(), "a wedged job must not become a pile of wedged jobs")
	assert.Equal(t, 1, rec.count("stuck"+opSuffixOverran), "and the overrun is reported once, not once per tick")
}

// The consequence that matters on shutdown: the process stands down and hands
// leadership back even with a job still stuck, so a surviving task picks the
// work up within a probe interval instead of waiting for a wedged process.
func TestAWedgedJobDoesNotHoldTheLeaseOpen(t *testing.T) {
	lease := &fakeLease{held: true}
	stuck := &wedged{release: make(chan struct{})}
	defer close(stuck.release)

	s := New(lease, &spyRecorder{}, impatientSettings())
	require.NoError(t, s.Register(Job{
		Name: "stuck", Interval: time.Hour, Timeout: 5 * time.Millisecond, Run: stuck.run,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() { s.Run(ctx); close(returned) }()
	require.Eventually(t, func() bool { return stuck.entered.Load() == 1 }, time.Second, testTick)

	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("a wedged job held Run open, so the lease would never be handed back")
	}

	_, releases := lease.counts()
	assert.Equal(t, 1, releases, "leadership goes back so another task can take the work on")
	assert.False(t, s.IsLeader())
}

func TestEachJobKeepsItsOwnInterval(t *testing.T) {
	lease := &fakeLease{held: true}
	fast, slow := &counter{}, &counter{}

	s := New(lease, &spyRecorder{}, fastSettings())
	require.NoError(t, s.Register(Job{Name: "fast", Interval: 2 * time.Millisecond, Run: fast.run}))
	require.NoError(t, s.Register(Job{Name: "slow", Interval: 200 * time.Millisecond, Run: slow.run}))

	start(t, s)
	require.Eventually(t, func() bool { return fast.count() >= 10 }, 2*time.Second, testTick)

	assert.LessOrEqual(t, slow.count(), 2, "the slow job must not ride the fast one's interval")
}

func TestRunReturnsWhenTheContextIsCancelled(t *testing.T) {
	s := New(&fakeLease{held: true}, &spyRecorder{}, fastSettings())
	require.NoError(t, s.Register(Job{Name: "work", Interval: time.Hour, Run: func(context.Context) error { return nil }}))

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() { s.Run(ctx); close(returned) }()

	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored its cancelled context")
	}
}

// --- lock key --------------------------------------------------------------

func TestLockKeyIsStableAndNamespaced(t *testing.T) {
	assert.Equal(t, LockKey("zentax:scheduler"), LockKey("zentax:scheduler"), "the key must not move between releases")
	assert.NotEqual(t, LockKey("zentax:scheduler"), LockKey("zentax:other"))
}

// --- helpers ---------------------------------------------------------------

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs points the process logger at buf and returns a restore function.
// The tests in this package never run in parallel, so the global is safe to
// borrow.
func captureLogs(buf *syncBuffer) func() {
	previous := logger.Log
	logger.Init(&logger.Config{Level: "debug", Writer: buf})
	return func() { logger.Log = previous }
}
