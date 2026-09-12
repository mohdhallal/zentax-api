package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

// MemoryStore is the single-process Store: one token bucket per key, held in a
// mutex-guarded map.
//
// WHAT A SHARED (REDIS) IMPLEMENTATION WOULD HAVE TO DO to replace it:
//
//  1. Make the read-refill-write of one bucket ATOMIC across processes. In
//     Redis that is a small Lua script (or a Function) taking key, capacity,
//     refill-rate and now, doing exactly the arithmetic in Take below on a
//     hash of {tokens, last}, and returning allowed + retry-after. A round trip
//     that reads and then writes is a lost update under concurrency — which is
//     the bypass this whole package exists to close.
//  2. Set a TTL on every key (the time the bucket needs to refill completely is
//     enough — a full bucket is indistinguishable from a missing one) so the
//     keyspace is self-pruning; that replaces the sweep below.
//  3. Take a clock decision. Redis TIME inside the script gives every process
//     one clock and avoids drift between tasks; passing the caller's now is
//     cheaper but makes a skewed task's charges wrong.
//  4. Fail OPEN, loudly, and fast: give the client a short timeout (a few
//     milliseconds — it sits in front of every request) and return the error
//     rather than a decision. The middleware logs it and lets the request
//     through, because a limiter outage must not become an API outage. The Gate
//     is unaffected: it is in-process and keeps capping memory regardless.
//  5. Keep the key shape as it is here (the "addr:"/"principal:" prefixes), so
//     both implementations can run side by side during a migration.
//
// Note what a per-process store means today: with N API tasks behind the load
// balancer the effective budget is N times the configured one, and it moves
// when the service scales. That is acceptable for a bound whose purpose is to
// stop exhaustion — each task still protects its own memory, and the Gate, the
// valve that actually caps memory, is per-process by nature — and it is the
// reason the seam exists.
type MemoryStore struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	// now is injectable so the tests can advance time instead of sleeping.
	now func() time.Time

	// maxKeys and sweepEvery bound the map: an attacker rotating source
	// addresses would otherwise turn the limiter itself into the memory leak.
	maxKeys    int
	sweepEvery time.Duration
	lastSweep  time.Time
}

// bucket carries the instant it will be back at capacity as well as its level,
// so the sweep can collect it without knowing which rule charged it — one store
// serves several rules (per address, per principal) at once.
type bucket struct {
	tokens float64
	last   time.Time
	full   time.Time
}

const (
	defaultMaxKeys    = 50_000
	defaultSweepEvery = time.Minute
)

// MemoryOption configures a MemoryStore.
type MemoryOption func(*MemoryStore)

// WithClock replaces the store's clock (tests).
func WithClock(now func() time.Time) MemoryOption {
	return func(s *MemoryStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithMaxKeys caps how many buckets are held before a sweep is forced.
func WithMaxKeys(n int) MemoryOption {
	return func(s *MemoryStore) {
		if n > 0 {
			s.maxKeys = n
		}
	}
}

// WithSweepEvery sets how often idle buckets are collected.
func WithSweepEvery(d time.Duration) MemoryOption {
	return func(s *MemoryStore) {
		if d > 0 {
			s.sweepEvery = d
		}
	}
}

// NewMemoryStore builds an in-memory Store. It owns no goroutine: collection is
// folded into Take, so the store needs no lifecycle and cannot leak one.
func NewMemoryStore(opts ...MemoryOption) *MemoryStore {
	s := &MemoryStore{
		buckets:    make(map[string]*bucket),
		now:        time.Now,
		maxKeys:    defaultMaxKeys,
		sweepEvery: defaultSweepEvery,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.lastSweep = s.now()
	return s
}

// Take charges one request against key. A rule that is not Active() allows
// everything (nothing is configured, so nothing is limited).
func (s *MemoryStore) Take(_ context.Context, key string, rule Rule) (Decision, error) {
	if !rule.Active() {
		return Decision{Allowed: true}, nil
	}

	now := s.now()
	capacity := rule.capacity()
	rate := rule.refillPerSecond()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.maybeSweep(now)

	b, ok := s.buckets[key]
	if !ok {
		b = &bucket{tokens: capacity, last: now}
		s.buckets[key] = b
	} else if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(capacity, b.tokens+elapsed.Seconds()*rate)
		b.last = now
	}

	var decision Decision
	if b.tokens >= 1 {
		b.tokens--
		decision = Decision{Allowed: true, Remaining: int(b.tokens)}
	} else {
		// Empty: how long until one whole token has refilled.
		decision = Decision{
			Allowed:    false,
			RetryAfter: seconds((1 - b.tokens) / rate),
		}
	}
	b.full = now.Add(seconds((capacity - b.tokens) / rate))

	return decision, nil
}

// Len reports how many buckets are held (tests, and a cheap operational probe).
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buckets)
}

// maybeSweep drops every bucket that has refilled to capacity: a missing bucket
// is created full, so dropping a full one is exactly equivalent to keeping it,
// and it can never hand anyone budget they had not already recovered. Called
// under the lock, and only once the interval has passed or the map has grown
// past maxKeys, so the common path stays a map lookup.
func (s *MemoryStore) maybeSweep(now time.Time) {
	if len(s.buckets) < s.maxKeys && now.Sub(s.lastSweep) < s.sweepEvery {
		return
	}
	s.lastSweep = now

	for key, b := range s.buckets {
		if !now.Before(b.full) {
			delete(s.buckets, key)
		}
	}
}

func seconds(f float64) time.Duration {
	if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}
