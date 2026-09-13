// Package scheduler runs the product's recurring work — deadline reminders,
// digests, outbox delivery, queue pruning — INSIDE the API process, with
// exactly one process elected runner through a Postgres advisory lock.
//
// Why in-process, and why a lock:
//
//   - The self-hosted edition ships as one container plus one Postgres. Work
//     that only happens because a cloud scheduler fires would simply never
//     happen there, and reminders are the product's core promise: a tax
//     deadline nobody was told about is the failure the product exists to
//     prevent.
//   - The hosted edition runs several API tasks. The advisory lock (see
//     AdvisoryLease) makes one of them the runner without adding a broker, a
//     lock table, or a second deployable — and without any configuration that
//     could accidentally name two runners.
//   - A separate worker process would either be a second Go binary to build,
//     ship, secure and observe, or — as an older roadmap line proposed — a Node
//     worker beside a Go backend, which ADR-0019 rules out. A scheduled cloud
//     task per job multiplies deployment surface for work measured in seconds.
//
// Behavioural contract, which the tests hold to:
//
//   - A task that does not hold the lease does NOTHING: no job runs, no error
//     is logged, no failure is counted. Losing an election is the normal state
//     of every task but one.
//   - Jobs run sequentially, each with a timeout, so a slow job delays its
//     peers rather than piling up copies of itself. A job that outlives its
//     timeout AND ignores the cancellation is ABANDONED — the loop stops
//     waiting on it, logs it, and refuses to start a second copy until the
//     first returns. Waiting instead would hand one wedged job the power to
//     stop every other job, and to hold the runner lease indefinitely so that
//     no other process could take the work over.
//   - A job that panics is contained: the panic is recovered, logged and
//     counted, and the job is tried again on its next interval. Nothing a job
//     does can take the API process down.
//   - Run returns only when its context is cancelled, and it releases the lease
//     on the way out so a surviving task takes over within one probe interval.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/metrics"
)

// metricClient is the client_name label every scheduler timing carries; the
// operation label is the job name (plus a suffix for the failure modes), so one
// histogram gives both the counts and the durations.
const metricClient = "scheduler"

const (
	opSuffixFailed   = ".failed"
	opSuffixPanicked = ".panicked"
	opSuffixOverran  = ".overran"
	opElectionFailed = "election.failed"
)

// Default loop settings. Tick is the resolution at which the loop notices a job
// is due; ProbeInterval is how often leadership is re-checked (a holder pings
// its pinned session, a follower re-offers itself).
const (
	DefaultTick           = time.Second
	DefaultProbeInterval  = 5 * time.Second
	DefaultReleaseTimeout = 5 * time.Second
	// DefaultAbandonGrace is how long a job has to wind up after its context is
	// cancelled before the loop stops waiting for it. It is slack for an
	// orderly exit, not a second budget.
	DefaultAbandonGrace = 5 * time.Second
)

// Job is one unit of recurring work.
type Job struct {
	// Name identifies the job in logs and in the metrics operation label.
	Name string
	// Interval is how often the job runs while this process is the runner.
	Interval time.Duration
	// Timeout bounds one run. Zero means Interval — a job may never take
	// longer than the gap to its own next run.
	Timeout time.Duration
	// Run does the work. It must respect ctx: cancellation is how shutdown
	// reaches it.
	Run func(ctx context.Context) error
}

type job struct {
	Job
	nextRun time.Time
	// running is held for as long as a run of this job is in flight. It only
	// ever outlives runJob for an ABANDONED job — an ordinary run, cut off or
	// not, clears it before runJob returns — and while it is set the loop skips
	// the job, so a wedged job can never become a pile of wedged jobs.
	running atomic.Bool
}

// Settings tune the loop. Zero values take the Default* constants.
type Settings struct {
	Tick           time.Duration
	ProbeInterval  time.Duration
	ReleaseTimeout time.Duration
	// AbandonGrace is how long the loop waits for a job to return after its
	// context has been cancelled, before giving up on it and moving on.
	AbandonGrace time.Duration
}

func (s Settings) withDefaults() Settings {
	if s.Tick <= 0 {
		s.Tick = DefaultTick
	}
	if s.ProbeInterval <= 0 {
		s.ProbeInterval = DefaultProbeInterval
	}
	if s.ReleaseTimeout <= 0 {
		s.ReleaseTimeout = DefaultReleaseTimeout
	}
	if s.AbandonGrace <= 0 {
		s.AbandonGrace = DefaultAbandonGrace
	}
	return s
}

// Scheduler is the loop. Build it with New, Register every job before Run, then
// Run it once for the life of the process.
type Scheduler struct {
	lease    Lease
	recorder metrics.ClientRecorder
	settings Settings

	jobs      []*job
	leader    atomic.Bool
	nextProbe time.Time

	// now is the clock, injectable for tests.
	now func() time.Time
}

func New(lease Lease, recorder metrics.ClientRecorder, settings Settings) *Scheduler {
	return &Scheduler{
		lease:    lease,
		recorder: recorder,
		settings: settings.withDefaults(),
		now:      time.Now,
	}
}

// Register adds a job. It must be called before Run — the loop reads the job
// list without a lock, precisely because registration is a startup-time act.
func (s *Scheduler) Register(j Job) error {
	switch {
	case j.Name == "":
		return errors.New("scheduler: job needs a name")
	case j.Interval <= 0:
		return fmt.Errorf("scheduler: job %q needs a positive interval", j.Name)
	case j.Run == nil:
		return fmt.Errorf("scheduler: job %q needs a Run function", j.Name)
	}
	for _, existing := range s.jobs {
		if existing.Name == j.Name {
			return fmt.Errorf("scheduler: job %q is already registered", j.Name)
		}
	}
	s.jobs = append(s.jobs, &job{Job: j})
	return nil
}

// Jobs lists the registered job names, in registration order.
func (s *Scheduler) Jobs() []string {
	names := make([]string, 0, len(s.jobs))
	for _, j := range s.jobs {
		names = append(names, j.Name)
	}
	return names
}

// IsLeader reports whether this process currently holds the lease. It is the
// honest answer for a readiness probe or a test — never a precondition to be
// checked before doing work, because leadership can change between the check
// and the work. The loop is the only thing that acts on it.
func (s *Scheduler) IsLeader() bool { return s.leader.Load() }

// Run drives the loop until ctx is cancelled, then releases the lease. It
// blocks; run it in its own goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	if len(s.jobs) == 0 {
		logger.Log.Info("Scheduler has no registered jobs; not competing for leadership")
		return
	}

	logger.Log.Info("Scheduler starting",
		logger.String("lease", s.lease.Name()),
		logger.Int("jobs", len(s.jobs)),
	)

	ticker := time.NewTicker(s.settings.Tick)
	defer ticker.Stop()
	defer s.releaseLease()

	for {
		select {
		case <-ctx.Done():
			logger.Log.Info("Scheduler stopping", logger.String("lease", s.lease.Name()))
			return
		case <-ticker.C:
			s.step(ctx)
		}
	}
}

// step is one turn of the loop: confirm leadership, then run whatever is due.
func (s *Scheduler) step(ctx context.Context) {
	if !s.holdsLease(ctx) {
		return // a follower does nothing at all.
	}

	for _, j := range s.jobs {
		if ctx.Err() != nil {
			return
		}
		now := s.now()
		if !j.nextRun.IsZero() && now.Before(j.nextRun) {
			continue
		}
		if j.running.Load() {
			// An abandoned run is still out there. Its overrun was logged and
			// counted once, when the loop stopped waiting for it; saying so
			// again every tick would bury the incident in its own noise.
			logger.Log.Debug("Scheduled job is still running from an earlier turn; skipping",
				logger.String("job", j.Name),
			)
			continue
		}
		j.nextRun = now.Add(j.Interval)
		s.runJob(ctx, j)
	}
}

// holdsLease returns whether this process may run work now, probing the lease
// at most once per ProbeInterval. Between probes the cached answer stands, so a
// follower's tick costs nothing and a leader's jobs are not gated on a round
// trip to Postgres.
func (s *Scheduler) holdsLease(ctx context.Context) bool {
	now := s.now()
	if !s.nextProbe.IsZero() && now.Before(s.nextProbe) {
		return s.leader.Load()
	}
	s.nextProbe = now.Add(s.settings.ProbeInterval)

	held, err := s.lease.Acquire(ctx)
	if err != nil {
		// A genuine fault (the database is unreachable), not a lost election.
		// It is counted and logged once per probe interval — never per tick.
		s.observe(opElectionFailed, 0)
		logger.Log.Warn("Scheduler could not probe its lease",
			logger.String("lease", s.lease.Name()),
			logger.Error(err),
		)
		s.setLeader(false)
		return false
	}

	s.setLeader(held)
	return held
}

// setLeader records leadership and logs only the TRANSITIONS — becoming the
// runner and ceasing to be one are events; staying a follower is not.
func (s *Scheduler) setLeader(held bool) {
	switch {
	case held && s.leader.CompareAndSwap(false, true):
		logger.Log.Info("Scheduler acquired leadership", logger.String("lease", s.lease.Name()))
	case !held && s.leader.CompareAndSwap(true, false):
		logger.Log.Info("Scheduler lost leadership", logger.String("lease", s.lease.Name()))
	}
}

// runJob runs one job with a timeout and a recovered panic, and records the
// outcome. It never returns an error: a failing job is an observation, not a
// reason to stop scheduling.
//
// The job runs on its own goroutine so that the LOOP's fate is not tied to the
// job's. A timeout is only a cancellation, and cancellation is cooperative: a
// job stuck on a socket that ignores its context would otherwise hold this
// goroutine for ever, and with it every other job, the leadership probe, and
// the release of the lease on shutdown — one wedged job would silently take the
// whole runner, and no other process could step in, because a lease is only
// handed back by a Run that returns. So a job that has not come back a grace
// period after its context was cancelled is abandoned: it keeps running (there
// is no way to kill a goroutine), it is logged and counted, and the loop
// refuses to start another copy of it until it finishes.
func (s *Scheduler) runJob(ctx context.Context, j *job) {
	timeout := j.Timeout
	if timeout <= 0 {
		timeout = j.Interval
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)

	start := s.now()
	done := make(chan error, 1) // buffered: an abandoned job must not block on this send.
	j.running.Store(true)
	go func() {
		defer cancel()
		err := safeRun(runCtx, j.Run)
		j.running.Store(false)
		done <- err
	}()

	returned, err := s.awaitJob(runCtx, done)
	if !returned {
		s.observe(j.Name+opSuffixOverran, s.now().Sub(start))
		logger.Log.Error("Scheduled job overran its budget and has not returned; abandoning it",
			logger.String("job", j.Name),
			logger.String("budget", timeout.String()),
			logger.String("grace", s.settings.AbandonGrace.String()),
		)
		return
	}
	elapsed := s.now().Sub(start)

	var panicked *PanicError
	switch {
	case err == nil:
		s.observe(j.Name, elapsed)
		logger.Log.Debug("Scheduled job ran",
			logger.String("job", j.Name),
			logger.String("took", elapsed.String()),
		)
	case errors.As(err, &panicked):
		s.observe(j.Name+opSuffixPanicked, elapsed)
		logger.Log.Error("Scheduled job panicked",
			logger.String("job", j.Name),
			logger.Error(err),
			logger.String("stack", panicked.Stack),
		)
	default:
		s.observe(j.Name+opSuffixFailed, elapsed)
		logger.Log.Error("Scheduled job failed",
			logger.String("job", j.Name),
			logger.String("took", elapsed.String()),
			logger.Error(err),
		)
	}
}

// awaitJob waits for a run to finish. It reports the job's error and whether it
// came back at all: once the run context is done — the job's timeout, or
// shutdown — the job has AbandonGrace to wind up, and after that the loop stops
// waiting. A job that returns within the grace is an ordinary outcome (usually
// a context.DeadlineExceeded of its own making) and is reported as one.
func (s *Scheduler) awaitJob(runCtx context.Context, done <-chan error) (returned bool, err error) {
	select {
	case err = <-done:
		return true, err
	case <-runCtx.Done():
	}

	grace := time.NewTimer(s.settings.AbandonGrace)
	defer grace.Stop()
	select {
	case err = <-done:
		return true, err
	case <-grace.C:
		return false, nil
	}
}

func (s *Scheduler) observe(operation string, d time.Duration) {
	if s.recorder == nil {
		return
	}
	s.recorder.ObserveDuration(metricClient, operation, d)
}

// releaseLease hands leadership back on shutdown. The run context is cancelled
// by the time this runs, so it builds its own.
func (s *Scheduler) releaseLease() {
	if !s.leader.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.settings.ReleaseTimeout)
	defer cancel()
	s.lease.Release(ctx)
	s.leader.Store(false)
	logger.Log.Info("Scheduler released leadership", logger.String("lease", s.lease.Name()))
}

// PanicError carries a recovered panic out of a job as an ordinary error, so
// the loop can log it, count it, and carry on.
type PanicError struct {
	Value any
	Stack string
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Value)
}

// safeRun converts a panicking job into a failing one.
func safeRun(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &PanicError{Value: p, Stack: string(debug.Stack())}
		}
	}()
	return run(ctx)
}
