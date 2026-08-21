package domain

import "context"

// IdentityBroker is the seam for federated identity (SSO/SCIM) per ADR-0011:
// SaaS → WorkOS, self-host → a bundled OSS broker / direct OIDC. This is a
// Phase-2 concern — first-party email/password + sessions ship first, so there
// is no implementation yet. The interface exists so the login flow can branch to
// a broker without a rewrite when SSO lands.
type IdentityBroker interface {
	// StartSSO begins a federated login for a connection and returns the IdP
	// redirect URL. The app still mints its own session on callback.
	StartSSO(ctx context.Context, connection string) (redirectURL string, err error)
}
