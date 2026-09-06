package identity_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// poolMax mirrors acceptance/config.DefaultConfig's Database.PoolMax — the
// harness deliberately runs the API on a SMALL pool so connection contention is
// reproducible in a test rather than a production surprise.
const poolMax = 5

// burstAttempt is one request of a burst: the password it submits, and an
// optional gate it waits on after the starting gun before it is issued. The gate
// is nil for the ordinary case (everything at once); it exists for the tests
// that have to place a guess deterministically AFTER the burst has spent the
// account's attempt budget.
type burstAttempt struct {
	password string
	wait     func()
}

// burstResult is what a burst reports back: the wall time of the whole burst,
// and per request its status, its own duration and whether it was handed a
// session cookie.
type burstResult struct {
	elapsed   time.Duration
	statuses  []int
	durations []time.Duration
	sessions  []string
}

// sessionsMinted counts the requests that came back with a session cookie —
// which is to say, the requests whose STORED hash was verified and matched.
func (r burstResult) sessionsMinted() int {
	n := 0
	for _, token := range r.sessions {
		if token != "" {
			n++
		}
	}
	return n
}

// slowest is the longest single request in the burst. A pool stall shows up
// here (~5s, the connection timeout) even when the burst as a whole finishes.
func (r burstResult) slowest() time.Duration {
	var max time.Duration
	for _, d := range r.durations {
		if d > max {
			max = d
		}
	}
	return max
}

// loginBurst fires n identical logins at once and returns the wall time and the
// status codes — the shape the round-2 pool-boundary test needs.
func (s *IdentitySuite) loginBurst(email, password string, n int) (time.Duration, []int) {
	s.T().Helper()

	attempts := make([]burstAttempt, n)
	for i := range attempts {
		attempts[i] = burstAttempt{password: password}
	}
	res := s.loginBurstEach(email, attempts)
	return res.elapsed, res.statuses
}

// loginBurstEach fires one login per attempt, all released from one starting
// gun (an attempt with a non-zero `after` waits that long past the gun).
//
// It does not use the suite's RequestBuilder: that fails the test from inside
// the calling goroutine (require.NoError → t.FailNow), which is illegal off the
// test goroutine. Errors are collected and asserted by the caller instead.
func (s *IdentitySuite) loginBurstEach(email string, attempts []burstAttempt) burstResult {
	s.T().Helper()

	n := len(attempts)
	bodies := make([][]byte, n)
	for i, attempt := range attempts {
		body, err := json.Marshal(map[string]any{"email": email, "password": attempt.password})
		s.Require().NoError(err)
		bodies[i] = body
	}

	// One client, its own transport: n concurrent requests must open n
	// connections rather than queue on a shared keep-alive pool, or the test
	// would serialise itself and never reach the API's connection pool.
	client := &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{MaxIdleConnsPerHost: n, MaxConnsPerHost: n},
	}
	defer client.CloseIdleConnections()

	reqCtx := s.T().Context()
	res := burstResult{
		statuses:  make([]int, n),
		durations: make([]time.Duration, n),
		sessions:  make([]string, n),
	}
	errs := make([]error, n)
	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(reqCtx,
				http.MethodPost, s.External.URL+"/auth/login", bytes.NewReader(bodies[i]))
			if err != nil {
				errs[i] = err
				return
			}
			req.Header.Set("Content-Type", "application/json")
			<-start
			if gate := attempts[i].wait; gate != nil {
				gate()
			}
			issued := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				errs[i] = err
				return
			}
			res.durations[i] = time.Since(issued)
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			res.statuses[i] = resp.StatusCode
			for _, c := range resp.Cookies() {
				if c.Name == cookieName {
					res.sessions[i] = c.Value
				}
			}
		}(i)
	}

	began := time.Now()
	close(start)
	wg.Wait()
	res.elapsed = time.Since(began)

	for i, err := range errs {
		s.Require().NoError(err, "request %d failed at the transport level", i)
	}
	return res
}

// burstBudget is what a burst of wrong passwords may take. The regression it
// guards is a 5-SECOND stall: the first fix recorded the counter on a SECOND
// pooled connection borrowed while the request transaction still held the
// first, so a burst at or above PoolMax deadlocked until the write's own 5s
// timeout fired — every attempt after the pool ran dry was swallowed AND the
// whole API stopped answering. Measured then: 4 requests → 100ms, 5 → 5.1s,
// 7 → 5.2s. A burst that holds one connection at a time cannot approach this.
const burstBudget = 2 * time.Second

// TestFailedLoginLockout_Concurrent is the regression test for that stall, and
// for the bypass it left behind: an attacker who simply guesses in PARALLEL
// outran the counter (5 concurrent wrong passwords recorded 1, 6 recorded 3),
// so the lockout never engaged however many attempts were made.
//
// Every attempt must be counted no matter how many arrive at once, the counter
// saturating at the threshold, and the burst must never queue on the pool.
func (s *IdentitySuite) TestFailedLoginLockout_Concurrent() {
	tenant := s.InsertTenant("auth-burst", "Auth Tenant Burst")

	cases := []struct {
		name string
		n    int
	}{
		{"below the pool", poolMax - 1},
		{"exactly the pool", poolMax},
		{"above the pool", poolMax + 2},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			email := "burst-" + uuid.NewString() + "@acme.com"
			userID := s.InsertUserWithPassword(tenant, email, "s3cret-password")

			elapsed, statuses := s.loginBurst(email, "not-my-password", tc.n)

			for i, code := range statuses {
				s.Require().Equal(http.StatusUnauthorized, code, "request %d", i)
			}

			attempts, lockedUntil := s.failedLoginState(userID)
			want := min(tc.n, maxFailedAttempts)
			s.T().Logf("n=%d wall=%s counter=%d", tc.n, elapsed.Round(time.Millisecond), attempts)

			s.Require().Equal(want, attempts,
				"every concurrent attempt must be counted (saturating at the threshold): "+
					"a lockout outrun by parallel guessing is no lockout")
			s.Require().Less(elapsed, burstBudget,
				"a login burst must never queue on the connection pool")

			if tc.n >= maxFailedAttempts {
				s.Require().NotNil(lockedUntil, "the threshold must lock the account")
			} else {
				s.Require().Nil(lockedUntil, "no lock below the threshold")
			}
		})
	}
}

// budgetBurst is the size of the concurrency the budget has to hold against:
// well above the threshold (5) and well above PoolMax (5), so neither the
// policy nor the pool can be what limits it.
const budgetBurst = 40

// stallCeiling is a per-REQUEST bound, not a throughput one. 40 concurrent
// argon2 verifications on a shared machine legitimately take a while in
// aggregate; what must never happen is a single request sitting on a connection
// it cannot get (the pool's own timeout is 5s, and the round-1 defect showed up
// as exactly that).
const stallCeiling = 3 * time.Second

// guessDelay is how long a correct-password guess waits after the starting gun
// before it is issued, so that it lands INSIDE the burst's window.
//
// It has to sit between a measured floor and a measured ceiling, and the test
// checks it landed there rather than trusting the number.
// FLOOR — the burst's charges must already have committed. Each is one small
// UPDATE and no request holds a connection while it hashes, so the fifth commits
// in a few milliseconds; the test observes the account at issue time and asserts
// the budget was gone.
// CEILING — the burst must still be IN FLIGHT, earlier than its first argon2
// verification can finish, or a build carrying the defect would refuse the guess
// by accident and the test would pass for the wrong reason. Measured against a
// deliberately reintroduced defect: at this delay nothing had completed, the
// guess was verified against the stored hash and logged straight in. The burst
// as a whole takes ~800-930ms, so there is better than an order of magnitude of
// room. Note that CPU saturation stretches both ends together — forty-five
// concurrent argon2 hashes on ten cores delay the guess and the completions
// alike — which is why a fixed delay is stable here and a polling gate was not.
const guessDelay = 50 * time.Millisecond

// budgetProbe reads the lockout columns without failing the test: it runs off
// the test goroutine, where s.Require() is illegal.
func (s *IdentitySuite) budgetProbe(userID uuid.UUID) (int, bool) {
	var attempts int
	var lockedUntil *time.Time
	if err := s.DB.QueryRowx(
		`SELECT failed_login_attempts, locked_until FROM users WHERE id = $1`, userID,
	).Scan(&attempts, &lockedUntil); err != nil {
		return -1, false
	}
	return attempts, lockedUntil != nil
}

// TestLoginBudget_ConcurrencyCannotWidenIt is the regression test for a lockout
// that throttled only SERIAL guessing.
//
// The defect: `locked` was decided from the user row read at the TOP of the
// request, while the attempt was counted AFTER the request transaction closed.
// Every request that started before the threshold increment committed therefore
// read an unlocked account and verified the STORED hash. A single concurrent
// burst bought as many real verifications as the attacker had concurrency — the
// five-guess budget bounded only an attacker who waited for each answer.
//
// The fix charges an attempt ATOMICALLY, in one statement evaluated under the
// row lock, and commits it BEFORE verifying anything. Whatever arrives at once
// now serialises on that statement, so the budget is the budget.
//
// Verifications of the STORED hash are counted by their only observable
// consequence: a session. A stored-hash verification that returns true mints
// one; a dummy verification never can. Which is why the guesses below carry the
// RIGHT password — the point is not that they are refused, it is that the
// credential was never checked.
func (s *IdentitySuite) TestLoginBudget_ConcurrencyCannotWidenIt() {
	tenant := s.InsertTenant("auth-budget", "Auth Tenant Budget")
	const password = "s3cret-password"

	// THE BLOCKER. A burst of wrong passwords with a COHORT of correct guesses
	// inside it, each released as soon as the burst has spent the budget — while
	// the burst itself is still hashing, which is the whole window the defect
	// lived in. Not one of them may reach the stored hash.
	//
	// A cohort rather than a single guess because the number is the measurement:
	// before the fix every guess in the window was verified and logged in, so
	// this is `guessCohort` sessions against zero, not a coin flip.
	s.Run("no guess in the burst's window reaches the stored hash", func() {
		const guessCohort = 5

		email := "budget-guess-" + uuid.NewString() + "@acme.com"
		userID := s.InsertUserWithPassword(tenant, email, password)

		attempts := make([]burstAttempt, budgetBurst)
		for i := range attempts {
			attempts[i] = burstAttempt{password: "not-my-password"}
		}
		// One of the guesses observes the account at the moment the cohort is
		// issued; the rest just wait. That observation is what makes the
		// measurement checkable instead of assumed.
		atIssue, lockedAtIssue := 0, false
		observe := func() {
			time.Sleep(guessDelay)
			atIssue, lockedAtIssue = s.budgetProbe(userID)
		}
		attempts = append(attempts, burstAttempt{password: password, wait: observe})
		for range guessCohort - 1 {
			attempts = append(attempts,
				burstAttempt{password: password, wait: func() { time.Sleep(guessDelay) }})
		}
		res := s.loginBurstEach(email, attempts)

		counter, lockedUntil := s.failedLoginState(userID)
		s.T().Logf("n=%d guesses=%d atIssue=(counter=%d locked=%v) wall=%s slowest=%s "+
			"sessions=%d counter=%d locked=%v",
			len(attempts), guessCohort, atIssue, lockedAtIssue,
			res.elapsed.Round(time.Millisecond), res.slowest().Round(time.Millisecond),
			res.sessionsMinted(), counter, lockedUntil != nil)

		for i, code := range res.statuses {
			s.Require().Equal(http.StatusUnauthorized, code, "request %d", i)
		}
		s.Require().Zero(res.sessionsMinted(),
			"once the burst has spent the budget, no request may verify the stored hash — "+
				"a budget an attacker widens by adding threads is no budget")
		s.Require().Equal(maxFailedAttempts, counter,
			"the budget is exactly the threshold, however many requests arrive at once")
		s.Require().NotNil(lockedUntil, "spending the budget must lock the account")
		s.Require().Less(res.slowest(), stallCeiling,
			"no request may stall on the connection pool")

		// ...and the guesses really were issued into the burst's window: the
		// budget was already gone when they went out, so refusing them is the
		// budget doing its job and not a coincidence of timing.
		s.Require().True(lockedAtIssue,
			"the burst must have spent the budget before the guesses were issued (counter was %d)",
			atIssue)
	})

	// The budget must not widen with concurrency in the other direction either:
	// a burst far above both the threshold and the pool still counts exactly the
	// threshold, and no request in it waits on a connection. (The round-2 test
	// pins the same property right at the pool boundary, where the stall was.)
	s.Run("a burst far above the pool still counts exactly the threshold", func() {
		email := "budget-count-" + uuid.NewString() + "@acme.com"
		userID := s.InsertUserWithPassword(tenant, email, password)

		attempts := make([]burstAttempt, budgetBurst)
		for i := range attempts {
			attempts[i] = burstAttempt{password: "not-my-password"}
		}
		res := s.loginBurstEach(email, attempts)

		counter, lockedUntil := s.failedLoginState(userID)
		s.T().Logf("n=%d wall=%s slowest=%s counter=%d locked=%v",
			budgetBurst, res.elapsed.Round(time.Millisecond), res.slowest().Round(time.Millisecond),
			counter, lockedUntil != nil)

		for i, code := range res.statuses {
			s.Require().Equal(http.StatusUnauthorized, code, "request %d", i)
		}
		s.Require().Equal(maxFailedAttempts, counter)
		s.Require().NotNil(lockedUntil)
		s.Require().Less(res.slowest(), stallCeiling,
			"no request may stall on the connection pool")
	})

	// The other half of the policy, under the same concurrency: an account whose
	// budget is intact still lets the right password through, and a success
	// clears what it spent. Charging before verifying must not cost a legitimate
	// login — the surplus of a burst of SIMULTANEOUS correct logins is refused
	// (the documented cost), but the account must not end up locked or in debt.
	s.Run("a correct password still logs in and clears what it spent", func() {
		email := "budget-ok-" + uuid.NewString() + "@acme.com"
		userID := s.InsertUserWithPassword(tenant, email, password)

		login := s.Client.External().POST(s.T(), "/auth/login",
			map[string]any{"email": email, "password": password})
		login.AssertStatus(s.T(), http.StatusOK)
		s.Require().NotEmpty(sessionCookie(login))

		counter, lockedUntil := s.failedLoginState(userID)
		s.Require().Equal(0, counter, "a successful login clears the attempt it spent")
		s.Require().Nil(lockedUntil)
	})
}
