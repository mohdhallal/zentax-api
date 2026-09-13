package securityevents_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// SecuritySuite proves ADR-0008 stream 2 end to end, against the real stack:
// the authentication events are actually written, by the use cases rather than
// by a handler, with the tenant and the actor optional; the address that was
// tried is correlatable and never stored; and the things that are NOT
// authentication events stay out of the store.
//
// The two cases the whole design turns on, and the two easiest to get wrong,
// each have their own test: a failed login for an address that does not exist
// (no tenant, no actor — the row audit_log cannot hold), and a lockout
// (recorded at the moment the lock is armed, not at the next request, and not
// at all when the threshold attempt turns out to be a successful login).
type SecuritySuite struct {
	acceptance.Suite
}

func TestSecuritySuite(t *testing.T) {
	suite.Run(t, new(SecuritySuite))
}

const (
	cookieName = "zentax_session"
	// Mirrors maxFailedAttempts in modules/identity/usecases/login.go.
	maxFailedAttempts = 5
)

func sessionCookie(resp *acceptance.TestResponse) string {
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c.Value
		}
	}
	return ""
}

// stream reads the authentication stream through its cross-tenant door — the
// same seam an investigator or a log shipper would use. There is no HTTP route
// onto this table, deliberately: the events belong to security ops, not to a
// tenant's Audit Trail page.
func (s *SecuritySuite) stream(f securityevent.Filter) []securityevent.Entry {
	s.T().Helper()
	exec := database.NewExec(s.DB)
	entries, err := securityevent.NewReader(exec, exec).List(s.T().Context(), f)
	s.Require().NoError(err)
	return entries
}

// digestOf computes the correlator for an address under the test key — exactly
// what an investigator does to ask "how many attempts hit this address".
func (s *SecuritySuite) digestOf(email string) string {
	s.T().Helper()
	key, err := config.AuthConfig{EncryptionKey: config.DevelopmentEncryptionKey}.DecodeEncryptionKey()
	s.Require().NoError(err)
	d, err := securityevent.NewDigest(key)
	s.Require().NoError(err)
	return d.Of(email)
}

// rawRow renders one whole stored row as text, so an assertion about what is
// NOT on it covers every column rather than the ones the reader selects. It
// goes through the stream door for the same reason everything else does: the
// table shows a connection that has asked for nothing exactly nothing.
func (s *SecuritySuite) rawRow(eventID string) string {
	s.T().Helper()
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.security_stream', 'on', true)`)
	s.Require().NoError(err)

	var raw string
	s.Require().NoError(tx.QueryRowx(
		`SELECT e::text FROM security_events e WHERE event_id = $1`, eventID).Scan(&raw))
	return raw
}

func (s *SecuritySuite) eventsOf(entries []securityevent.Entry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Event)
	}
	return names
}

func (s *SecuritySuite) countOf(entries []securityevent.Entry, event string) int {
	n := 0
	for _, e := range entries {
		if e.Event == event {
			n++
		}
	}
	return n
}

// THE CASE THE DOMAIN TRAIL CANNOT HOLD, and the first question of any breach
// investigation: somebody tried an address nobody here recognises.
//
// There is no tenant (no account was resolved) and no actor (the caller is an
// unidentified stranger), so audit_log — tenant-keyed RLS, NOT NULL actor_id —
// would refuse the row outright. It lands here, and it is still useful: the
// address is correlatable through a keyed digest, and the network address says
// where from. What is nowhere on the row is the address itself.
func (s *SecuritySuite) TestFailedLoginForAnAddressThatDoesNotExist() {
	stranger := "nobody-" + uuid.NewString() + "@example.invalid"

	s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": stranger, "password": "whatever"}).
		AssertStatus(s.T(), http.StatusUnauthorized)

	entries := s.stream(securityevent.Filter{})
	s.Require().Len(entries, 1, "one refusal, one event: %v", s.eventsOf(entries))

	got := entries[0]
	s.Require().Equal(securityevent.EventLoginFailed, got.Event)
	s.Require().Equal(securityevent.OutcomeFailure, got.Outcome)
	s.Require().Equal(securityevent.MethodPassword, got.Method)
	s.Require().Equal(securityevent.ReasonNoSuchPrincipal, got.Reason.String,
		"the response is one generic 401 for every shape; the class the caller is denied lives here")
	s.Require().False(got.TenantID.Valid, "an unrecognised address belongs to no tenant")
	s.Require().False(got.PrincipalID.Valid, "an unrecognised address names no account")

	// Correlatable by address without the store holding the address.
	s.Require().True(got.SubjectDigest.Valid)
	s.Require().Equal(s.digestOf(stranger), got.SubjectDigest.String)
	s.Require().Equal(s.digestOf(strings.ToUpper(stranger)), got.SubjectDigest.String,
		"a different spelling of one address must not become a second subject")

	// "From where" is answered.
	s.Require().True(got.ClientIP.Valid)
	s.Require().Equal("127.0.0.1", got.ClientIP.String)

	// And the address is nowhere in the row — checked against the whole row
	// rendered as text, not against the columns the reader happens to select.
	raw := s.rawRow(got.EventID)
	s.Require().NotContains(strings.ToLower(raw), strings.ToLower(stranger),
		"the submitted address must never be stored in the clear")
	s.Require().NotContains(strings.ToLower(raw), "example.invalid")
}

// THE LOCKOUT. It is recorded at the moment the account locks — from the
// statement that stamps locked_until — and not from the next refusal, because a
// lockout nobody probes again would otherwise leave no trace at all. It is
// recorded ONCE: the refusals that follow are login failures with the locked_out
// class, and a live lock that records itself again on every request it refuses
// would turn one incident into an alert storm.
func (s *SecuritySuite) TestLockoutIsRecordedWhenTheLockIsArmedAndOnlyOnce() {
	tenant := s.InsertTenant("sec-lockout", "Security Lockout")
	userID := s.InsertUserWithPassword(tenant, "lockout@acme.com", "s3cret-password")

	wrong := map[string]any{"email": "lockout@acme.com", "password": "not-my-password"}
	right := map[string]any{"email": "lockout@acme.com", "password": "s3cret-password"}

	for attempt := 1; attempt <= maxFailedAttempts; attempt++ {
		s.Client.External().POST(s.T(), "/auth/login", wrong).
			AssertStatus(s.T(), http.StatusUnauthorized)

		entries := s.stream(securityevent.Filter{PrincipalID: userID.String()})
		s.Require().Equal(attempt, s.countOf(entries, securityevent.EventLoginFailed))
		if attempt < maxFailedAttempts {
			s.Require().Zero(s.countOf(entries, securityevent.EventLockoutEngaged),
				"no lockout before the threshold")
		}
	}

	entries := s.stream(securityevent.Filter{PrincipalID: userID.String()})
	s.Require().Equal(1, s.countOf(entries, securityevent.EventLockoutEngaged),
		"the attempt that armed the lock records it, exactly once: %v", s.eventsOf(entries))

	lockout := s.stream(securityevent.Filter{Event: securityevent.EventLockoutEngaged})
	s.Require().Len(lockout, 1)
	s.Require().Equal(userID.String(), lockout[0].PrincipalID.String)
	s.Require().Equal(tenant.String(), lockout[0].TenantID.String)
	s.Require().Equal("127.0.0.1", lockout[0].ClientIP.String,
		"a lockout without the address that caused it answers half the question")
	s.Require().False(lockout[0].SubjectDigest.Valid,
		"a known account is correlated by id, never by a second pseudonym for the same person")

	// The lock is live. The CORRECT password is refused, in exactly the words a
	// wrong one gets — and the stream, which owes nobody that discretion, says
	// which refusal it was. Still one lockout event: a lock is not re-armed by
	// the requests it refuses.
	s.Client.External().POST(s.T(), "/auth/login", right).
		AssertStatus(s.T(), http.StatusUnauthorized)

	after := s.stream(securityevent.Filter{PrincipalID: userID.String()})
	s.Require().Equal(1, s.countOf(after, securityevent.EventLockoutEngaged),
		"a live lock must not record itself again on every refusal it serves")
	s.Require().Equal(maxFailedAttempts+1, s.countOf(after, securityevent.EventLoginFailed))
	s.Require().Equal(securityevent.ReasonLockedOut, after[0].Reason.String,
		"the newest refusal is the one the lock served")
}

// THE FALSE ALARM the lockout event is easy to raise. The threshold-th attempt
// ARMS the lock before the password is verified — that ordering is the whole
// concurrency defence — so an attempt that then turns out to be the right
// password logs in and immediately clears the stamp it just wrote. Nothing was
// locked out; the stream must not say otherwise, or the one signal an operator
// pages on fires on ordinary users who mistyped four times.
func (s *SecuritySuite) TestAThresholdAttemptThatSucceedsIsNotALockout() {
	tenant := s.InsertTenant("sec-nearmiss", "Security Near Miss")
	userID := s.InsertUserWithPassword(tenant, "nearmiss@acme.com", "s3cret-password")

	for range maxFailedAttempts - 1 {
		s.Client.External().POST(s.T(), "/auth/login",
			map[string]any{"email": "nearmiss@acme.com", "password": "not-my-password"}).
			AssertStatus(s.T(), http.StatusUnauthorized)
	}

	login := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": "nearmiss@acme.com", "password": "s3cret-password"})
	login.AssertStatus(s.T(), http.StatusOK)

	entries := s.stream(securityevent.Filter{PrincipalID: userID.String()})
	s.Require().Zero(s.countOf(entries, securityevent.EventLockoutEngaged),
		"the threshold attempt armed the lock and the success cleared it: nobody was locked out (%v)",
		s.eventsOf(entries))
	s.Require().Equal(1, s.countOf(entries, securityevent.EventLoginSucceeded))
	s.Require().Equal(maxFailedAttempts-1, s.countOf(entries, securityevent.EventLoginFailed))
}

// A session's BIRTH and DEATH are the events; its USE is not. A successful
// login names the session it minted (so the stream and the sessions table
// cannot disagree about who had access), polling /auth/me records nothing at
// all, and the logout closes the interval.
func (s *SecuritySuite) TestSessionLifecycleIsRecordedAndItsUseIsNot() {
	tenant := s.InsertTenant("sec-session", "Security Session")
	userID := s.InsertUserWithPassword(tenant, "session@acme.com", "s3cret-password")

	login := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": "session@acme.com", "password": "s3cret-password"})
	login.AssertStatus(s.T(), http.StatusOK)
	token := sessionCookie(login)
	s.Require().NotEmpty(token)

	entries := s.stream(securityevent.Filter{PrincipalID: userID.String()})
	s.Require().Len(entries, 1)
	s.Require().Equal(securityevent.EventLoginSucceeded, entries[0].Event)
	s.Require().Equal(tenant.String(), entries[0].TenantID.String)
	s.Require().True(entries[0].SessionID.Valid, "a login must name the session it minted")
	sessionID := entries[0].SessionID.String

	// It names the session that actually exists.
	var live string
	s.Require().NoError(s.DB.QueryRowx(
		`SELECT id::text FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&live))
	s.Require().Equal(live, sessionID)

	// Using the session is not an authentication event: a poll of /auth/me is
	// the same session that was already recorded when it was minted, and a store
	// with one row per request is one nobody can read.
	for range 3 {
		s.Client.External().WithSession(token).GET(s.T(), "/auth/me").AssertStatus(s.T(), http.StatusOK)
		s.Client.External().WithSession(token).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)
	}
	s.Require().Len(s.stream(securityevent.Filter{PrincipalID: userID.String()}), 1,
		"a successful poll of an existing session must not reach the stream")

	s.Client.External().WithSession(token).POST(s.T(), "/auth/logout", nil).
		AssertStatus(s.T(), http.StatusNoContent)

	after := s.stream(securityevent.Filter{PrincipalID: userID.String()})
	s.Require().Len(after, 2, "%v", s.eventsOf(after))
	s.Require().Equal(securityevent.EventSessionRevoked, after[0].Event)
	s.Require().Equal(securityevent.ReasonSelfService, after[0].Reason.String)
	s.Require().Equal(sessionID, after[0].SessionID.String,
		"the revocation must close the interval the login opened")

	// An expired or unknown cookie is the most common non-event in the product;
	// it must not become a row either.
	s.Client.External().WithSession(token).GET(s.T(), "/auth/me").
		AssertStatus(s.T(), http.StatusUnauthorized)
	s.Require().Len(s.stream(securityevent.Filter{PrincipalID: userID.String()}), 2,
		"a dead cookie is not an authentication event")
}

// The second factor: enrolling one, turning it on, and passing it. Enrolment is
// on the stream because a change to HOW an account authenticates is the classic
// account-takeover step and is invisible in a stream of sign-ins alone; the
// verification is there so that a login which never completed its second factor
// is visible as such.
func (s *SecuritySuite) TestSecondFactorLifecycle() {
	tenant := s.InsertTenant("sec-mfa", "Security MFA")
	userID := s.InsertUserWithPassword(tenant, "mfa@acme.com", "s3cret-password")

	login := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": "mfa@acme.com", "password": "s3cret-password"})
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

	// A wrong code on the re-login's pending session is a refusal the stream
	// classifies, while the response says only "invalid code".
	login2 := s.Client.External().POST(s.T(), "/auth/login",
		map[string]any{"email": "mfa@acme.com", "password": "s3cret-password"})
	login2.AssertStatus(s.T(), http.StatusOK)
	pending := sessionCookie(login2)

	s.Client.External().WithSession(pending).
		POST(s.T(), "/auth/mfa/verify", map[string]any{"code": "000000"}).
		AssertStatus(s.T(), http.StatusUnauthorized)

	code2, err := totp.GenerateCode(enroll.Secret, time.Now())
	s.Require().NoError(err)
	s.Client.External().WithSession(pending).
		POST(s.T(), "/auth/mfa/verify", map[string]any{"code": code2}).
		AssertStatus(s.T(), http.StatusOK)

	entries := s.stream(securityevent.Filter{PrincipalID: userID.String()})
	names := s.eventsOf(entries)
	s.Require().Equal(1, s.countOf(entries, securityevent.EventMFAEnrolled), "%v", names)
	s.Require().Equal(1, s.countOf(entries, securityevent.EventMFAEnabled), "%v", names)
	s.Require().Equal(1, s.countOf(entries, securityevent.EventMFAFailed), "%v", names)
	s.Require().Equal(1, s.countOf(entries, securityevent.EventMFASucceeded), "%v", names)
	s.Require().Equal(2, s.countOf(entries, securityevent.EventLoginSucceeded), "%v", names)

	for _, e := range entries {
		if e.Event == securityevent.EventMFAFailed {
			s.Require().Equal(securityevent.ReasonBadCredential, e.Reason.String)
			s.Require().Equal(tenant.String(), e.TenantID.String)
		}
	}
}

// Disabling a member ends every live credential it holds, and that is an
// INCIDENT TIMELINE fact as well as an access-review one. It is the single
// event in the stream where the principal and the actor are different people —
// which is why the table carries both.
func (s *SecuritySuite) TestDisablingAMemberEndsItsAccessOnTheStream() {
	tenant := s.InsertTenant("sec-disable", "Security Disable").String()
	admin := s.As(tenant)

	var issued struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
	}
	invited := admin.POST(s.T(), "/members", map[string]any{
		"email": "leaver-" + uuid.NewString() + "@acme.com", "name": "Leaver", "role": "preparer",
	})
	invited.AssertStatus(s.T(), http.StatusCreated)
	invited.DecodeData(s.T(), &issued)
	s.Require().NotEmpty(issued.Member.ID)

	admin.PUT(s.T(), "/members/"+issued.Member.ID, map[string]any{"name": "Leaver", "status": "disabled"}).
		AssertStatus(s.T(), http.StatusOK)

	entries := s.stream(securityevent.Filter{PrincipalID: issued.Member.ID})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	got := entries[0]
	s.Require().Equal(securityevent.EventSessionsRevoked, got.Event)
	s.Require().Equal(securityevent.ReasonMemberDisabled, got.Reason.String)
	s.Require().Equal(tenant, got.TenantID.String)
	s.Require().True(got.ActorID.Valid, "somebody else ended this principal's access; the stream must name them")
	s.Require().NotEqual(issued.Member.ID, got.ActorID.String)
}

// A bearer credential this deployment does not accept is the machine half of a
// failed login. Only the REJECTION is recorded — an accepted token on every
// request is the machine equivalent of a session poll — and nothing about the
// presented secret reaches the row, not even a hash of it: an append-only
// ledger cannot unpublish a verifier for a live credential later.
func (s *SecuritySuite) TestARejectedApiTokenIsRecordedAndTheSecretIsNot() {
	bogus := "ztx_" + uuid.NewString() + uuid.NewString()

	s.Client.External().WithBearer(bogus).GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusUnauthorized)

	entries := s.stream(securityevent.Filter{Event: securityevent.EventTokenRejected})
	s.Require().Len(entries, 1)
	got := entries[0]
	s.Require().Equal(securityevent.OutcomeFailure, got.Outcome)
	s.Require().Equal(securityevent.MethodAPIToken, got.Method)
	s.Require().Equal(securityevent.ReasonUnknownCredential, got.Reason.String)
	s.Require().False(got.PrincipalID.Valid)
	s.Require().False(got.TenantID.Valid)

	s.Require().NotContains(s.rawRow(got.EventID), strings.TrimPrefix(bogus, "ztx_"),
		"the presented credential must not be written down anywhere")

	// A header that is not one of ours at all is not recorded: that is the
	// Authorization line of every client that guessed the scheme wrong, and a
	// store full of it is a store nobody reads.
	s.Client.External().WithBearer("Basic-ish nonsense").GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusUnauthorized)
	s.Require().Len(s.stream(securityevent.Filter{Event: securityevent.EventTokenRejected}), 1)
}
