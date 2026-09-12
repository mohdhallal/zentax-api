package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGate_HoldsAtMostMaxConcurrent(t *testing.T) {
	t.Parallel()

	gate := NewGate(3, 0, time.Millisecond)

	for i := range 3 {
		require.NoError(t, gate.Acquire(t.Context()), "slot %d", i)
	}
	assert.Equal(t, 3, gate.InFlight())

	require.ErrorIs(t, gate.Acquire(t.Context()), ErrOverloaded,
		"the bound is what caps peak argon2 memory; it must not stretch")

	gate.Release()
	assert.NoError(t, gate.Acquire(t.Context()), "a released slot is reusable")
}

// The queue is bounded on purpose: an unbounded one only moves the exhaustion
// from argon2's heap to the goroutines and buffers of everyone waiting.
func TestGate_QueueIsBounded_ExcessShedsWithoutWaiting(t *testing.T) {
	t.Parallel()

	const maxWait = 2 * time.Second
	gate := NewGate(1, 1, maxWait)
	require.NoError(t, gate.Acquire(t.Context()))

	queued := make(chan error, 1)
	go func() { queued <- gate.Acquire(context.Background()) }()

	require.Eventually(t, func() bool { return gate.Waiting() == 1 }, time.Second, time.Millisecond)

	// The queue is full, so this one must be refused AT ONCE rather than after
	// the wait — a request that is already going to be refused must not also
	// occupy the server for two seconds first.
	start := time.Now()
	err := gate.Acquire(t.Context())
	elapsed := time.Since(start)

	require.ErrorIs(t, err, ErrOverloaded)
	assert.Less(t, elapsed, maxWait/2, "an over-queue request must not wait first")

	gate.Release()
	assert.NoError(t, <-queued, "the queued caller gets the freed slot")
	gate.Release()
}

// A brief wait is the point: verifications are short, so a legitimate login
// that waits is a better answer than one that is refused.
func TestGate_WaitsBrieflyThenTakesAFreedSlot(t *testing.T) {
	t.Parallel()

	gate := NewGate(1, 4, 2*time.Second)
	require.NoError(t, gate.Acquire(t.Context()))

	got := make(chan error, 1)
	go func() { got <- gate.Acquire(context.Background()) }()

	require.Eventually(t, func() bool { return gate.Waiting() == 1 }, time.Second, time.Millisecond)
	gate.Release()

	select {
	case err := <-got:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a waiting caller must be handed the freed slot")
	}
}

func TestGate_WaitElapses_Sheds(t *testing.T) {
	t.Parallel()

	gate := NewGate(1, 4, 20*time.Millisecond)
	require.NoError(t, gate.Acquire(t.Context()))

	start := time.Now()
	err := gate.Acquire(t.Context())

	require.ErrorIs(t, err, ErrOverloaded)
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond, "it must actually have waited")
	assert.Zero(t, gate.Waiting(), "the queue counter must be released on the way out")
}

func TestGate_ContextCancelledWhileQueued_ReturnsContextError(t *testing.T) {
	t.Parallel()

	gate := NewGate(1, 4, 5*time.Second)
	require.NoError(t, gate.Acquire(t.Context()))

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() { got <- gate.Acquire(ctx) }()

	require.Eventually(t, func() bool { return gate.Waiting() == 1 }, time.Second, time.Millisecond)
	cancel()

	select {
	case err := <-got:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("a caller that went away must not keep waiting")
	}
	assert.Eventually(t, func() bool { return gate.Waiting() == 0 }, time.Second, time.Millisecond)
}

// The property the whole package exists for: whatever concurrency arrives,
// never more than MaxConcurrent verifications run at once.
func TestGate_UnderLoad_ConcurrencyNeverExceedsTheBound(t *testing.T) {
	t.Parallel()

	const bound = 3
	gate := NewGate(bound, 200, 2*time.Second)

	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup

	for range 120 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := gate.Acquire(context.Background()); err != nil {
				return
			}
			defer gate.Release()

			now := inFlight.Add(1)
			for {
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
			time.Sleep(time.Millisecond) // stand in for the verification
			inFlight.Add(-1)
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, peak.Load(), int64(bound),
		"peak verification memory is bound × 64 MiB; exceeding the bound is exceeding the task's memory limit")
	assert.Zero(t, gate.InFlight(), "every slot must be returned")
	assert.Zero(t, gate.Waiting())
}

func TestGate_NonPositiveArgumentsFallBackToDefaults(t *testing.T) {
	t.Parallel()

	gate := NewGate(0, -1, 0)
	assert.Equal(t, DefaultMaxConcurrent, gate.Capacity())
}

func TestGate_NilIsAPassThrough(t *testing.T) {
	t.Parallel()

	var gate *Gate
	assert.NoError(t, gate.Acquire(t.Context()))
	gate.Release()
	assert.Zero(t, gate.Capacity())
}

// A Release with no matching Acquire is a bug, but it must never deadlock the
// handler's defer.
func TestGate_UnbalancedReleaseDoesNotBlock(t *testing.T) {
	t.Parallel()

	gate := NewGate(1, 0, time.Millisecond)

	done := make(chan struct{})
	go func() { gate.Release(); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Release must never block")
	}
}
