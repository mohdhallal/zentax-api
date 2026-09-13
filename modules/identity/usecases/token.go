package usecases

import (
	"context"
	"strings"
	"time"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// AuthenticateToken resolves a bearer API token to its (token, service-account
// user). Used by the RequireAuth middleware for machine callers. Deliberately
// generic errors — no token/account enumeration.
//
// ONLY REJECTIONS REACH THE SECURITY STREAM. An accepted token on every request
// is the machine equivalent of a session poll: it says nothing that the token's
// issuance (audited as api_token.issued) and its last_used stamp do not already
// say, and recording it would put one row per API call into a store an
// investigator has to be able to read. A rejection is the interesting half —
// somebody presenting a credential this deployment does not accept — and the
// reason class separates the two populations that matter: a client whose real
// token has expired or been revoked, and somebody guessing.
//
// THE ROWS CARRY AN ADDRESS, which they did not when the address was bound by
// each /auth handler that remembered to: this runs inside RequireAuth, which
// wraps EVERY route rather than only the identity ones, so none of those
// handlers is on the path and "where was this stolen credential being presented
// from" came back empty. It is bound once now, in the global chain
// (middlewares.ClientAddressMiddleware), which is why nothing here has to ask.
func (uc *UseCases) AuthenticateToken(ctx context.Context, raw string) (*domain.APIToken, *domain.User, error) {
	if !strings.HasPrefix(raw, domain.TokenPrefix) {
		// Not even shaped like one of ours. Not recorded: this is the
		// Authorization header of every client that guessed wrong about the
		// scheme, and a store that fills up with it is a store nobody reads.
		return nil, nil, apperrors.NewUnauthorized(domain.MsgTokenInvalid)
	}
	now := uc.now()

	token, err := uc.tokens.GetByTokenHash(ctx, crypto.HashToken(raw))
	if err != nil {
		return nil, nil, err
	}
	if token == nil {
		// A well-formed credential that matches no row: probing, or a token
		// from another deployment. Nothing about the presented secret is
		// recorded — not the value, not its hash: an append-only ledger cannot
		// unpublish a credential later, and a hash of a live token is a
		// verifier for it.
		uc.noteTokenRejected(ctx, securityevent.ReasonUnknownCredential, "", "", "")
		return nil, nil, apperrors.NewUnauthorized(domain.MsgTokenInvalid)
	}
	if !token.IsValid(now) {
		uc.noteTokenRejected(ctx, securityevent.ReasonCredentialExpired,
			token.TenantID, token.UserID, token.ID)
		return nil, nil, apperrors.NewUnauthorized(domain.MsgTokenInvalid)
	}

	user, err := uc.users.GetByID(ctx, token.UserID)
	if err != nil {
		return nil, nil, err
	}
	// Tokens authenticate machine principals only.
	if user == nil || !user.IsActive() || !user.IsService() {
		// The credential is real and live; the principal behind it is not
		// usable. This is the row that says a disabled service account is still
		// being called by something that has not been told.
		uc.noteTokenRejected(ctx, securityevent.ReasonPrincipalNotUsable,
			token.TenantID, token.UserID, token.ID)
		return nil, nil, apperrors.NewUnauthorized(domain.MsgTokenInvalid)
	}

	_ = uc.tokens.TouchLastUsed(ctx, token.ID, now) // best-effort usage stamp

	return token, user, nil
}

// noteTokenRejected records a refused bearer credential (ADR-0008 stream 2).
//
// tokenID is carried in SessionID: both name "the credential this event is
// about", and a machine's API token is what a session is to a person — which is
// also why one column serves both and neither needs a discriminator beyond
// `method`.
func (uc *UseCases) noteTokenRejected(ctx context.Context, reason, tenantID, principalID, tokenID string) {
	uc.note(ctx, securityevent.Event{
		Event:       securityevent.EventTokenRejected,
		Outcome:     securityevent.OutcomeFailure,
		Method:      securityevent.MethodAPIToken,
		Reason:      reason,
		TenantID:    tenantID,
		PrincipalID: principalID,
		SessionID:   tokenID,
	})
}

func time24h(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}
