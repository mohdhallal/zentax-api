package ratelimit

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// ErrOverloaded is returned by Acquire when no verification slot came free
// inside the wait. It is the signal to shed, not a failure.
var ErrOverloaded = errors.New("ratelimit: verification capacity exhausted")

// Gate bounds how many expensive verifications run at once.
//
// It is the valve that actually caps memory. Each argon2id verification holds
// 64 MiB for its duration (platform/crypto), and the login path pays that cost
// for an UNKNOWN address too — it verifies a dummy hash so a caller cannot tell
// registered addresses from unregistered ones by timing. Multiply that by
// arbitrary concurrency and the 1 GiB API task is gone; the orchestrator then
// kills the container, which is a far better outcome for the attacker than the
// 401 they were nominally asking for.
//
// The shape is deliberate:
//
//   - MaxConcurrent slots. Peak verification memory is MaxConcurrent × 64 MiB,
//     which is the number an operator can actually reason about against the
//     task's memory limit.
//   - A brief wait. A request that finds every slot busy waits, because
//     verifications are short (tens of milliseconds) and a legitimate login
//     that waits 100 ms is a better answer than one that is refused.
//   - A bounded queue. The number of requests that may be waiting is capped:
//     an unbounded queue just moves the exhaustion from argon2's heap to the
//     goroutines, sockets and request buffers of everyone waiting, and answers
//     them all long after the client gave up. Over the cap, a request sheds
//     IMMEDIATELY rather than waiting first.
//
// The zero value is not usable; build one with NewGate.
type Gate struct {
	slots    chan struct{}
	maxWait  time.Duration
	maxQueue int64
	waiting  atomic.Int64
}

// Gate defaults, used when a caller passes a non-positive value.
const (
	// DefaultMaxConcurrent × 64 MiB = 256 MiB of argon2 working set, which
	// fits the 1 GiB API task with room for everything else it does.
	DefaultMaxConcurrent = 4
	DefaultMaxQueued     = 16
	DefaultMaxWait       = 500 * time.Millisecond
)

// NewGate builds a Gate. Non-positive arguments fall back to the defaults
// above; maxQueued may be zero, which means "never wait, shed at once".
func NewGate(maxConcurrent, maxQueued int, maxWait time.Duration) *Gate {
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrent
	}
	if maxQueued < 0 {
		maxQueued = DefaultMaxQueued
	}
	if maxWait <= 0 {
		maxWait = DefaultMaxWait
	}
	return &Gate{
		slots:    make(chan struct{}, maxConcurrent),
		maxWait:  maxWait,
		maxQueue: int64(maxQueued),
	}
}

// Acquire takes a verification slot, waiting at most the configured time.
//
// It returns ErrOverloaded when the queue is already full (no wait at all) or
// when the wait elapsed with no slot free, and the context's error when the
// caller went away. Every non-nil error means "do not run the verification".
// A nil error obliges the caller to Release exactly once.
func (g *Gate) Acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}

	// Fast path: a free slot, no queue bookkeeping.
	select {
	case g.slots <- struct{}{}:
		return nil
	default:
	}

	// The queue is full — shed now rather than making the caller wait for an
	// answer that is already decided.
	if g.waiting.Add(1) > g.maxQueue {
		g.waiting.Add(-1)
		return ErrOverloaded
	}
	defer g.waiting.Add(-1)

	timer := time.NewTimer(g.maxWait)
	defer timer.Stop()

	select {
	case g.slots <- struct{}{}:
		return nil
	case <-timer.C:
		return ErrOverloaded
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns a slot taken by a successful Acquire.
func (g *Gate) Release() {
	if g == nil {
		return
	}
	select {
	case <-g.slots:
	default:
		// Unreachable with balanced use; never block a handler's defer on a
		// bookkeeping mistake.
	}
}

// InFlight is the number of slots currently held (tests, operational probe).
func (g *Gate) InFlight() int {
	if g == nil {
		return 0
	}
	return len(g.slots)
}

// Waiting is the number of requests currently queued for a slot.
func (g *Gate) Waiting() int {
	if g == nil {
		return 0
	}
	return int(g.waiting.Load())
}

// Capacity is the configured concurrency bound.
func (g *Gate) Capacity() int {
	if g == nil {
		return 0
	}
	return cap(g.slots)
}
