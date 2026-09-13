package domain

import (
	"time"
)

// InvitePayload is the frozen value set of the member.invited template: what an
// invited person needs to activate their account, and nothing else.
//
// THE RAW TOKEN IS IN THIS PAYLOAD, WHICH IS UNAVOIDABLE — SO IT IS SEALED AT
// REST AND CLEARED ON DELIVERY.
//
// invite_tokens stores only a SHA-256 of the token precisely so a leaked table
// cannot be replayed; a mailed link, however, has to contain the cleartext, and
// an outbox exists exactly so that the cleartext survives the commit that
// minted it until the runner can send it. No outbox can avoid this: the only
// alternatives are sending inline from the request (which loses the mail on any
// provider hiccup, and mails invites that then roll back) or storing something
// the renderer cannot turn back into a link.
//
// The exposure is therefore bounded four ways rather than tolerated:
//
//   - AT REST IT IS CIPHERTEXT. platform/outbox seals the payload with the
//     application's encryption key before the INSERT and opens it in the
//     delivery loop just before the transport call, so a backup, a replica or a
//     support query yields an envelope rather than a working link — the same
//     protection ADR-0006 gives users.totp_secret_enc.
//   - IT STOPS EXISTING WHEN THE MAIL GOES OUT. Settling clears the payload on
//     the statement that settles the row (migration 20260913000028 lets the
//     state-only guard do exactly that and nothing else), so the window is
//     bounded by delivery — not by the 30-day prune window, and not by the
//     token's own lifetime.
//   - IT NEVER REACHES A LOG. The mail adapters record a template name and byte
//     counts; the ADR-0015 build gate fails a compile on a payload, a rendered
//     body or a token-shaped literal at a log call site.
//   - AND IT IS STILL single-use, still expires in InviteTTL, and is still
//     enqueued on the invite's own transaction, so a token that never committed
//     never reaches this payload at all.
type InvitePayload struct {
	// RecipientName is the display name the invite was created with, for the
	// greeting. It is personal data the `recipient` column does not carry (an
	// address is not a name), which is one more reason the payload is sealed
	// rather than only the credential inside it.
	RecipientName string `json:"recipientName"`
	// InviteToken is the cleartext "zti_…" credential. The template builds the
	// activation link from it (the accept-invite route and its query parameter
	// are the SPA's shape, which is the mail layer's to know — see
	// mail.Brand.Link).
	InviteToken string `json:"inviteToken"`
	// ExpiresAt is the instant the credential dies, so the mail can say so.
	// UTC, per ADR-0003 — it is an instant, not a legal date.
	ExpiresAt time.Time `json:"expiresAt"`
}
