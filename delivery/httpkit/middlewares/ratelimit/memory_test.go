package ratelimit

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock lets the bucket arithmetic be tested at its exact boundaries
// instead of around a sleep.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func take(t *testing.T, s *MemoryStore, key string, rule Rule) Decision {
	t.Helper()
	d, err := s.Take(t.Context(), key, rule)
	require.NoError(t, err)
	return d
}

func TestMemoryStore_BurstThenShed(t *testing.T) {
	t.Parallel()

	clock := newClock()
	store := NewMemoryStore(WithClock(clock.Now))
	rule := Rule{Limit: 60, Burst: 5, Window: time.Minute}

	for i := range 5 {
		assert.True(t, take(t, store, "addr:1.2.3.4", rule).Allowed, "request %d is inside the burst", i)
	}

	shed := take(t, store, "addr:1.2.3.4", rule)
	assert.False(t, shed.Allowed)
	assert.Positive(t, shed.RetryAfter, "a shed decision must say when to come back")
}

func TestMemoryStore_KeysAreIndependent(t *testing.T) {
	t.Parallel()

	clock := newClock()
	store := NewMemoryStore(WithClock(clock.Now))
	rule := Rule{Limit: 60, Burst: 2, Window: time.Minute}

	require.True(t, take(t, store, "addr:1.1.1.1", rule).Allowed)
	require.True(t, take(t, store, "addr:1.1.1.1", rule).Allowed)
	require.False(t, take(t, store, "addr:1.1.1.1", rule).Allowed)

	assert.True(t, take(t, store, "addr:2.2.2.2", rule).Allowed,
		"one address exhausting its budget must not spend another's")
}

func TestMemoryStore_RefillsAtTheConfiguredRate(t *testing.T) {
	t.Parallel()

	clock := newClock()
	store := NewMemoryStore(WithClock(clock.Now))
	// 60 per minute is one per second.
	rule := Rule{Limit: 60, Burst: 2, Window: time.Minute}

	require.True(t, take(t, store, "k", rule).Allowed)
	require.True(t, take(t, store, "k", rule).Allowed)
	require.False(t, take(t, store, "k", rule).Allowed)

	clock.Advance(999 * time.Millisecond)
	assert.False(t, take(t, store, "k", rule).Allowed, "just short of a whole token")

	clock.Advance(time.Millisecond)
	assert.True(t, take(t, store, "k", rule).Allowed, "one second buys exactly one request")
}

func TestMemoryStore_RetryAfterMatchesTheRefill(t *testing.T) {
	t.Parallel()

	clock := newClock()
	store := NewMemoryStore(WithClock(clock.Now))
	rule := Rule{Limit: 30, Burst: 1, Window: time.Minute} // one per 2s

	require.True(t, take(t, store, "k", rule).Allowed)
	shed := take(t, store, "k", rule)
	require.False(t, shed.Allowed)
	assert.InDelta(t, 2*time.Second, shed.RetryAfter, float64(10*time.Millisecond))

	// And the wait it named is honest: waiting it out buys a request.
	clock.Advance(shed.RetryAfter)
	assert.True(t, take(t, store, "k", rule).Allowed)
}

func TestMemoryStore_RefillIsCappedAtTheBurst(t *testing.T) {
	t.Parallel()

	clock := newClock()
	store := NewMemoryStore(WithClock(clock.Now))
	rule := Rule{Limit: 60, Burst: 3, Window: time.Minute}

	require.True(t, take(t, store, "k", rule).Allowed)
	clock.Advance(time.Hour) // an hour of credit must not accumulate

	for i := range 3 {
		assert.True(t, take(t, store, "k", rule).Allowed, "request %d", i)
	}
	assert.False(t, take(t, store, "k", rule).Allowed,
		"an idle hour buys the burst, not an hour's worth of requests")
}

func TestMemoryStore_InactiveRuleAllowsEverything(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()

	for range 100 {
		assert.True(t, take(t, store, "k", Rule{}).Allowed)
	}
	assert.Zero(t, store.Len(), "an unconfigured rule must not even allocate a bucket")
}

// The limiter must not become the memory leak it exists to prevent: an attacker
// rotating source addresses creates one bucket each, and they have to go away.
func TestMemoryStore_SweepsBucketsThatHaveFullyRefilled(t *testing.T) {
	t.Parallel()

	clock := newClock()
	store := NewMemoryStore(WithClock(clock.Now), WithSweepEvery(time.Minute))
	rule := Rule{Limit: 60, Burst: 5, Window: time.Minute}

	for i := range 500 {
		take(t, store, string(rune('a'+i%26))+string(rune('a'+i/26)), rule)
	}
	require.Positive(t, store.Len())

	// Long enough for every bucket to be back at capacity, and past the sweep
	// interval. Dropping a full bucket is exactly equivalent to keeping it.
	clock.Advance(10 * time.Minute)
	take(t, store, "trigger", rule)

	assert.Equal(t, 1, store.Len(), "only the bucket charged by the sweeping call survives")
}

// Sweeping must never hand back budget that has not actually refilled.
func TestMemoryStore_SweepKeepsBucketsStillInDebt(t *testing.T) {
	t.Parallel()

	clock := newClock()
	// Sweep on every call, so the sweep is what the test is measuring rather
	// than the interval.
	store := NewMemoryStore(WithClock(clock.Now), WithSweepEvery(time.Nanosecond))
	// One per minute, burst 10: a drained bucket needs ten minutes.
	rule := Rule{Limit: 1, Burst: 10, Window: time.Minute}

	for range 10 {
		require.True(t, take(t, store, "victim", rule).Allowed)
	}
	require.False(t, take(t, store, "victim", rule).Allowed)

	clock.Advance(time.Second) // far short of even one token's refill
	take(t, store, "other", rule)

	shed := take(t, store, "victim", rule)
	assert.False(t, shed.Allowed, "a swept-away bucket would have handed the budget straight back")
}

// One store serves both budgets at once (per address, per principal), with
// different rules. The sweep must not collect a slow-refilling principal bucket
// using a fast-refilling address rule's arithmetic.
func TestMemoryStore_MixedRules_SweepUsesEachBucketsOwnRefill(t *testing.T) {
	t.Parallel()

	clock := newClock()
	store := NewMemoryStore(WithClock(clock.Now), WithSweepEvery(time.Minute))
	fast := Rule{Limit: 600, Burst: 1, Window: time.Minute}     // refills in 100ms
	slow := Rule{Limit: 1, Burst: 10, Window: 10 * time.Minute} // refills in 100 minutes

	for range 10 {
		require.True(t, take(t, store, "principal:u1", slow).Allowed)
	}
	require.False(t, take(t, store, "principal:u1", slow).Allowed)

	clock.Advance(2 * time.Minute)
	take(t, store, "addr:1.1.1.1", fast) // triggers the sweep, with the FAST rule

	assert.False(t, take(t, store, "principal:u1", slow).Allowed,
		"the slow bucket must survive a sweep driven by a fast rule")
}

// Two concurrent calls must not both spend the last token.
func TestMemoryStore_ConcurrentTakes_SpendExactlyTheBudget(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	rule := Rule{Limit: 1, Burst: 20, Window: time.Hour} // refill is irrelevant here

	const callers = 200
	var allowed, start sync.WaitGroup
	var mu sync.Mutex
	count := 0

	start.Add(1)
	allowed.Add(callers)
	for range callers {
		go func() {
			defer allowed.Done()
			start.Wait()
			d, err := store.Take(t.Context(), "k", rule)
			if err == nil && d.Allowed {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}()
	}
	start.Done()
	allowed.Wait()

	assert.Equal(t, 20, count, "the budget is the budget, however many callers arrive at once")
}
