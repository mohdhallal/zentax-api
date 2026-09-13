package securityevents_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	acceptconfig "github.com/mohamadhallal/zentax-api/acceptance/config"
	"github.com/mohamadhallal/zentax-api/bootstrap"
	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// A SHED REQUEST IS NOT AN AUTHENTICATION EVENT, and this is the test that says
// so out loud, because the opposite is a tempting mistake: a burst of 429s at
// /auth/login is exactly the traffic that looks like an attack.
//
// Three reasons it stays out. It never reached a credential check, so it says
// nothing about who tried what — there is no outcome to record. The limiter
// already counts it (metrics) and logs it (the request logger runs before the
// shed valve precisely so a 429 is visible), so the detection signal exists
// where detection lives. And recording it would hand an unauthenticated caller
// one durable row per request in a store whose value depends on somebody being
// able to read it — the write amplifier the volume rule exists to prevent.
//
// What the stream shows instead is exactly the attempts that were ACTUALLY
// tried: the ones the limiter let through.
func (s *SecuritySuite) TestAShedRequestIsNotAnAuthenticationEvent() {
	const (
		budget   = 3  // requests the limiter admits in the window
		attempts = 12 // what the caller fires at it
	)

	url := s.limitedAPI(config.RateLimitConfig{
		Anonymous: config.RateLimitRuleConfig{Requests: budget, Burst: budget, WindowSeconds: 60},
	})
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	admitted, shed := 0, 0
	for range attempts {
		switch s.postLogin(client, url) {
		case http.StatusTooManyRequests:
			shed++
		case http.StatusUnauthorized:
			admitted++
		default:
			s.FailNow("a login against an unknown address must answer 401 or 429")
		}
	}
	s.Require().Positive(shed, "the limiter has to actually shed something for this test to mean anything")
	s.Require().Equal(budget, admitted, "the limiter admits exactly its budget")

	entries := s.stream(securityevent.Filter{Event: securityevent.EventLoginFailed})
	s.Require().Len(entries, admitted,
		"the stream must hold one event per attempt the limiter ADMITTED, and nothing for the %d it shed", shed)
}

// postLogin fires one login at an address that exists nowhere and returns the
// status. A fresh address per call keeps this from touching any account's
// lockout budget, which is a different control being measured elsewhere.
func (s *SecuritySuite) postLogin(client *http.Client, baseURL string) int {
	s.T().Helper()
	body, err := json.Marshal(map[string]any{
		"email": "shed-" + s.T().Name() + "@example.invalid", "password": "whatever",
	})
	s.Require().NoError(err)

	req, err := http.NewRequestWithContext(s.T().Context(), http.MethodPost, baseURL+"/auth/login", bytes.NewReader(body))
	s.Require().NoError(err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	s.Require().NoError(err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// limitedAPI starts a second external API with rate limiting ON, because the
// shared harness deliberately runs with it off (several suites fire bursts on
// purpose). Everything it owns is torn down when the test ends.
func (s *SecuritySuite) limitedAPI(policy config.RateLimitConfig) string {
	s.T().Helper()

	cfg := acceptconfig.DefaultConfig()
	cfg.Database.URL = os.Getenv("TEST_DATABASE_URL")
	if cfg.Database.URL == "" {
		cfg.Database.URL = "postgres://admin:secret@postgres:5432/golang-api?sslmode=disable"
	}
	// Trust nothing: every request is charged to its socket peer, which for an
	// httptest server is the one loopback client this test uses.
	policy.TrustedProxies = []string{}
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
