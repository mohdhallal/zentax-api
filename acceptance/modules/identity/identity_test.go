package identity_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

type IdentitySuite struct {
	acceptance.Suite
}

func TestIdentitySuite(t *testing.T) {
	suite.Run(t, new(IdentitySuite))
}

const cookieName = "zentax_session"

func sessionCookie(resp *acceptance.TestResponse) string {
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c.Value
		}
	}
	return ""
}

// TestLoginSessionLogout covers the core flow: bad password → 401, login → session
// cookie, session authenticates /auth/me + a tenant route, logout revokes it.
func (s *IdentitySuite) TestLoginSessionLogout() {
	tenant := s.InsertTenant("auth-a", "Auth Tenant A")
	s.InsertUserWithPassword(tenant, "admin@acme.com", "s3cret-password")

	s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "admin@acme.com", "password": "nope"}).
		AssertStatus(s.T(), http.StatusUnauthorized)

	login := s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "admin@acme.com", "password": "s3cret-password"})
	login.AssertStatus(s.T(), http.StatusOK)
	token := sessionCookie(login)
	s.Require().NotEmpty(token)

	var me struct {
		Email string `json:"email"`
	}
	s.Client.External().WithSession(token).GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	s.Require().Equal("admin@acme.com", me.Email)

	// the session authenticates a tenant route (tenant comes from the session)
	s.Client.External().WithSession(token).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)

	s.Client.External().WithSession(token).POST(s.T(), "/auth/logout", nil).AssertStatus(s.T(), http.StatusNoContent)
	s.Client.External().WithSession(token).GET(s.T(), "/auth/me").AssertStatus(s.T(), http.StatusUnauthorized)
}

// TestMfaFlow covers enroll → enable → re-login (mfa_pending) → verify → full
// session, including that a pending session cannot reach tenant routes.
func (s *IdentitySuite) TestMfaFlow() {
	tenant := s.InsertTenant("auth-b", "Auth Tenant B")
	s.InsertUserWithPassword(tenant, "mfa@acme.com", "s3cret-password")

	login := s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "mfa@acme.com", "password": "s3cret-password"})
	login.AssertStatus(s.T(), http.StatusOK)
	token := sessionCookie(login)

	var enroll struct {
		Secret string `json:"secret"`
	}
	s.Client.External().WithSession(token).POST(s.T(), "/auth/mfa/enroll", nil).DecodeData(s.T(), &enroll)
	s.Require().NotEmpty(enroll.Secret)

	code, err := totp.GenerateCode(enroll.Secret, time.Now())
	s.Require().NoError(err)
	s.Client.External().WithSession(token).
		POST(s.T(), "/auth/mfa/enable", map[string]any{"code": code}).
		AssertStatus(s.T(), http.StatusNoContent)

	// re-login now requires MFA
	login2 := s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "mfa@acme.com", "password": "s3cret-password"})
	login2.AssertStatus(s.T(), http.StatusOK)
	var lr struct {
		MFARequired bool `json:"mfaRequired"`
	}
	login2.DecodeData(s.T(), &lr)
	s.Require().True(lr.MFARequired)
	pending := sessionCookie(login2)

	// a pending session cannot reach a tenant route
	s.Client.External().WithSession(pending).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusUnauthorized)

	// verify → full session (rotated token)
	code2, err := totp.GenerateCode(enroll.Secret, time.Now())
	s.Require().NoError(err)
	verify := s.Client.External().WithSession(pending).
		POST(s.T(), "/auth/mfa/verify", map[string]any{"code": code2})
	verify.AssertStatus(s.T(), http.StatusOK)
	full := sessionCookie(verify)
	s.Require().NotEmpty(full)

	s.Client.External().WithSession(full).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)
}

// maxFailedAttempts / lockoutDuration mirror the login use case's policy.
const (
	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute
)

// failedLoginState reads the lockout columns straight from Postgres — the point
// of this test is that they are actually written, so nothing goes through the
// API to observe them.
func (s *IdentitySuite) failedLoginState(userID uuid.UUID) (int, *time.Time) {
	var attempts int
	var lockedUntil *time.Time
	err := s.DB.QueryRowx(
		`SELECT failed_login_attempts, locked_until FROM users WHERE id = $1`, userID,
	).Scan(&attempts, &lockedUntil)
	s.Require().NoError(err)
	return attempts, lockedUntil
}

// expireLock simulates the passage of the lockout window.
func (s *IdentitySuite) expireLock(userID uuid.UUID) {
	_, err := s.DB.Exec(
		`UPDATE users SET locked_until = NOW() - INTERVAL '1 minute' WHERE id = $1`, userID)
	s.Require().NoError(err)
}

// TestFailedLoginLockout is the regression test for a lockout that could never
// engage. /auth/login used to declare Tx, and the transaction middleware rolls
// the request transaction back on any status >= 400 — discarding the very UPDATE
// the 401 path issues. Six wrong passwords left failed_login_attempts at 0 and
// locked_until NULL, so online password guessing was unthrottled. The route now
// declares no transaction at all: the attempt is charged by a statement of its
// own that commits before the password is even verified.
//
// It also pins the two properties that making the lockout reachable exposed:
// serving a lockout must not EXTEND it, and serving it must CLEAR the debt.
// (What it does NOT pin is that the budget holds under concurrency — the counter
// saturates either way, so only a burst with a correct guess in it can tell:
// TestLoginBudget_ConcurrencyCannotWidenIt.)
func (s *IdentitySuite) TestFailedLoginLockout() {
	tenant := s.InsertTenant("auth-lockout", "Auth Tenant Lockout")
	userID := s.InsertUserWithPassword(tenant, "lockout@acme.com", "s3cret-password")

	wrong := map[string]any{"email": "lockout@acme.com", "password": "not-my-password"}
	right := map[string]any{"email": "lockout@acme.com", "password": "s3cret-password"}

	for attempt := 1; attempt <= maxFailedAttempts; attempt++ {
		s.Client.External().POST(s.T(), "/auth/login", wrong).
			AssertStatus(s.T(), http.StatusUnauthorized)

		attempts, lockedUntil := s.failedLoginState(userID)
		s.Require().Equal(attempt, attempts,
			"the attempt must be durably charged even though the request answers 401")
		if attempt < maxFailedAttempts {
			s.Require().Nil(lockedUntil, "no lock before the threshold")
		}
	}

	// The threshold locked the account, in the same statement as the counter.
	_, lockedUntil := s.failedLoginState(userID)
	s.Require().NotNil(lockedUntil, "the threshold must lock the account")
	s.Require().True(lockedUntil.After(time.Now()), "the lock must be in the future")
	s.Require().True(lockedUntil.Before(time.Now().Add(lockoutDuration+time.Minute)),
		"the lock must be the configured window, not indefinite")

	// Locked: the CORRECT password is refused, and refused in exactly the words a
	// wrong one gets. Telling this caller the account is locked was a PASSWORD
	// ORACLE, not a courtesy — whoever just spent five requests locking the
	// address could then read the right password off the responses while the lock
	// refused no verification at all. The owner's cost (a generic message, and a
	// wait) is the deliberate price; see TestLoginFailures_Indistinguishable.
	locked := s.Client.External().POST(s.T(), "/auth/login", right)
	locked.AssertStatus(s.T(), http.StatusUnauthorized)
	s.Require().NotContains(strings.ToLower(locked.BodyString()), "lock",
		"a locked account must never announce itself, least of all to a correct password")

	// A further wrong password while the lock is live changes nothing: the
	// counter is already saturated and the window must not slide, or an attacker
	// could hold the account shut for as long as they cared to keep guessing.
	wrongWhileLocked := s.Client.External().POST(s.T(), "/auth/login", wrong)
	wrongWhileLocked.AssertStatus(s.T(), http.StatusUnauthorized)
	s.Require().Equal(locked.BodyString(), wrongWhileLocked.BodyString(),
		"right and wrong passwords on a locked account must be byte-identical")
	attempts, stillLockedUntil := s.failedLoginState(userID)
	s.Require().Equal(maxFailedAttempts, attempts,
		"a live lock records nothing: the counter is already at the threshold")
	s.Require().True(lockedUntil.Equal(*stillLockedUntil), "the lock window must not slide")

	// The window elapses. Serving the lockout must CLEAR the debt: the next
	// failure starts a fresh count at 1 and does not re-lock. Until now the
	// counter stayed saturated and nothing reset it, so "attempts + 1 >=
	// threshold" was true forever — ONE wrong password re-locked for another 15
	// minutes and the victim's own password was refused. An unauthenticated
	// attacker held any known address locked out permanently, at 4 requests/hour.
	s.expireLock(userID)
	s.Client.External().POST(s.T(), "/auth/login", wrong).
		AssertStatus(s.T(), http.StatusUnauthorized)
	attempts, lockedUntil = s.failedLoginState(userID)
	s.Require().Equal(1, attempts, "an elapsed window must start a fresh count")
	s.Require().Nil(lockedUntil, "one wrong password after the window must not re-lock")

	// ...so the owner's correct password works again, and clears what is left.
	login := s.Client.External().POST(s.T(), "/auth/login", right)
	login.AssertStatus(s.T(), http.StatusOK)
	s.Require().NotEmpty(sessionCookie(login))

	attempts, lockedUntil = s.failedLoginState(userID)
	s.Require().Equal(0, attempts, "a successful login clears the counter")
	s.Require().Nil(lockedUntil, "a successful login clears a stale lock")
}

// enumerationSamples is how many probes each address gets. Deliberately below
// maxFailedAttempts: probing an unlocked account must not lock it, or the case
// it stands for disappears mid-measurement.
const enumerationSamples = 3

// insertLoginUser seeds a user row directly, for the login shapes the ordinary
// fixture cannot express — a disabled member, an invited one with no password
// yet, a service account. No grant: none of these ever reaches a tenant route.
func (s *IdentitySuite) insertLoginUser(
	tenantID uuid.UUID, email, kind, status string, password *string,
) uuid.UUID {
	var hash *string
	if password != nil {
		h, err := crypto.HashPassword(*password)
		s.Require().NoError(err)
		hash = &h
	}
	var id uuid.UUID
	err := s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, kind, password_hash, status)
		 VALUES ($1, $2, 'Probe', $3, $4, $5) RETURNING id`,
		tenantID, strings.ToLower(email), kind, hash, status).Scan(&id)
	s.Require().NoError(err)
	return id
}

// volatileHeaders differ per response by design and are excluded from the
// comparison; everything else must match byte for byte. Set-Cookie is NOT in
// here on purpose — a refused login that plants a cookie would be a difference
// worth failing on.
var volatileHeaders = map[string]bool{"Date": true, "X-Request-Id": true}

func stableHeaders(resp *acceptance.TestResponse) map[string][]string {
	out := make(map[string][]string, len(resp.Header))
	for k, v := range resp.Header {
		if !volatileHeaders[http.CanonicalHeaderKey(k)] {
			out[k] = v
		}
	}
	return out
}

// TestLoginFailures_Indistinguishable is the regression test for two oracles on
// /auth/login, both found by measuring the real router.
//
//  1. USER ENUMERATION. A locked account used to short-circuit BEFORE the
//     password comparison, while an unknown address paid the full argon2 cost.
//     Measured: locked+registered 2.4ms, unknown 69ms — a ~28x gap plus a
//     distinguishing body, and five wrong passwords minted that state against
//     any address.
//  2. PASSWORD ORACLE. Moving the lock check AFTER the verification closed (1)
//     and opened something worse: while locked, a wrong password got the generic
//     refusal and the CORRECT password got "account temporarily locked". An
//     attacker who had just locked an address could score candidate passwords
//     off the responses at full speed — the lock refused no verification at all.
//
// So there is no longer any failure that reads differently from any other. Every
// shape below — including the right password on a locked account — must return
// the same status, the same bytes, and the same headers. The assertion on the
// WORK (exactly one argon2 verification per path, against the stored hash only
// when the account could authenticate right now AND the attempt was inside its
// budget) is a unit test, TestLogin_EveryFailure_SameAnswerAndSameWork: a
// wall-clock threshold on a shared CI Postgres is a flaky test. Medians are
// logged here so a human can see the gap is gone, and so is the one residual: a
// REGISTERED address additionally pays the small attempt-charging UPDATE, a few
// ms against a ~70ms floor. Locked and unlocked registered addresses now pay it
// alike — the charge runs before anything is known about the outcome — so the
// residual separates "registered" from "unknown" and nothing finer. The current
// figures are recorded in STATUS.md, not repeated here.
func (s *IdentitySuite) TestLoginFailures_Indistinguishable() {
	tenant := s.InsertTenant("auth-enum", "Auth Tenant Enumeration")
	lockedID := s.InsertUserWithPassword(tenant, "locked-enum@acme.com", "s3cret-password")
	s.InsertUserWithPassword(tenant, "open-enum@acme.com", "s3cret-password")

	password := "s3cret-password"
	s.insertLoginUser(tenant, "disabled-enum@acme.com", "human", "disabled", &password)
	s.insertLoginUser(tenant, "invited-enum@acme.com", "human", "invited", nil)
	s.insertLoginUser(tenant, "service-enum@acme.com", "service", "active", &password)

	// Lock one account exactly as an attacker would.
	for range maxFailedAttempts {
		s.Client.External().
			POST(s.T(), "/auth/login", map[string]any{
				"email": "locked-enum@acme.com", "password": "not-my-password",
			}).AssertStatus(s.T(), http.StatusUnauthorized)
	}
	_, lockedUntil := s.failedLoginState(lockedID)
	s.Require().NotNil(lockedUntil, "the probe target must actually be locked")

	probes := []struct{ name, email, password string }{
		{"locked, wrong password", "locked-enum@acme.com", "not-my-password"},
		// THE oracle case: on a locked account the right password must be
		// answered exactly like a wrong one.
		{"locked, RIGHT password", "locked-enum@acme.com", password},
		{"unlocked, registered", "open-enum@acme.com", "not-my-password"},
		{"disabled member", "disabled-enum@acme.com", password},
		{"invited, no password", "invited-enum@acme.com", password},
		{"service account", "service-enum@acme.com", password},
		// Normalisation must not fork the answer either.
		{"locked, mixed-case email", "LOCKED-Enum@Acme.COM", password},
		{"unknown address", "nobody-" + uuid.NewString() + "@acme.com", "not-my-password"},
	}

	type answer struct {
		status  int
		body    string
		headers map[string][]string
	}
	answers := make(map[string]answer, len(probes))
	for _, probe := range probes {
		samples := make([]time.Duration, 0, enumerationSamples)
		var last *acceptance.TestResponse
		for range enumerationSamples {
			started := time.Now()
			resp := s.Client.External().POST(s.T(), "/auth/login",
				map[string]any{"email": probe.email, "password": probe.password})
			samples = append(samples, time.Since(started))
			resp.AssertStatus(s.T(), http.StatusUnauthorized)
			last = resp
		}
		s.Require().Empty(sessionCookie(last), "a refused login must never plant a session cookie")
		answers[probe.name] = answer{last.StatusCode, last.BodyString(), stableHeaders(last)}
		slices.Sort(samples)
		s.T().Logf("%-26s median=%-8s body=%s",
			probe.name, samples[len(samples)/2].Round(100*time.Microsecond), last.BodyString())
	}

	baseline := answers["unknown address"]
	for _, probe := range probes {
		got := answers[probe.name]
		s.Require().Equal(baseline.status, got.status,
			"%s must answer the same status as an unknown address", probe.name)
		s.Require().Equal(baseline.body, got.body,
			"%s must be byte-identical to an unknown address", probe.name)
		s.Require().Equal(baseline.headers, got.headers,
			"%s must carry the same headers as an unknown address", probe.name)
	}

	s.Require().NotContains(strings.ToLower(baseline.body), "lock",
		"no refusal may mention the lock at all")

	// A PADDED address DOES reach the use case, and resolves to the same account.
	// (The earlier claim here — that `validate:"email"` refuses padding with a
	// 400 — was wrong about the outcome and about the mechanism. Measured: the
	// validator does reject a padded address, but it never sees one, because the
	// route builder TrimSpaces every string in the body before validating
	// (routing.sanitize). So the padded form arrives at Login already canonical.
	// The assertion that both padded probes answered alike was vacuous: both were
	// simply ordinary refusals.)
	//
	// The behaviour worth pinning is therefore the positive one: padding does not
	// make an address a different account. With the CORRECT password on an
	// unlocked account, a padded spelling logs in.
	paddedLogin := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": "  open-enum@acme.com  ", "password": password})
	paddedLogin.AssertStatus(s.T(), http.StatusOK)
	s.Require().NotEmpty(sessionCookie(paddedLogin),
		"a padded address must resolve to the same account, not a 400 and not a different user")

	// ...and it does not become a different account when the answer is a refusal
	// either: a padded registered address is answered exactly like a padded
	// unknown one.
	paddedKnown := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": "  locked-enum@acme.com  ", "password": password})
	paddedUnknown := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": "  nobody-" + uuid.NewString() + "@acme.com  ", "password": password})
	paddedKnown.AssertStatus(s.T(), http.StatusUnauthorized)
	s.Require().Equal(paddedUnknown.StatusCode, paddedKnown.StatusCode,
		"a padded address must not be answered differently because it is registered")
	s.Require().Equal(paddedUnknown.BodyString(), paddedKnown.BodyString())

	// Probing a locked account — right password included — must not have moved
	// its counter or slid its window: the requests it refuses cannot extend it.
	attempts, stillLockedUntil := s.failedLoginState(lockedID)
	s.Require().Equal(maxFailedAttempts, attempts, "a live lock records nothing")
	s.Require().True(lockedUntil.Equal(*stillLockedUntil), "a live lock must not slide")
}

// TestCrossOriginAuthMutationsRejected is the regression test for a CSRF gate
// that covered only tenant routes: the origin check lived inside RequireAuth,
// which the route builder wires only when a route declares a tenant. The public
// /auth mutations declare none, so a page on any origin could POST /auth/logout
// with the victim's cookie and genuinely destroy their session. The check now
// sits in the route builder, around every external route.
func (s *IdentitySuite) TestCrossOriginAuthMutationsRejected() {
	const evil = "https://evil.example"

	tenant := s.InsertTenant("auth-csrf", "Auth Tenant CSRF")
	s.InsertUserWithPassword(tenant, "csrf@acme.com", "s3cret-password")
	creds := map[string]any{"email": "csrf@acme.com", "password": "s3cret-password"}

	// LOGIN — refused before the credentials are looked at, and no cookie is
	// planted in the victim's browser (session fixation).
	forged := s.Client.External().WithOrigin(evil).POST(s.T(), "/auth/login", creds)
	forged.AssertStatus(s.T(), http.StatusForbidden)
	s.Require().Empty(sessionCookie(forged), "no session cookie for a cross-origin login")

	// Same origin (the product's own app) still works...
	same := s.Client.External().WithOrigin(s.External.URL).POST(s.T(), "/auth/login", creds)
	same.AssertStatus(s.T(), http.StatusOK)
	token := sessionCookie(same)
	s.Require().NotEmpty(token)

	// ...and so does no Origin at all (server-to-server clients; the Express
	// adapter strips the header).
	s.Client.External().POST(s.T(), "/auth/login", creds).AssertStatus(s.T(), http.StatusOK)

	// LOGOUT — the sharpest case: cookie-authenticated, state-changing, public.
	s.Client.External().WithSession(token).WithOrigin(evil).
		POST(s.T(), "/auth/logout", nil).AssertStatus(s.T(), http.StatusForbidden)
	s.Client.External().WithSession(token).GET(s.T(), "/auth/me").
		AssertStatus(s.T(), http.StatusOK) // the session survived the attempt

	// ACCEPT-INVITE — public, no tenant, no transaction. Cross-origin it is
	// refused before the token is even parsed; the same bogus token from the
	// site's own origin reaches the handler and is answered 400.
	invite := map[string]any{"token": "zti_not-a-real-token", "password": "n3w-password-123"}
	s.Client.External().WithOrigin(evil).
		POST(s.T(), "/auth/accept-invite", invite).AssertStatus(s.T(), http.StatusForbidden)
	s.Client.External().WithOrigin(s.External.URL).
		POST(s.T(), "/auth/accept-invite", invite).AssertStatus(s.T(), http.StatusBadRequest)

	// Reads are untouched, whatever origin they claim.
	s.Client.External().WithSession(token).WithOrigin(evil).
		GET(s.T(), "/auth/me").AssertStatus(s.T(), http.StatusOK)
	s.Client.External().WithSession(token).WithOrigin(evil).
		GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)

	// A tenant mutation keeps the behaviour it always had (the control).
	s.Client.External().WithSession(token).WithOrigin(evil).
		POST(s.T(), "/entities", map[string]any{"name": "Forged Ltd"}).
		AssertStatus(s.T(), http.StatusForbidden)

	// Logout from the site's own origin still works.
	s.Client.External().WithSession(token).WithOrigin(s.External.URL).
		POST(s.T(), "/auth/logout", nil).AssertStatus(s.T(), http.StatusNoContent)
	s.Client.External().WithSession(token).GET(s.T(), "/auth/me").
		AssertStatus(s.T(), http.StatusUnauthorized)
}
