package domain

import "context"

// IdentityBroker is the seam for delegated identity per ADR-0011: SaaS →
// WorkOS, self-host → a bundled OSS broker (Keycloak / Authentik / Dex) or
// direct OIDC. It is a Phase-2 concern — first-party email/password + server
// side sessions ship first — so the only implementation today is
// NotConfiguredBroker. The interface exists so the flows that will delegate can
// branch to a broker without a rewrite, and so the shape of what the broker
// owns is written down here instead of being discovered when it lands.
//
// ADR-0011's ownership split holds for every method on it: the APP owns the
// user record, the session and MFA policy; the BROKER owns federation,
// directory sync, and the self-service credential-recovery challenge (assigned
// to the broker by ADR-0011 decision 7 — see StartRecovery). Nothing here returns
// a session, a password or an API token: a broker proves something about a
// subject, and the app decides what that is worth.
type IdentityBroker interface {
	// StartSSO begins a federated login for a connection and returns the IdP
	// redirect URL. The app still mints its own session on callback.
	StartSSO(ctx context.Context, connection string) (redirectURL string, err error)

	// StartRecovery begins a self-service credential recovery for an address —
	// the "I forgot my password" / "I lost my authenticator" entry point. The
	// broker owns the challenge end to end: it decides how control of the
	// address is proven, mints the single-use token, and delivers it. A
	// broker-hosted flow returns the page to send the browser to; a broker that
	// delivers out of band (e-mail) returns "" with a nil error, so redirectURL
	// being empty is a normal outcome and not a failure.
	//
	// It MUST be enumeration-safe: an unknown, disabled or SSO-only address has
	// to answer exactly as a recoverable one does, so that no caller can turn
	// the result into "this account exists" — the same policy the credential
	// messages in error_messages.go already follow. A nil error therefore means
	// "the challenge was accepted for processing", never "this address has an
	// account here".
	StartRecovery(ctx context.Context, email string) (redirectURL string, err error)

	// CompleteRecovery redeems the token a StartRecovery challenge issued and
	// returns the identity whose control it proves — that, and nothing else.
	// The token is single-use: a spent, expired, unknown or tampered one is an
	// error, and repeating a successful call fails.
	//
	// What happens next is the APP's work and stays the app's under ADR-0011
	// decisions 2 and 5. On a proof it accepts, the app: applies the credential
	// change to its own user record (a broker never holds the password hash or
	// the TOTP seed); mints its OWN session, exactly as the first-party login
	// path does; and revokes everything the lost credential could still reach —
	// every existing session (SessionRepository.RevokeAllForUser) and every
	// live API token of the principal (TokenRepository.RevokeAllForUser). A
	// recovery that leaves the old sessions alive has recovered nothing: the
	// attacker who prompted it keeps the access the reset was meant to take
	// away.
	CompleteRecovery(ctx context.Context, token string) (*RecoveredIdentity, error)
}

// RecoveredIdentity is what a completed recovery proves, and the whole of it:
// the broker verified control of this address. It deliberately carries no
// credential, no session and no authorisation — see CompleteRecovery for what
// the app does with it.
type RecoveredIdentity struct {
	// Email is the address whose control was proven, normalised by the broker.
	// The app resolves it against its own users; an address with no user here
	// is not an error at this seam, it is a no-op at the caller.
	Email string
	// Subject is the broker's stable identifier for the identity, or "" when
	// the challenge had none (a plain e-mail round trip). Worth storing to
	// correlate, never to authorise: tenant and role stay the app's own
	// decision (ADR-0001), never a claim read off the broker.
	Subject string
}

// NoIdentityProviderError is the typed refusal every IdentityBroker operation
// gives when the deployment has no provider wired — which is every deployment
// today, SaaS included, because the broker is Phase 2 (ADR-0011).
//
// It reports a configuration state, not a fault, and is typed so that one
// answer can be written once instead of per caller. It implements IsNotFound,
// which is the interface delivery/httpkit/httperr.Classify duck-types on, so a
// future route that simply returns it answers a plain 404 carrying
// MsgNoIdentityProvider — "this deployment has no such flow" — rather than the
// 500 plus stack trace an unrecognised error earns. Nothing about the subject
// reaches the response: the answer depends only on how the server is
// configured.
type NoIdentityProviderError struct{}

func (NoIdentityProviderError) Error() string    { return MsgNoIdentityProvider }
func (NoIdentityProviderError) IsNotFound() bool { return true }

// ErrNoIdentityProvider is the single value every NotConfiguredBroker operation
// returns. Test for it with errors.Is — the type is an empty struct, so any
// instance of it compares equal and a wrapped one still matches — and never by
// comparing the message.
//
// It is what separates "there was no broker to ask" from a broker that was
// reached and failed, which is the distinction a caller has to keep: the first
// is the steady state of an unconfigured edition and must not be logged, paged
// or retried as an incident; the second is a real dependency failure. Treating
// any non-nil error from the seam as the latter would turn every deployment
// into a permanent alarm.
var ErrNoIdentityProvider error = NoIdentityProviderError{}

// NotConfiguredBroker is the IdentityBroker every edition runs today: it
// refuses every operation with ErrNoIdentityProvider and does nothing else —
// no I/O, no logging, no partial work, so it is safe as a zero value and safe
// to share.
//
// It exists so the seam has one honest implementation instead of a nil
// interface each caller has to remember to nil-check, and so the "not
// configured" answer is written in one place. It is deliberately NOT wired into
// a route or a handler: there is no SSO endpoint and no recovery endpoint yet
// (ADR-0011), and adding either is its own decision, not a side effect of
// naming the seam.
type NotConfiguredBroker struct{}

// Compile-time proof the zero value satisfies the seam.
var _ IdentityBroker = NotConfiguredBroker{}

func (NotConfiguredBroker) StartSSO(context.Context, string) (string, error) {
	return "", ErrNoIdentityProvider
}

func (NotConfiguredBroker) StartRecovery(context.Context, string) (string, error) {
	return "", ErrNoIdentityProvider
}

func (NotConfiguredBroker) CompleteRecovery(context.Context, string) (*RecoveredIdentity, error) {
	return nil, ErrNoIdentityProvider
}
