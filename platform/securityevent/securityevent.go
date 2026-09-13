// Package securityevent implements ADR-0008 stream 2: the authentication event
// stream. It answers the question a breach investigation opens with — who got
// in, when, from where, by what method, and what happened — and it is a
// SEPARATE store from the domain audit trail for one structural reason.
//
// WHY NOT THE DOMAIN TRAIL. platform/audit requires a tenant (its RLS append
// policy keys on app.tenant_id) and an actor (audit_log.actor_id is NOT NULL).
// A failed login for an address nobody recognises has neither: there is no
// tenant, because no account was resolved, and no actor, because the caller is
// an unidentified stranger. That is exactly the event that matters most, so
// "just add auth actions to the trail" is not a smaller version of this — it is
// a stream that cannot hold its own most important row. Here both are optional.
//
// THE SUBMITTED ADDRESS IS NEVER STORED. An address that resolves to no account
// is an unauthenticated stranger's personal data, and this ledger is append-only
// — it cannot be redacted afterwards and it is not the erasure boundary. So an
// identified attempt correlates by PrincipalID; an unidentified one correlates
// by a KEYED digest of the address (see digest.go), which lets an investigator
// count attempts against one address without the store ever holding it. The
// column is CHAR(64) with a hex CHECK, so "never in the clear" is enforced by
// the schema rather than by this comment.
//
// THE ADDRESS IS THE CLIENT'S, NOT THE LOAD BALANCER'S. In every deployment
// that matters the API has no public surface — CloudFront -> ALB -> web -> api
// in a cell, web container -> api in the self-host stack — so the socket peer
// answers "from where" with the name of our own infrastructure on every row,
// and the one question this stream exists for becomes unanswerable in exactly
// the deployments it was built for. The caller is therefore resolved through
// the configured trusted-proxy chain (delivery/httpkit/clientaddr, the same
// resolver the rate limiter keys its budgets on, which believes a forwarded
// header only from a peer the operator named), bound once by a global
// middleware, and stored WITH ITS PROVENANCE — see client_ip_source in
// context.go, which is how a reader tells a TCP-verified peer from a header a
// proxy wrote from our own hop standing in for a client it never named.
//
// THE NETWORK ADDRESS IS STORED, IN FULL, DELIBERATELY. It is personal data
// (ADR-0015 removed it from the request log for exactly that reason), and a
// security stream that cannot say "from where" cannot serve the incident it
// exists for: a truncated prefix identifies neither the host to block nor the
// row to correlate against a load-balancer or VPC flow log. The control is
// therefore RETENTION and ACCESS, not obfuscation — security_events is range
// partitioned by month precisely so the stream (and the address with it) ages
// out by dropping whole partitions, which is the only deletion an append-only
// table admits.
//
// WHAT IS NOT AN EVENT HERE. Volume is a security property of this store: an
// unauthenticated caller must not be able to write to it without limit, and a
// stream that records everything is one nobody reads.
//   - A SHED request (HTTP 429) is not an authentication event. It never reached
//     a credential check, so it says nothing about who tried what; the limiter
//     already counts and logs it, and recording it here would hand an
//     unauthenticated party a row-per-request amplifier into a durable store.
//   - RESOLVING a session cookie is not an authentication event, in either
//     direction. A successful poll of /auth/me is the same session that was
//     already recorded when it was minted, and an expired cookie is the single
//     most common non-event in the product. The session's BIRTH and DEATH are
//     the events; its use is not.
//
// WHERE THE EVENTS ARE WIRED. At the use cases in modules/identity, never in the
// handlers — a future route, a CLI, or a second delivery mechanism reaching the
// same use case records the same event, and cannot bypass it by not knowing to.
package securityevent

import (
	"context"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// The event vocabulary. It is closed: Record refuses a name that is not here,
// because an unrecognised event is one an alert will never fire on and a query
// will never find.
const (
	// EventLoginSucceeded — a password login that minted a session. The session
	// may still be mfa_pending; EventMFASucceeded says whether the second factor
	// followed.
	EventLoginSucceeded = "auth.login.succeeded"
	// EventLoginFailed — any refusal of /auth/login, whatever the shape. The
	// response is one generic 401 for every case on purpose (see the enumeration
	// policy in modules/identity/usecases/login.go); Reason is where the
	// operator gets the distinction the caller is denied.
	EventLoginFailed = "auth.login.failed"
	// EventLockoutEngaged — the attempt that ARMED the lockout, recorded once,
	// at the moment the account locked. Not the same as a refusal while the lock
	// runs (that is EventLoginFailed with ReasonLockedOut): a lockout nobody
	// probes again would otherwise leave no trace at all.
	EventLockoutEngaged = "auth.lockout.engaged"

	// EventMFAEnrolled — a TOTP secret was issued (not yet confirmed).
	EventMFAEnrolled = "auth.mfa.enrolled"
	// EventMFAEnabled — an enrolment was confirmed; the account now has a second
	// factor. A change to how an account authenticates is a security event
	// whether or not anyone signed in.
	EventMFAEnabled = "auth.mfa.enabled"
	// EventMFAEnrollmentDiscarded — a pending enrolment was destroyed after its
	// confirmation budget was spent.
	EventMFAEnrollmentDiscarded = "auth.mfa.enrollment_discarded"
	// EventMFASucceeded — a pending session completed its second factor.
	EventMFASucceeded = "auth.mfa.succeeded"
	// EventMFAFailed — a second-factor attempt was refused.
	EventMFAFailed = "auth.mfa.failed"

	// EventSessionRevoked — one session ended (a logout).
	EventSessionRevoked = "auth.session.revoked"
	// EventSessionsRevoked — every session of a principal ended at once: the
	// principal's own "log out everywhere", or an administrator disabling the
	// account. Reason separates the two, and ActorID names the administrator.
	EventSessionsRevoked = "auth.sessions.revoked"

	// EventTokenRejected — a bearer API token was refused. Only the REJECTION is
	// recorded: an accepted token on every request is the machine equivalent of
	// a session poll, and recording it would bury the stream.
	EventTokenRejected = "auth.token.rejected"

	// EventInviteAccepted — an invitation was redeemed: an account that had no
	// password now has one and is active. /auth/accept-invite is the only PUBLIC,
	// unauthenticated route in the product that ESTABLISHES a credential, so
	// this is the birth certificate of every human principal that is not the
	// founding administrator. A stream that records how credentials are USED but
	// not how they come into existence cannot answer "when did this account
	// become able to sign in, and from where".
	EventInviteAccepted = "auth.invite.accepted"
	// EventInviteRejected — a redemption was refused. Every refusal answers the
	// caller with one generic 400 (see AcceptInvite's enumeration policy), so
	// Reason is the only place the operator learns whether somebody is guessing
	// invite tokens, whether a real invitation ran out its clock, or whether a
	// token was presented for an account that can no longer use it.
	EventInviteRejected = "auth.invite.rejected"
)

// Outcomes. Mirrored by a CHECK on the column.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// Methods — what credential was presented. Mirrored by a CHECK on the column.
const (
	MethodPassword = "password"
	MethodTOTP     = "totp"
	MethodSession  = "session"
	MethodAPIToken = "api_token"
	// MethodInviteToken — a single-use "zti_" invitation token, the credential
	// presented at /auth/accept-invite. Its own method rather than one of the
	// four above because it is the only one that is spent rather than reused,
	// and because an access review that asks "how did this principal first get
	// in" must be able to select on it.
	MethodInviteToken = "invite_token"
)

// Reason classes. A CLASS, never a message and never caller text: this is what
// the generic refusal withholds from the caller and owes the operator.
const (
	// ReasonNoSuchPrincipal — the address resolved to no account at all. The
	// row carries SubjectDigest and nothing else identifying.
	ReasonNoSuchPrincipal = "no_such_principal"
	// ReasonNotLoginCapable — the account exists but cannot authenticate
	// interactively: disabled, invited without a password yet, or a service
	// account (machines use API tokens).
	ReasonNotLoginCapable = "not_login_capable"
	// ReasonBadCredential — the credential was verified and was wrong.
	ReasonBadCredential = "bad_credential"
	// ReasonLockedOut — refused because the account's attempt budget was already
	// spent. The stored hash was never verified (verifying it while locked is
	// what made the lock a password oracle).
	ReasonLockedOut = "locked_out"
	// ReasonChargeFailed — the attempt could not be charged against the budget,
	// so it was refused rather than admitted uncharged (fail closed).
	ReasonChargeFailed = "charge_failed"
	// ReasonBudgetSpent — a second-factor budget ran out; what was being guessed
	// was destroyed with it.
	ReasonBudgetSpent = "budget_spent"
	// ReasonNoPendingSession — a second-factor code arrived for a session that
	// is not pending, has expired, or no longer exists.
	ReasonNoPendingSession = "no_pending_session"
	// ReasonNotEnrolled — a confirmation arrived with no pending enrolment.
	ReasonNotEnrolled = "not_enrolled"
	// ReasonSelfService — the principal ended its own access.
	ReasonSelfService = "self_service"
	// ReasonMemberDisabled — an administrator disabled the account, which ends
	// every live credential it holds.
	ReasonMemberDisabled = "member_disabled"
	// ReasonUnknownCredential — the presented token matched no row (probing).
	ReasonUnknownCredential = "unknown_credential"
	// ReasonCredentialExpired — the token was real and is past its life, or has
	// been revoked. Told apart from the unknown case on purpose: one is a client
	// that needs a new credential, the other is somebody guessing.
	ReasonCredentialExpired = "credential_expired"
	// ReasonPrincipalNotUsable — the credential resolved, but the principal
	// behind it cannot use it (inactive, or not a service account).
	ReasonPrincipalNotUsable = "principal_not_usable"
)

var (
	knownEvents = map[string]struct{}{
		EventLoginSucceeded: {}, EventLoginFailed: {}, EventLockoutEngaged: {},
		EventMFAEnrolled: {}, EventMFAEnabled: {}, EventMFAEnrollmentDiscarded: {},
		EventMFASucceeded: {}, EventMFAFailed: {},
		EventSessionRevoked: {}, EventSessionsRevoked: {},
		EventTokenRejected:  {},
		EventInviteAccepted: {}, EventInviteRejected: {},
	}
	knownOutcomes = map[string]struct{}{OutcomeSuccess: {}, OutcomeFailure: {}}
	knownMethods  = map[string]struct{}{
		MethodPassword: {}, MethodTOTP: {}, MethodSession: {}, MethodAPIToken: {},
		MethodInviteToken: {},
	}
	knownReasons = map[string]struct{}{
		ReasonNoSuchPrincipal: {}, ReasonNotLoginCapable: {}, ReasonBadCredential: {},
		ReasonLockedOut: {}, ReasonChargeFailed: {}, ReasonBudgetSpent: {},
		ReasonNoPendingSession: {}, ReasonNotEnrolled: {},
		ReasonSelfService: {}, ReasonMemberDisabled: {},
		ReasonUnknownCredential: {}, ReasonCredentialExpired: {}, ReasonPrincipalNotUsable: {},
	}
)

// Resolution is the resolution security_events.occurred_at (TIMESTAMPTZ) keeps.
// The writer truncates to it so a stamped instant IS the stored instant — the
// same discipline platform/audit had to learn the hard way.
const Resolution = time.Microsecond

// Event is one authentication event, as its call site knows it.
//
// Everything about WHO is optional, which is the point of the whole package.
type Event struct {
	// Event, Outcome and Method are required and come from the constants above.
	Event   string
	Outcome string
	Method  string
	// Reason is the refusal class; empty for a success.
	Reason string

	// TenantID is the tenant the principal belongs to, when one was resolved.
	TenantID string
	// PrincipalID is WHOSE access this event is about: the account
	// authenticating, or the account whose sessions ended.
	PrincipalID string
	// ActorID is who CAUSED it, when that is somebody other than the principal
	// (an administrator disabling a member). Empty for a self-service act.
	ActorID string
	// SessionID names the session the event is about, where there is one.
	SessionID string

	// Subject is the address the caller SUBMITTED. It is hashed by the recorder
	// and NEVER stored, and it is ignored entirely when PrincipalID is set — an
	// account named by id needs no second pseudonym for the same person.
	Subject string
}

// validate refuses an event this build cannot classify. These are programming
// errors, not runtime conditions: a name outside the vocabulary would be a row
// no alert fires on and no query finds.
func (e Event) validate() error {
	if _, ok := knownEvents[e.Event]; !ok {
		return fmt.Errorf("securityevent: unknown event %q", e.Event)
	}
	if _, ok := knownOutcomes[e.Outcome]; !ok {
		return fmt.Errorf("securityevent: unknown outcome %q for %s", e.Outcome, e.Event)
	}
	if _, ok := knownMethods[e.Method]; !ok {
		return fmt.Errorf("securityevent: unknown method %q for %s", e.Method, e.Event)
	}
	if e.Reason != "" {
		if _, ok := knownReasons[e.Reason]; !ok {
			return fmt.Errorf("securityevent: unknown reason %q for %s", e.Reason, e.Event)
		}
	}
	if e.Outcome == OutcomeFailure && e.Reason == "" {
		return fmt.Errorf("securityevent: %s is a failure with no reason class", e.Event)
	}
	return nil
}

// Recorder appends events. A nil *Recorder is a valid no-op, so use cases take
// it as an optional dependency (unit tests construct without one; the
// composition root always injects it).
type Recorder struct {
	db     database.ExecerPg
	digest *Digest
	now    func() time.Time
}

func NewRecorder(db database.ExecerPg) *Recorder {
	return &Recorder{db: db, now: time.Now}
}

// WithDigest injects the keyed subject digest. Without one an unidentified
// attempt is still recorded — it simply carries no correlator, which is worse
// than a pseudonym and better than nothing.
func (r *Recorder) WithDigest(d *Digest) *Recorder {
	if r != nil {
		r.digest = d
	}
	return r
}

// WithClock overrides the clock. For tests only.
func (r *Recorder) WithClock(now func() time.Time) *Recorder {
	if r != nil && now != nil {
		r.now = now
	}
	return r
}

const insertEvent = `
	INSERT INTO security_events
	    (occurred_at, event, outcome, method, reason,
	     tenant_id, principal_id, actor_id, session_id,
	     subject_digest, client_ip, client_ip_source, request_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::inet, $12, $13)`

// Record appends one event and returns the error, if any.
//
// It writes on whatever the context resolves to: the ambient request
// transaction when there is one — so an event can commit or roll back WITH the
// change it describes — and the pool otherwise. The public /auth routes
// deliberately declare no transaction, so their refusals are written in
// autocommit and survive the 401 that follows; a request transaction is rolled
// back by the tx middleware on any status >= 400, which would discard exactly
// the events a refusal owes.
//
// Use Note for anything that must not be able to fail its caller.
func (r *Recorder) Record(ctx context.Context, e Event) error {
	if r == nil {
		return nil
	}
	if err := e.validate(); err != nil {
		return err
	}

	// The digest exists for the unidentified case only. A principal named by id
	// is already correlated, and hashing its address as well would put a second
	// pseudonym for the same person in an append-only store for no answer.
	var digest *string
	if e.PrincipalID == "" {
		if d := r.digest.Of(e.Subject); d != "" {
			digest = &d
		}
	}

	addr := clientAddrOf(ctx)
	source := clientSourceOf(ctx)
	requestID := requestUUID(ctx)

	_, err := r.db.ExecContext(ctx, insertEvent,
		r.stamp(), e.Event, e.Outcome, e.Method, nilIfEmpty(e.Reason),
		nilIfEmpty(e.TenantID), nilIfEmpty(e.PrincipalID), nilIfEmpty(e.ActorID), nilIfEmpty(e.SessionID),
		digest, addr, source, requestID)
	if err != nil {
		return fmt.Errorf("securityevent: append %s: %w", e.Event, err)
	}
	return nil
}

// Note records an event that must not be able to fail its caller, and is how
// almost every call site uses this package.
//
// THE ASYMMETRY IS DELIBERATE, and it is the opposite of platform/audit's rule.
// A domain mutation must not commit without its evidence, so audit failures fail
// the transaction. An authentication REFUSAL is not like that: it has already
// happened by the time it is recorded, the attempt has already been charged
// against the budget, and turning "the event store hiccuped" into a 500 would
// both undo nothing and hand an attacker a distinguishable answer on a path
// whose whole defence is that every refusal reads alike. So a failure here is
// logged loudly and the refusal stands.
//
// The one call site that does NOT use Note is the successful login: it records
// on the same transaction that mints the session, so a session cannot exist
// without the record that it was minted.
//
// The write runs on a context the client cannot cancel, for the reason the
// lockout writes do: a caller that hangs up after its last wrong password must
// not thereby erase the evidence of it.
func (r *Recorder) Note(ctx context.Context, e Event) {
	if r == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if err := r.Record(ctx, e); err != nil {
		// No address, no digest, no reason to name a person: the identifiers
		// here are ids and code constants, which is what ADR-0015 allows a log
		// line to carry.
		logger.Log.WithContext(ctx).Error(
			"securityevent: an authentication event could not be recorded — the stream has a hole",
			logger.String("event", e.Event),
			logger.String("outcome", e.Outcome),
			logger.String("principalId", e.PrincipalID),
			logger.Error(err))
	}
}

// stamp is the instant an event is recorded at, truncated to what the column
// stores so the value read back is the value written.
func (r *Recorder) stamp() time.Time {
	now := r.now
	if now == nil {
		now = time.Now
	}
	return now().UTC().Truncate(Resolution)
}

// requestUUID is the ambient correlation id, but only when it is a UUID.
//
// The request id is CALLER-SUPPLIED whenever an X-Request-Id header is present
// (middlewares.RequestIdMiddleware honours it), so passing it through unchecked
// would be the one door by which arbitrary caller text could reach an
// append-only store. The column is UUID for the same reason; this is the check
// that keeps the insert from failing on a header somebody made up.
func requestUUID(ctx context.Context) *string {
	id := app.GetRequestId(ctx)
	if !isUUID(id) {
		return nil
	}
	return &id
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isUUID reports whether s is a canonical 8-4-4-4-12 hex UUID. Hand-rolled
// rather than uuid.Parse because uuid.Parse also accepts urn: and braced forms,
// and this is a gate on what may enter the store, not a parser.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
