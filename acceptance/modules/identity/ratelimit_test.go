package identity_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	acceptconfig "github.com/mohamadhallal/zentax-api/acceptance/config"
	"github.com/mohamadhallal/zentax-api/bootstrap"
	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// RateLimitSuite exercises the shed valves end to end against the real stack.
//
// It stands up its OWN API server rather than using the suite's, because the
// shared harness deliberately runs with rate limiting off: several existing
// acceptance tests fire deliberate bursts (TestLoginBudget_ConcurrencyCannotWidenIt
// sends 45 simultaneous logins and requires every one of them to be answered
// 401), and those are measuring the lockout, not the limiter. Each test here
// therefore builds a config with exactly the policy it is measuring.
type RateLimitSuite struct {
	acceptance.Suite
}

func TestRateLimitSuite(t *testing.T) {
	suite.Run(t, new(RateLimitSuite))
}

// Two addresses that are unmistakably different clients, both from the
// documentation ranges so they can never collide with anything real.
const (
	addrAttacker  = "203.0.113.10"
	addrBystander = "198.51.100.7"
)

// limitedAPI starts an external API server with the given rate-limit policy and
// returns its base URL. Everything it owns is torn down when the test ends.
//
// The trusted-proxy list is the loopback: the httptest server answers on
// 127.0.0.1, so it stands in for the load balancer, and the tests can present
// distinct clients through X-Forwarded-For exactly as the ALB would.
func (s *RateLimitSuite) limitedAPI(policy config.RateLimitConfig) string {
	s.T().Helper()

	cfg := acceptconfig.DefaultConfig()
	cfg.Database.URL = testDatabaseURL()
	policy.TrustedProxies = []string{"127.0.0.0/8", "::1/128"}
	policy.ApplyDefaults()
	cfg.RateLimit = policy
	s.Require().True(cfg.RateLimit.Active())

	app, err := bootstrap.New(cfg, types.ModeExternal)
	s.Require().NoError(err)
	server := httptest.NewServer(app.Router)

	s.T().Cleanup(func() {
		server.Close()
		s.Require().NoError(app.Close())
		_ = os.RemoveAll(cfg.Storage.FS.Root)
	})
	return server.URL
}

// testDatabaseURL mirrors acceptance.Suite's own resolution — the suite keeps
// its parsed URL private, and this suite builds a second application.
func testDatabaseURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://admin:secret@postgres:5432/golang-api?sslmode=disable"
}

// rlResponse is the little of an answer these tests care about.
type rlResponse struct {
	status     int
	retryAfter string
	code       string
	cookie     string
}

func (r rlResponse) shed() bool { return r.status == http.StatusTooManyRequests }

// sendAs issues one request as the given client address. It never calls
// s.Require: it runs off the test goroutine in the concurrent cases, where
// failing from inside a helper is illegal.
func sendAs(
	client *http.Client, t *testing.T, method, url, clientAddr string, body any,
) (rlResponse, error) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return rlResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	if err != nil {
		return rlResponse{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if clientAddr != "" {
		req.Header.Set("X-Forwarded-For", clientAddr)
	}

	resp, err := client.Do(req)
	if err != nil {
		return rlResponse{}, err
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return rlResponse{}, err
	}

	out := rlResponse{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		out.code = envelope.Error.Code
	}
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			out.cookie = c.Value
		}
	}
	return out, nil
}

func (s *RateLimitSuite) send(client *http.Client, method, url, clientAddr string, body any) rlResponse {
	s.T().Helper()
	resp, err := sendAs(client, s.T(), method, url, clientAddr, body)
	s.Require().NoError(err)
	return resp
}

// newRateLimitClient builds a client that can hold `concurrency` requests open
// at once.
//
// Keep-alives are OFF deliberately. net/http transparently REPLAYS a request
// whose idle connection turned out to be dead — POSTs included, because
// http.NewRequest over a bytes.Reader sets GetBody — and a replayed login is
// charged twice against the account's attempt budget while the test sees a
// single response. That artifact is what a first draft of this suite tripped
// over (four asserted 401s, five charges, an account locked one attempt earlier
// than the test expected), and it has nothing to do with what is being
// measured. A fresh connection per request removes the replay path entirely.
func newRateLimitClient(concurrency int) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxConnsPerHost:   concurrency,
			DisableKeepAlives: true,
		},
	}
}

// TestAnonymousBudget_IsPerClientAddress is the headline property: password
// guessing from one address runs out of budget, and the address it runs out for
// is the one the load balancer reported — not the balancer's own, which would
// shed every customer at once, and not one the caller can choose.
func (s *RateLimitSuite) TestAnonymousBudget_IsPerClientAddress() {
	const budget = 4

	baseURL := s.limitedAPI(config.RateLimitConfig{
		Anonymous: config.RateLimitRuleConfig{Requests: budget, Burst: budget, WindowSeconds: 60},
	})
	client := newRateLimitClient(2)

	tenant := s.InsertTenant("rl-anon-"+uuid.NewString(), "Rate Limit Anonymous")
	const password = "s3cret-password"

	// TWO accounts, and that is not incidental. The account lockout charges an
	// attempt on EVERY login, successful ones included, and its threshold is 5;
	// spending the address budget on the same account as the closing
	// "a different address still works" login would leave that login sitting
	// one attempt away from the lock, so the test would be measuring the
	// interaction of two unrelated budgets instead of the limiter. The two
	// controls are deliberately independent here: one address, one account.
	guessed := "rl-anon-" + uuid.NewString() + "@acme.com"
	bystander := "rl-anon-" + uuid.NewString() + "@acme.com"
	s.InsertUserWithPassword(tenant, guessed, password)
	bystanderID := s.InsertUserWithPassword(tenant, bystander, password)

	wrong := map[string]any{"email": guessed, "password": "not-my-password"}

	// The budget is spent on real answers...
	for attempt := 1; attempt <= budget; attempt++ {
		resp := s.send(client, http.MethodPost, baseURL+"/auth/login", addrAttacker, wrong)
		s.Require().Equal(http.StatusUnauthorized, resp.status, "attempt %d is inside the budget", attempt)
	}

	// ...and then the address is shed, with everything a client needs to back off.
	for attempt := budget + 1; attempt <= budget+4; attempt++ {
		resp := s.send(client, http.MethodPost, baseURL+"/auth/login", addrAttacker, wrong)
		s.Require().Equal(http.StatusTooManyRequests, resp.status, "attempt %d is past the budget", attempt)
		s.Require().Equal("TOO_MANY_REQUESTS", resp.code, "the standard error envelope, not a bare status")

		seconds, err := strconv.Atoi(resp.retryAfter)
		s.Require().NoError(err, "Retry-After must be an integer number of seconds, got %q", resp.retryAfter)
		s.Require().GreaterOrEqual(seconds, 1, "Retry-After 0 reads as 'retry immediately'")
	}

	// THE OTHER HALF, and the one that makes the control usable: a different
	// client is untouched. A limiter that sheds the whole cell because one
	// address misbehaved is an outage with extra steps.
	//
	// The bystander's own account state is logged first, so that if this ever
	// does fail the log says immediately whether the limiter refused the request
	// (429) or the account did (a lock, or a row that is not there) — the two
	// have entirely different causes and only one of them is this code's.
	attempts, lockedUntil := s.failedLoginState(bystanderID)
	s.T().Logf("bystander account before the closing login: attempts=%d locked=%v", attempts, lockedUntil != nil)

	ok := s.send(client, http.MethodPost, baseURL+"/auth/login", addrBystander,
		map[string]any{"email": bystander, "password": password})
	s.Require().Equal(http.StatusOK, ok.status, "a different address must still be served")
	s.Require().NotEmpty(ok.cookie, "and must still get a session")
}

// TestAnonymousBudget_ForgedForwardedHeaderCannotRefillIt is the spoofing case.
// The proxy appends the address it actually saw, so the forged prefix a caller
// writes is to the LEFT of their real address and must be ignored. Read the
// chain from the left — the common implementation — and an attacker mints a
// fresh budget on every request, which is not a weaker limiter but none at all.
func (s *RateLimitSuite) TestAnonymousBudget_ForgedForwardedHeaderCannotRefillIt() {
	const budget = 3

	baseURL := s.limitedAPI(config.RateLimitConfig{
		Anonymous: config.RateLimitRuleConfig{Requests: budget, Burst: budget, WindowSeconds: 60},
	})
	client := newRateLimitClient(2)

	tenant := s.InsertTenant("rl-spoof", "Rate Limit Spoof")
	s.InsertUserWithPassword(tenant, "rl-spoof@acme.com", "s3cret-password")
	wrong := map[string]any{"email": "rl-spoof@acme.com", "password": "not-my-password"}

	for attempt := 1; attempt <= budget; attempt++ {
		resp := s.send(client, http.MethodPost, baseURL+"/auth/login", addrAttacker, wrong)
		s.Require().Equal(http.StatusUnauthorized, resp.status, "attempt %d", attempt)
	}

	// A brand-new forgery on every request, all in front of the same real
	// address. The budget must stay spent.
	for i, forged := range []string{"1.1.1.1", "8.8.8.8", "192.0.2.55, 1.1.1.1"} {
		resp := s.send(client, http.MethodPost, baseURL+"/auth/login",
			forged+", "+addrAttacker, wrong)
		s.Require().Equal(http.StatusTooManyRequests, resp.status,
			"forgery %d (%q) must not mint a new budget", i, forged)
	}
}

// TestAuthenticatedBudget_IsPerPrincipalNotPerAddress pins the other keying
// decision. On a tenant route the budget follows the caller: a whole office
// behind one NAT must not share one budget, and moving to another address must
// not refill the one a caller has spent.
func (s *RateLimitSuite) TestAuthenticatedBudget_IsPerPrincipalNotPerAddress() {
	const budget = 3

	baseURL := s.limitedAPI(config.RateLimitConfig{
		// The anonymous budget is left generous: nothing here may be shed by it.
		Anonymous:     config.RateLimitRuleConfig{Requests: 10_000, Burst: 10_000, WindowSeconds: 60},
		Authenticated: config.RateLimitRuleConfig{Requests: budget, Burst: budget, WindowSeconds: 60},
	})
	client := newRateLimitClient(2)

	tenant := s.InsertTenant("rl-principal", "Rate Limit Principal")
	alice := s.seedSessionFor(tenant)
	bob := s.seedSessionFor(tenant)

	spend := func(token, clientAddr string) rlResponse {
		req, err := http.NewRequestWithContext(s.T().Context(), http.MethodGet, baseURL+"/entities", nil)
		s.Require().NoError(err)
		req.Header.Set("Cookie", cookieName+"="+token)
		req.Header.Set("X-Forwarded-For", clientAddr)
		resp, err := client.Do(req)
		s.Require().NoError(err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return rlResponse{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}
	}

	for i := 1; i <= budget; i++ {
		s.Require().Equal(http.StatusOK, spend(alice, addrAttacker).status, "request %d", i)
	}

	// Spent. And changing address does not refill it — the budget is the
	// principal's, and one an attacker escapes by moving is no budget.
	shed := spend(alice, addrBystander)
	s.Require().Equal(http.StatusTooManyRequests, shed.status,
		"the budget must follow the principal, not the socket")
	s.Require().NotEmpty(shed.retryAfter)

	// A different principal on the SAME address is untouched: keying an
	// authenticated route by address would shed a whole office because one
	// colleague ran a script.
	s.Require().Equal(http.StatusOK, spend(bob, addrAttacker).status,
		"one user's exhausted budget must not shed their colleague behind the same address")
}

// TestVerificationGate_BoundsConcurrentPasswordVerification is the memory
// bound, which the rate budget cannot provide: argon2id holds 64 MiB per
// in-flight verification, /auth/login pays that even for an address that does
// not exist (it verifies a dummy hash so timing discloses nothing), and the
// API task is provisioned with 1 GiB. An attacker spread across many source
// addresses has a full rate budget on every one of them; only a bound on
// CONCURRENCY caps the memory.
func (s *RateLimitSuite) TestVerificationGate_BoundsConcurrentPasswordVerification() {
	const concurrent = 24

	baseURL := s.limitedAPI(config.RateLimitConfig{
		// Deliberately enormous, so nothing below can be shed by the rate
		// budget — whatever sheds here is the gate.
		Anonymous: config.RateLimitRuleConfig{Requests: 100_000, Burst: 100_000, WindowSeconds: 60},
		Verification: config.VerificationLimitConfig{
			MaxConcurrent: 1,
			MaxQueued:     2,
			MaxWaitMs:     25,
		},
	})
	client := newRateLimitClient(concurrent)

	tenant := s.InsertTenant("rl-gate", "Rate Limit Gate")
	email := "rl-gate@acme.com"
	userID := s.InsertUserWithPassword(tenant, email, "s3cret-password")
	wrong := map[string]any{"email": email, "password": "not-my-password"}

	results := make([]rlResponse, concurrent)
	errs := make([]error, concurrent)
	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(concurrent)
	for i := range concurrent {
		go func(i int) {
			defer wg.Done()
			<-start
			// Every request from its OWN address, so the per-address budget is
			// provably not what refuses any of them.
			addr := "203.0.113." + strconv.Itoa(i+1)
			results[i], errs[i] = sendAs(client, s.T(), http.MethodPost, baseURL+"/auth/login", addr, wrong)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		s.Require().NoError(err, "request %d failed at the transport level", i)
	}

	shed, served := 0, 0
	for i, resp := range results {
		switch {
		case resp.shed():
			shed++
			s.Require().Equal("TOO_MANY_REQUESTS", resp.code, "request %d", i)
			s.Require().NotEmpty(resp.retryAfter, "request %d must be told when to come back", i)
		default:
			served++
			s.Require().Equal(http.StatusUnauthorized, resp.status, "request %d", i)
		}
	}
	s.T().Logf("concurrent=%d served=%d shed=%d", concurrent, served, shed)

	s.Require().Positive(shed,
		"%d simultaneous verifications against a bound of 1 must be shed, not queued without limit", concurrent)
	s.Require().Positive(served, "the gate must still let real work through")

	// And the shed requests genuinely never reached argon2: the login use case
	// charges the account's attempt budget before it verifies anything, so the
	// counter can only have been moved by requests that got past the gate.
	attempts, _ := s.failedLoginState(userID)
	s.Require().LessOrEqual(attempts, served,
		"a shed request must not have paid the verification cost it was shed to avoid")
}

// TestHealthProbesAreExempt: shedding a liveness check is how a rate limiter
// talks the orchestrator into killing a perfectly healthy task — the exact
// failure mode this whole control exists to prevent. The probes arrive from the
// load balancer's own address, with no forwarded header, so they all share one
// key and would be the first thing an address budget refused.
func (s *RateLimitSuite) TestHealthProbesAreExempt() {
	baseURL := s.limitedAPI(config.RateLimitConfig{
		Anonymous: config.RateLimitRuleConfig{Requests: 2, Burst: 2, WindowSeconds: 60},
	})
	client := newRateLimitClient(2)

	for i := range 12 {
		resp := s.send(client, http.MethodGet, baseURL+"/health", "", nil)
		s.Require().Equal(http.StatusOK, resp.status, "probe %d", i)
	}
}

// seedSessionFor inserts an active tenant_admin with a valid session and returns
// the raw cookie token. It mirrors the suite's own session fixture, which is
// unexported; the token is a database row, so a session minted here
// authenticates against this suite's separately built server just as well.
func (s *RateLimitSuite) seedSessionFor(tenantID uuid.UUID) string {
	s.T().Helper()

	var userID string
	err := s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status)
		 VALUES ($1, $2, 'Rate Limit User', 'active') RETURNING id`,
		tenantID, "rl-"+uuid.NewString()+"@test.local").Scan(&userID)
	s.Require().NoError(err)

	// user_grants is RLS'd, so the insert runs inside a transaction that binds
	// the tenant GUC, mirroring the application's Tx seam.
	tx := s.DB.MustBegin()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID.String())
	if err != nil {
		_ = tx.Rollback()
		s.Require().NoError(err)
	}
	_, err = tx.Exec(`INSERT INTO user_grants (user_id, role) VALUES ($1, 'tenant_admin')`, userID)
	if err != nil {
		_ = tx.Rollback()
		s.Require().NoError(err)
	}
	s.Require().NoError(tx.Commit())

	token := uuid.NewString() + uuid.NewString()
	_, err = s.DB.Exec(
		`INSERT INTO sessions (token_hash, user_id, tenant_id, idle_expires_at, absolute_expires_at)
		 VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour', NOW() + INTERVAL '30 days')`,
		crypto.HashToken(token), userID, tenantID)
	s.Require().NoError(err)
	return token
}

// failedLoginState reads the lockout columns. (IdentitySuite has its own; the
// two suites are independent by design, so this one carries its own reader.)
func (s *RateLimitSuite) failedLoginState(userID uuid.UUID) (int, *time.Time) {
	s.T().Helper()

	var attempts int
	var lockedUntil *time.Time
	err := s.DB.QueryRowx(
		`SELECT failed_login_attempts, locked_until FROM users WHERE id = $1`, userID,
	).Scan(&attempts, &lockedUntil)
	s.Require().NoError(err)
	return attempts, lockedUntil
}
