package identity_test

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// mfaBudget mirrors maxMFAAttempts in modules/identity/usecases/mfa.go — the
// number of code validations one session may buy.
const mfaBudget = 5

// wrongMFACode is not a code any authenticator would ever emit for these secrets.
// (A six-digit constant could in principle collide with the live window; the
// assertions below would fail loudly and immediately if it ever did.)
const wrongMFACode = "000000"

// sessionState reads the session row behind a cookie straight from Postgres —
// the point of these tests is that the budget is actually written and that the
// session is actually gone, so nothing goes through the API to observe it.
// The lookup is by token hash, which survives the rotation a successful
// verification performs: the row is the same row, carrying the new token.
func (s *IdentitySuite) sessionState(token string) (attempts int, revoked, pending bool) {
	err := s.DB.QueryRowx(
		`SELECT mfa_attempts, revoked_at IS NOT NULL, mfa_pending
		   FROM sessions WHERE token_hash = $1`, crypto.HashToken(token),
	).Scan(&attempts, &revoked, &pending)
	s.Require().NoError(err)
	return attempts, revoked, pending
}

// totpState reads the enrolment columns of a user.
func (s *IdentitySuite) totpState(userID uuid.UUID) (enrolled, enabled bool) {
	err := s.DB.QueryRowx(
		`SELECT totp_secret_enc IS NOT NULL, totp_enabled FROM users WHERE id = $1`, userID,
	).Scan(&enrolled, &enabled)
	s.Require().NoError(err)
	return enrolled, enabled
}

// enrollAndEnable takes a user with a live full session through
// enroll → enable and returns the TOTP secret.
func (s *IdentitySuite) enrollAndEnable(token string) string {
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
	return enroll.Secret
}

// login signs in and returns the cookie token plus whether MFA is still owed.
func (s *IdentitySuite) login(email, password string) (string, bool) {
	resp := s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": email, "password": password})
	resp.AssertStatus(s.T(), http.StatusOK)
	var lr struct {
		MFARequired bool `json:"mfaRequired"`
	}
	resp.DecodeData(s.T(), &lr)
	return sessionCookie(resp), lr.MFARequired
}

// TestMfaVerify_AttemptBudget is the regression test for an authentication
// bypass: /auth/mfa/verify had no attempt counter and did not invalidate the
// pending session on a wrong code, so the six digits standing between a stolen
// password and a live session could be retried without limit. Twenty guesses an
// hour is what a second factor is worth against a human; a script with an
// unbounded retry walks the whole million-value space.
//
// The budget is charged BEFORE the code is validated and committed outside any
// request transaction (the tx middleware rolls a request transaction back on any
// 4xx, which is what once made the login lockout unreachable), so every one of
// these refusals must leave the counter one higher than it found it.
func (s *IdentitySuite) TestMfaVerify_AttemptBudget() {
	tenant := s.InsertTenant("mfa-budget", "MFA Budget Tenant")
	const password = "s3cret-password"
	email := "mfa-budget@acme.com"
	s.InsertUserWithPassword(tenant, email, password)

	first, mfaRequired := s.login(email, password)
	s.Require().False(mfaRequired)
	secret := s.enrollAndEnable(first)

	s.Run("the budget is spent and the pending session is destroyed", func() {
		pending, mfaRequired := s.login(email, password)
		s.Require().True(mfaRequired, "MFA is on, so the login must leave the session pending")

		var lastRefusal string
		for attempt := 1; attempt <= mfaBudget; attempt++ {
			resp := s.Client.External().WithSession(pending).
				POST(s.T(), "/auth/mfa/verify", map[string]any{"code": wrongMFACode})
			resp.AssertStatus(s.T(), http.StatusUnauthorized)
			s.Require().Empty(sessionCookie(resp), "a refused verification must never rotate the cookie")

			attempts, revoked, _ := s.sessionState(pending)
			s.Require().Equal(attempt, attempts,
				"the attempt must be durably charged even though the request answers 401 "+
					"(a counter rolled back with the response is no counter)")
			if attempt < mfaBudget {
				s.Require().False(revoked, "the session must survive a wrong code inside the budget")
			}
			lastRefusal = resp.BodyString()
		}

		// THE BLOCKER: the wrong code that spent the last attempt took the
		// session with it.
		attempts, revoked, _ := s.sessionState(pending)
		s.Require().Equal(mfaBudget, attempts, "the budget is exactly the threshold")
		s.Require().True(revoked,
			"spending the budget must destroy the pending session — a session that survives "+
				"every wrong code is an unbounded guessing oracle")

		// ...so the CORRECT code is now worth nothing: there is no session left
		// to complete, and the answer is the one every other refusal gives.
		code, err := totp.GenerateCode(secret, time.Now())
		s.Require().NoError(err)
		after := s.Client.External().WithSession(pending).
			POST(s.T(), "/auth/mfa/verify", map[string]any{"code": code})
		after.AssertStatus(s.T(), http.StatusUnauthorized)
		s.Require().Empty(sessionCookie(after),
			"a destroyed session must never be completed, least of all by a correct code")
		s.Require().Equal(lastRefusal, after.BodyString(),
			"a destroyed session must be answered exactly like a wrong code: a distinguishable "+
				"refusal tells a script where the budget ends")

		// ...and the dead cookie reaches nothing else either.
		s.Client.External().WithSession(pending).GET(s.T(), "/entities").
			AssertStatus(s.T(), http.StatusUnauthorized)

		// A further guess past the budget changes nothing: the counter is
		// saturated and the session stays destroyed.
		s.Client.External().WithSession(pending).
			POST(s.T(), "/auth/mfa/verify", map[string]any{"code": wrongMFACode}).
			AssertStatus(s.T(), http.StatusUnauthorized)
		attempts, revoked, _ = s.sessionState(pending)
		s.Require().Equal(mfaBudget, attempts, "the counter saturates at the threshold")
		s.Require().True(revoked)
	})

	s.Run("a correct code inside the budget still works and clears the count", func() {
		pending, mfaRequired := s.login(email, password)
		s.Require().True(mfaRequired)

		// Spend part of the budget: a typo must not cost the login.
		for attempt := 1; attempt <= 2; attempt++ {
			s.Client.External().WithSession(pending).
				POST(s.T(), "/auth/mfa/verify", map[string]any{"code": wrongMFACode}).
				AssertStatus(s.T(), http.StatusUnauthorized)
		}
		attempts, revoked, _ := s.sessionState(pending)
		s.Require().Equal(2, attempts)
		s.Require().False(revoked, "two wrong codes are inside the budget")

		code, err := totp.GenerateCode(secret, time.Now())
		s.Require().NoError(err)
		verify := s.Client.External().WithSession(pending).
			POST(s.T(), "/auth/mfa/verify", map[string]any{"code": code})
		verify.AssertStatus(s.T(), http.StatusOK)
		full := sessionCookie(verify)
		s.Require().NotEmpty(full, "a verified code must rotate the session token")

		// The success cleared the debt, in the same statement that completed the
		// session: the next login starts with a whole budget, and a long-lived
		// session never carries the attempts that created it.
		attempts, revoked, pendingFlag := s.sessionState(full)
		s.Require().Equal(0, attempts, "a successful verification clears the counter")
		s.Require().False(revoked)
		s.Require().False(pendingFlag, "verification must clear mfa_pending")

		s.Client.External().WithSession(full).GET(s.T(), "/entities").
			AssertStatus(s.T(), http.StatusOK)
	})

	s.Run("the last attempt of the budget still completes the login", func() {
		pending, mfaRequired := s.login(email, password)
		s.Require().True(mfaRequired)

		for attempt := 1; attempt < mfaBudget; attempt++ {
			s.Client.External().WithSession(pending).
				POST(s.T(), "/auth/mfa/verify", map[string]any{"code": wrongMFACode}).
				AssertStatus(s.T(), http.StatusUnauthorized)
		}
		attempts, revoked, _ := s.sessionState(pending)
		s.Require().Equal(mfaBudget-1, attempts)
		s.Require().False(revoked)

		// The threshold-th attempt is the LAST TRY, not the first refusal.
		code, err := totp.GenerateCode(secret, time.Now())
		s.Require().NoError(err)
		verify := s.Client.External().WithSession(pending).
			POST(s.T(), "/auth/mfa/verify", map[string]any{"code": code})
		verify.AssertStatus(s.T(), http.StatusOK)
		full := sessionCookie(verify)
		s.Require().NotEmpty(full)

		attempts, _, _ = s.sessionState(full)
		s.Require().Equal(0, attempts)
	})
}

// TestMfaEnable_ConfirmationBudget covers the neighbouring flow: enrolment
// confirmation validated codes in the same unbounded loop. It is NOT an
// authentication bypass — /auth/mfa/enroll hands this same caller the secret in
// cleartext, so there is nothing to discover by guessing — but the loop is
// unbounded work on an authenticated route, and the bound is the same budget.
//
// What spending it destroys is the PENDING ENROLMENT rather than the session:
// the proportionate "start again" for a confirmation step. The session survives,
// and the next enrolment gets a fresh secret and a fresh budget.
func (s *IdentitySuite) TestMfaEnable_ConfirmationBudget() {
	tenant := s.InsertTenant("mfa-enable-budget", "MFA Enable Budget Tenant")
	const password = "s3cret-password"
	email := "mfa-enable-budget@acme.com"
	userID := s.InsertUserWithPassword(tenant, email, password)

	token, _ := s.login(email, password)

	var enroll struct {
		Secret string `json:"secret"`
	}
	s.Client.External().WithSession(token).POST(s.T(), "/auth/mfa/enroll", nil).DecodeData(s.T(), &enroll)
	s.Require().NotEmpty(enroll.Secret)

	for attempt := 1; attempt <= mfaBudget; attempt++ {
		s.Client.External().WithSession(token).
			POST(s.T(), "/auth/mfa/enable", map[string]any{"code": wrongMFACode}).
			AssertStatus(s.T(), http.StatusUnauthorized)

		if attempt < mfaBudget {
			attempts, _, _ := s.sessionState(token)
			s.Require().Equal(attempt, attempts,
				"the confirmation attempt must be durably charged despite the 401")
			enrolled, _ := s.totpState(userID)
			s.Require().True(enrolled, "an enrolment inside the budget must survive")
		}
	}

	// The budget is spent: the unconfirmed secret is gone, MFA is still off, and
	// the counter is returned so the owner can enrol again at once.
	enrolled, enabled := s.totpState(userID)
	s.Require().False(enrolled, "spending the budget must discard the pending enrolment")
	s.Require().False(enabled, "a discarded enrolment must never leave MFA on")

	attempts, revoked, _ := s.sessionState(token)
	s.Require().Equal(0, attempts, "the discard returns the session's budget")
	s.Require().False(revoked, "confirmation failures must not destroy the session itself")
	s.Client.External().WithSession(token).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)

	// The old secret is worthless even with a correct code — there is no
	// enrolment left to confirm.
	code, err := totp.GenerateCode(enroll.Secret, time.Now())
	s.Require().NoError(err)
	s.Client.External().WithSession(token).
		POST(s.T(), "/auth/mfa/enable", map[string]any{"code": code}).
		AssertStatus(s.T(), http.StatusBadRequest)
	_, enabled = s.totpState(userID)
	s.Require().False(enabled)

	// Enrolling again works, with a fresh secret and a fresh budget.
	secret := s.enrollAndEnable(token)
	s.Require().NotEqual(enroll.Secret, secret, "a re-enrolment must mint a new secret")
	enrolled, enabled = s.totpState(userID)
	s.Require().True(enrolled)
	s.Require().True(enabled)
}

// TestMfaEnable_RequiresALiveFullSession pins the auth checks the route
// performs for itself now that it carries no auth middleware (it cannot: a
// tenant implies a request transaction, and a budget charged inside one is
// rolled back with the 401 it answers). No cookie, a revoked cookie and a
// still-pending cookie must all be refused before anything is charged.
func (s *IdentitySuite) TestMfaEnable_RequiresALiveFullSession() {
	tenant := s.InsertTenant("mfa-enable-auth", "MFA Enable Auth Tenant")
	const password = "s3cret-password"
	email := "mfa-enable-auth@acme.com"
	s.InsertUserWithPassword(tenant, email, password)

	body := map[string]any{"code": wrongMFACode}

	s.Client.External().POST(s.T(), "/auth/mfa/enable", body).
		AssertStatus(s.T(), http.StatusUnauthorized)
	s.Client.External().WithSession(uuid.NewString()).POST(s.T(), "/auth/mfa/enable", body).
		AssertStatus(s.T(), http.StatusUnauthorized)

	token, _ := s.login(email, password)
	secret := s.enrollAndEnable(token)

	// A pending session must not be able to confirm an enrolment.
	pending, mfaRequired := s.login(email, password)
	s.Require().True(mfaRequired)
	s.Client.External().WithSession(pending).POST(s.T(), "/auth/mfa/enable", body).
		AssertStatus(s.T(), http.StatusUnauthorized)
	attempts, _, _ := s.sessionState(pending)
	s.Require().Equal(0, attempts, "a refusal before the charge must cost no budget")

	// A logged-out session is dead for this route too.
	s.Client.External().WithSession(token).POST(s.T(), "/auth/logout", nil).
		AssertStatus(s.T(), http.StatusNoContent)
	s.Client.External().WithSession(token).POST(s.T(), "/auth/mfa/enable", body).
		AssertStatus(s.T(), http.StatusUnauthorized)

	// Losing the tenant declaration must not have lost the CSRF guard with it:
	// the origin check wraps every external route, tenant or not, so a
	// cross-origin confirmation is refused before any credential is read.
	s.Client.External().WithOrigin("https://evil.example").
		POST(s.T(), "/auth/mfa/enable", body).
		AssertStatus(s.T(), http.StatusForbidden)

	// An account that already has MFA on is refused outright: there is no
	// pending enrolment to confirm, so no guessing loop can reach the secret
	// that is already protecting it (and the discard path can never turn it off).
	full, err := totp.GenerateCode(secret, time.Now())
	s.Require().NoError(err)
	fresh, _ := s.login(email, password)
	verify := s.Client.External().WithSession(fresh).
		POST(s.T(), "/auth/mfa/verify", map[string]any{"code": full})
	verify.AssertStatus(s.T(), http.StatusOK)
	live := sessionCookie(verify)
	s.Client.External().WithSession(live).POST(s.T(), "/auth/mfa/enable", body).
		AssertStatus(s.T(), http.StatusConflict)
}
