package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

// reached is the handler under the middlewares: it records that the request got
// through, which is the only thing these tests need from it.
func reached(hit *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Add(1)
		w.WriteHeader(http.StatusOK)
	})
}

// chain wires the global Provide middleware in front of a per-route one, which
// is exactly how bootstrap and the route builder compose them.
func chain(l *Limiter, route types.Middleware, h http.Handler) http.Handler {
	return Provide(l)(route(h))
}

func call(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

func loginRequest(remoteAddr, forwarded string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/auth/login", http.NoBody)
	r.RemoteAddr = remoteAddr
	if forwarded != "" {
		r.Header.Set(DefaultForwardedHeader, forwarded)
	}
	return r
}

func testLimiter(t *testing.T, settings Settings, opts ...MemoryOption) *Limiter {
	t.Helper()
	res, err := NewAddressResolver([]string{"127.0.0.0/8", "10.0.0.0/8"}, "")
	require.NoError(t, err)
	return New(NewMemoryStore(opts...), NewGate(1, 0, time.Millisecond), res, settings)
}

func TestByAddress_ShedsPastTheBudget(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{Anonymous: Rule{Limit: 60, Burst: 2, Window: time.Minute}})
	h := chain(limiter, ByAddress("/auth/login"), reached(&hits))

	require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)
	require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)

	shed := call(h, loginRequest("10.0.0.1:1", "198.51.100.5"))
	assert.Equal(t, http.StatusTooManyRequests, shed.Code)
	assert.EqualValues(t, 2, hits.Load(), "a shed request must never reach the handler")
}

// The shed answer must be the standard envelope, with a usable Retry-After —
// this is a contract the Express adapter and the browser client both read.
func TestShed_ResponseShape(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{Anonymous: Rule{Limit: 60, Burst: 1, Window: time.Minute}})
	h := chain(limiter, ByAddress("/auth/login"), reached(&hits))

	require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)
	shed := call(h, loginRequest("10.0.0.1:1", "198.51.100.5"))

	require.Equal(t, http.StatusTooManyRequests, shed.Code)
	assert.Equal(t, "application/json", shed.Header().Get("Content-Type"))

	retryAfter := shed.Header().Get("Retry-After")
	require.NotEmpty(t, retryAfter, "a shed request must be told when to come back")
	secs, err := strconv.Atoi(retryAfter)
	require.NoError(t, err, "Retry-After must be an integer number of seconds")
	assert.GreaterOrEqual(t, secs, 1, "Retry-After 0 reads as 'retry immediately'")

	var body struct {
		Status bool `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(shed.Body.Bytes(), &body))
	assert.False(t, body.Status)
	assert.Equal(t, string(CodeTooManyRequests), body.Error.Code)
	assert.Equal(t, MsgTooManyRequests, body.Error.Message)
	assert.NotContains(t, body.Error.Message, "address",
		"the message must not say WHICH budget was hit — that is a map of how to evade it")
}

// The address is the key, and it comes from the forwarded header only when the
// socket peer is a trusted proxy. A forged header must not mint a new budget.
func TestByAddress_KeyIsTheResolvedClientAddress(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{Anonymous: Rule{Limit: 60, Burst: 1, Window: time.Minute}})
	h := chain(limiter, ByAddress("/auth/login"), reached(&hits))

	require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)

	// Same real client, a different forgery in front of it: still shed.
	assert.Equal(t, http.StatusTooManyRequests,
		call(h, loginRequest("10.0.0.1:1", "203.0.113.9, 198.51.100.5")).Code)

	// A genuinely different client still gets its own budget.
	assert.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.6")).Code)
}

func TestByAddress_ExemptPathIsNeverCharged(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{
		Anonymous: Rule{Limit: 60, Burst: 1, Window: time.Minute},
		Exempt:    []string{"/health/ready"},
	})
	h := chain(limiter, ByAddress("/health/ready"), reached(&hits))

	for i := range 20 {
		require.Equal(t, http.StatusOK,
			call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code, "probe %d", i)
	}
	assert.EqualValues(t, 20, hits.Load(),
		"shedding a liveness probe is how a limiter talks the orchestrator into killing a healthy task")
}

func TestByAddress_NoLimiterInContext_PassesThrough(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	h := ByAddress("/auth/login")(reached(&hits)) // no Provide

	for range 50 {
		require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)
	}
	assert.EqualValues(t, 50, hits.Load())
}

func TestByAddress_UnconfiguredRule_PassesThrough(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{})
	h := chain(limiter, ByAddress("/auth/login"), reached(&hits))

	for range 50 {
		require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)
	}
}

func authedRequest(principal, remoteAddr, forwarded string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/entities", http.NoBody)
	r.RemoteAddr = remoteAddr
	if forwarded != "" {
		r.Header.Set(DefaultForwardedHeader, forwarded)
	}
	if principal != "" {
		ctx := app.WithRequester(r.Context(), &app.Requester{Kind: app.RequesterUser, ID: principal})
		r = r.WithContext(ctx)
	}
	return r
}

// THE per-principal property: the budget follows the caller, not the socket.
// Keying an authenticated route by address would put a whole corporate NAT into
// one budget, and one user's several sessions into one as well.
func TestByPrincipal_BudgetFollowsThePrincipalNotTheAddress(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{Authenticated: Rule{Limit: 60, Burst: 2, Window: time.Minute}})
	h := chain(limiter, ByPrincipal("/entities"), reached(&hits))

	require.Equal(t, http.StatusOK, call(h, authedRequest("user-a", "10.0.0.1:1", "198.51.100.5")).Code)
	require.Equal(t, http.StatusOK, call(h, authedRequest("user-a", "10.0.0.1:1", "198.51.100.5")).Code)

	// Spent — and moving to another address does not refill it.
	assert.Equal(t, http.StatusTooManyRequests,
		call(h, authedRequest("user-a", "10.0.0.1:1", "203.0.113.44")).Code,
		"a per-principal budget an attacker escapes by changing address is no budget")

	// A different principal on the SAME address is unaffected.
	assert.Equal(t, http.StatusOK,
		call(h, authedRequest("user-b", "10.0.0.1:1", "198.51.100.5")).Code,
		"one user's burst must not shed their colleague behind the same NAT")
}

// A request that somehow reaches an authenticated route with no principal must
// still be charged something rather than being left unlimited.
func TestByPrincipal_NoPrincipal_FallsBackToTheAddress(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{Authenticated: Rule{Limit: 60, Burst: 1, Window: time.Minute}})
	h := chain(limiter, ByPrincipal("/entities"), reached(&hits))

	require.Equal(t, http.StatusOK, call(h, authedRequest("", "10.0.0.1:1", "198.51.100.5")).Code)
	assert.Equal(t, http.StatusTooManyRequests,
		call(h, authedRequest("", "10.0.0.1:1", "198.51.100.5")).Code)
}

func TestVerification_OnlyGatesTheConfiguredPaths(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	limiter := testLimiter(t, Settings{Verify: []string{"/auth/login"}})

	assert.True(t, limiter.GatesPath("/auth/login"))
	assert.False(t, limiter.GatesPath("/entities"))

	ungated := chain(limiter, Verification("/entities"), reached(&hits))
	for range 20 {
		require.Equal(t, http.StatusOK, call(ungated, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)
	}
}

// The gate's slot must be held for the handler's whole run and released after.
func TestVerification_HoldsAndReleasesASlot(t *testing.T) {
	t.Parallel()

	limiter := testLimiter(t, Settings{Verify: []string{"/auth/login"}})

	var inside atomic.Int64
	release := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		inside.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := chain(limiter, Verification("/auth/login"), slow)

	done := make(chan int, 1)
	go func() { done <- call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code }()
	require.Eventually(t, func() bool { return inside.Load() == 1 }, time.Second, time.Millisecond)

	// The single slot is held, so the second request is shed rather than run.
	shed := call(h, loginRequest("10.0.0.1:1", "198.51.100.6"))
	assert.Equal(t, http.StatusTooManyRequests, shed.Code)
	assert.NotEmpty(t, shed.Header().Get("Retry-After"))
	assert.EqualValues(t, 1, inside.Load(), "the shed request must not have run the verification")

	close(release)
	assert.Equal(t, http.StatusOK, <-done)

	// The slot came back.
	assert.Eventually(t, func() bool { return limiter.gate.InFlight() == 0 }, time.Second, time.Millisecond)
}

// The bound is per gate, not per address: an attacker spread across a thousand
// source addresses buys no extra argon2 memory. This is the property the rate
// budget on its own cannot give.
func TestVerification_BoundHoldsAcrossDistinctAddresses(t *testing.T) {
	t.Parallel()

	res, err := NewAddressResolver([]string{"10.0.0.0/8"}, "")
	require.NoError(t, err)
	limiter := New(
		NewMemoryStore(), NewGate(2, 4, 20*time.Millisecond), res,
		Settings{
			// A budget so large it cannot be what sheds anything here.
			Anonymous: Rule{Limit: 100_000, Burst: 100_000, Window: time.Minute},
			Verify:    []string{"/auth/login"},
		},
	)

	var peak, inFlight atomic.Int64
	busy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := inFlight.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond) // stand in for argon2
		inFlight.Add(-1)
		w.WriteHeader(http.StatusOK)
	})
	h := Provide(limiter)(ByAddress("/auth/login")(Verification("/auth/login")(busy)))

	var wg sync.WaitGroup
	var shed atomic.Int64
	for i := range 60 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Every request from its own address, so the rate budget is never
			// the thing that refuses one.
			addr := "198.51.100." + strconv.Itoa(i%250+1)
			if call(h, loginRequest("10.0.0.1:1", addr)).Code == http.StatusTooManyRequests {
				shed.Add(1)
			}
		}(i)
	}
	wg.Wait()

	assert.LessOrEqual(t, peak.Load(), int64(2),
		"concurrency is bounded by the gate, whatever the source addresses")
	assert.Positive(t, shed.Load(), "the excess must be shed, not queued without limit")
}

// errStore stands in for a shared (Redis) store that is down.
type errStore struct{}

func (errStore) Take(context.Context, string, Rule) (Decision, error) {
	return Decision{}, errors.New("redis: connection refused")
}

// A limiter outage must not become an API outage — the Gate keeps capping
// memory either way, which is the bound that actually matters.
func TestCharge_StoreFailure_FailsOpen(t *testing.T) {
	t.Parallel()

	res, err := NewAddressResolver([]string{"10.0.0.0/8"}, "")
	require.NoError(t, err)
	limiter := New(errStore{}, NewGate(1, 0, time.Millisecond), res,
		Settings{Anonymous: Rule{Limit: 1, Burst: 1, Window: time.Minute}})

	var hits atomic.Int64
	h := chain(limiter, ByAddress("/auth/login"), reached(&hits))

	for range 10 {
		require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)
	}
	assert.EqualValues(t, 10, hits.Load())
}

func TestProvide_NilLimiterIsAPassThrough(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	h := Provide(nil)(ByAddress("/auth/login")(reached(&hits)))

	require.Equal(t, http.StatusOK, call(h, loginRequest("10.0.0.1:1", "198.51.100.5")).Code)
	assert.Nil(t, FromContext(context.Background()))
}
