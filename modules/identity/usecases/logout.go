package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// Logout revokes the current session. A missing session is a no-op (already gone).
//
// The revocation is recorded on the security stream (ADR-0008 stream 2): a
// session's birth and its death are the two events that bound "who had access,
// and until when", and an incident timeline built from logins alone would show
// every session as still live. The no-op case records nothing — there was no
// session, so nothing ended.
func (uc *UseCases) Logout(ctx context.Context, sessionToken string) error {
	session, err := uc.sessions.GetByTokenHash(ctx, crypto.HashToken(sessionToken))
	if err != nil {
		return err
	}
	if session == nil {
		return nil
	}
	if err := uc.sessions.Revoke(ctx, session.ID); err != nil {
		return err
	}
	uc.note(ctx, securityevent.Event{
		Event:       securityevent.EventSessionRevoked,
		Outcome:     securityevent.OutcomeSuccess,
		Method:      securityevent.MethodSession,
		Reason:      securityevent.ReasonSelfService,
		TenantID:    session.TenantID,
		PrincipalID: session.UserID,
		SessionID:   session.ID,
	})
	return nil
}

// LogoutAll revokes every session for a user ("log out everywhere"). The count
// is dropped on purpose: the principal is ending its OWN sessions, which is
// operational hygiene, not the access-review event a disable is — that path
// records it (member.credentials_revoked).
//
// It IS a security event, though, and for the reason the count is not needed:
// "everything this principal held stopped working at this instant" is exactly
// the line an incident timeline needs, whether the principal did it because a
// laptop went missing or because an attacker wanted the owner's other sessions
// gone.
func (uc *UseCases) LogoutAll(ctx context.Context, userID string) error {
	if _, err := uc.sessions.RevokeAllForUser(ctx, userID); err != nil {
		return err
	}
	uc.note(ctx, securityevent.Event{
		Event:       securityevent.EventSessionsRevoked,
		Outcome:     securityevent.OutcomeSuccess,
		Method:      securityevent.MethodSession,
		Reason:      securityevent.ReasonSelfService,
		TenantID:    app.GetTenantID(ctx),
		PrincipalID: userID,
	})
	return nil
}
