package domain

import (
	"context"
	"time"
)

// The invite's delivery seam.
//
// Until now an invite ended in the API RESPONSE: POST /members returned the
// cleartext token and a tenant administrator copied it into a chat window by
// hand. That is not a product — it is a manual step that leaks a live
// credential through whatever channel the admin happens to use, and it makes
// onboarding impossible to hand to a customer.
//
// So the invite use case now also QUEUES the mail that carries the link, and it
// does so on the invite's own transaction: the token row, the audit entry and
// the message commit together or not at all. There is no state in which an
// invite exists and nobody was ever going to be told, and none in which a link
// was mailed for an invite that rolled back.
//
// The port is declared HERE, on the consumer's side, for the reason
// taskinstances.AssigneeChecker is: identity states the narrow thing it needs
// ("deliver this invite"), and the notifications module — which owns the outbox
// and the templates — implements it. Identity therefore has no dependency on
// how, or whether, a message is eventually rendered and sent.

// InviteMail is one invitation to deliver.
type InviteMail struct {
	// TokenID is the invite_tokens row this link activates. It is the message's
	// identity: the enqueue is deduplicated on it, so retrying an invite that
	// already queued its mail queues nothing, while a RE-ISSUE — a new token —
	// is deliberately a new message.
	TokenID string
	// RawToken is the cleartext credential the link carries. It is written to
	// the outbox payload; see notifications' InvitePayload for what that
	// exposes and what bounds it.
	RawToken string
	// Email and Name address the invited member.
	Email string
	Name  string
	// ExpiresAt is when the credential dies, so the mail can say so.
	ExpiresAt time.Time
}

// InviteMailer queues an invitation on the caller's transaction.
//
// An implementation MUST NOT send anything itself and MUST NOT open its own
// transaction: the whole guarantee is that this write lives or dies with the
// invite that caused it.
type InviteMailer interface {
	EnqueueInviteMail(ctx context.Context, m InviteMail) error
}
