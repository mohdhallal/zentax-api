// Package ratelimit holds the API's two shed valves and the HTTP middlewares
// that wire them.
//
// WHAT IT DEFENDS. Password verification is argon2id at 64 MiB, t=3, p=2
// (platform/crypto.VerifyPassword), and the login use case deliberately
// verifies a DUMMY hash when the address is unknown, so the full memory cost is
// paid before an account is even identified — an anonymous caller can buy
// 64 MiB of the task's heap per in-flight request. The SaaS API task is
// provisioned at 0.5 vCPU / 1 GiB, so a couple of dozen concurrent
// unauthenticated requests exhaust it and the orchestrator hard-kills the
// container. Nothing in the tree produced a 429 before this package.
//
// TWO VALVES, because they bound different things:
//
//   - Gate — a bound on how many expensive verifications run AT ONCE, which is
//     what actually caps peak memory. Requests over the bound wait briefly and
//     are then shed; the number that may wait is itself bounded, so the queue
//     cannot grow without limit either.
//   - Store + Rule — a request RATE budget: per client address on the
//     unauthenticated routes, per principal on the authenticated ones.
//
// A rate budget alone cannot cap memory (60 requests spread over a second still
// arrive together), and a concurrency bound alone cannot stop a patient
// attacker from spending the whole budget of a real account. Both are needed.
//
// Both are shed the same way: 429 with a Retry-After header and the standard
// error envelope, logged as a client error — never as a server error.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Rule is a token-bucket budget: Limit requests per Window, with Burst tokens
// of headroom for traffic that arrives in clumps (a page load firing a handful
// of requests at once must not be shed). Burst is the bucket's capacity; the
// bucket refills at Limit/Window.
type Rule struct {
	Limit  int
	Burst  int
	Window time.Duration
}

// Active reports whether the rule is a real budget. A zero rule means "not
// configured": the middleware then passes the request through rather than
// inventing a limit of its own.
func (r Rule) Active() bool {
	return r.Limit > 0 && r.Window > 0
}

// capacity is the bucket size — Burst when set, otherwise one window's worth.
func (r Rule) capacity() float64 {
	if r.Burst > 0 {
		return float64(r.Burst)
	}
	return float64(r.Limit)
}

// refillPerSecond is the sustained rate the bucket recovers at.
func (r Rule) refillPerSecond() float64 {
	return float64(r.Limit) / r.Window.Seconds()
}

// Decision is the store's answer for one charged request.
type Decision struct {
	Allowed bool
	// Remaining is the whole tokens left in the bucket after the charge.
	Remaining int
	// RetryAfter is how long the caller must wait for one token to refill.
	// Meaningful only when Allowed is false.
	RetryAfter time.Duration
}

// Store is the rate-limit counter backend.
//
// It is an interface for one reason: the in-memory implementation below is
// correct for a single process and is what ships today, but the SaaS cell runs
// several API tasks behind one load balancer, where a per-process budget is
// really "the limit times the task count" and moves whenever the service scales.
// A Redis (or DynamoDB) implementation drops in behind this same seam — see the
// package README notes on MemoryStore for exactly what it has to do.
//
// Take charges ONE request against key under rule and answers whether it is
// allowed. It must be atomic per key: two concurrent calls may not both spend
// the last token.
type Store interface {
	Take(ctx context.Context, key string, rule Rule) (Decision, error)
}

// Settings is the per-application policy the Limiter applies.
type Settings struct {
	// Anonymous is the budget charged per client address on routes that
	// authenticate no principal.
	Anonymous Rule
	// Authenticated is the budget charged per principal on tenant routes.
	Authenticated Rule
	// Verify lists the full route paths whose handler must hold a Gate slot —
	// the ones that perform a password or token verification.
	Verify []string
	// Exempt lists full route paths no rate budget is charged for. The health
	// probes belong here: shedding a liveness check is how a rate limiter
	// convinces the orchestrator to kill a perfectly healthy task.
	Exempt []string
}

// Limiter bundles the policy with the store, the gate and the address resolver.
// It is per-application state (the acceptance suite runs several apps in one
// process), so it travels to the route middlewares through the request context
// rather than through a package-level variable — see Provide.
type Limiter struct {
	store    Store
	gate     *Gate
	resolver *AddressResolver
	settings Settings
	verify   map[string]struct{}
	exempt   map[string]struct{}

	// sharedAddressOnce keeps the "your proxy is not appending" warning to one
	// line per process: it is a fact about the deployment, not about a request.
	sharedAddressOnce sync.Once
}

// New builds a Limiter. A nil store or resolver makes every rate budget a
// pass-through; a nil gate makes the verification bound a pass-through.
func New(store Store, gate *Gate, resolver *AddressResolver, settings Settings) *Limiter {
	return &Limiter{
		store:    store,
		gate:     gate,
		resolver: resolver,
		settings: settings,
		verify:   pathSet(settings.Verify),
		exempt:   pathSet(settings.Exempt),
	}
}

// GatesPath reports whether the route at path must hold a verification slot.
func (l *Limiter) GatesPath(path string) bool {
	if l == nil || l.gate == nil {
		return false
	}
	_, ok := l.verify[path]
	return ok
}

// ExemptsPath reports whether the route at path is outside every rate budget.
func (l *Limiter) ExemptsPath(path string) bool {
	if l == nil {
		return false
	}
	_, ok := l.exempt[path]
	return ok
}

func pathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if p != "" {
			set[p] = struct{}{}
		}
	}
	return set
}
