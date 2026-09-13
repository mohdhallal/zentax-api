// Package usecases holds the notification producers: the code that decides a
// human is owed a message and writes it to the transactional outbox. Nothing
// here sends anything — delivery is the scheduled runner's job, and keeping the
// two apart is what makes a mutation and its notification impossible to
// disagree.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"

	identitydomain "github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	"github.com/mohamadhallal/zentax-api/platform/outbox"
)

// Notifier is the event-driven producer: a mutation happened, and somebody has
// to be told. Today that is one event — a member was invited — and the shape is
// deliberately one small method per event rather than a generic "send" that
// every caller would have to assemble a payload for.
type Notifier struct {
	outbox domain.Enqueuer
}

var _ identitydomain.InviteMailer = (*Notifier)(nil)

func NewNotifier(outbox domain.Enqueuer) *Notifier {
	return &Notifier{outbox: outbox}
}

// EnqueueInviteMail queues the invitation on the CALLER'S transaction — the one
// that just minted the token — so the credential and the message that carries
// it commit together.
//
// It is idempotent on the token: the dedupe key is the invite_tokens row id, so
// the same invite can never produce two mails, while re-issuing an invite mints
// a new token and therefore is a new message. A duplicate is silently accepted
// rather than reported, because the caller's answer to "the mail was already
// queued" is the same as to "the mail is now queued": nothing.
func (n *Notifier) EnqueueInviteMail(ctx context.Context, m identitydomain.InviteMail) error {
	if n == nil || n.outbox == nil {
		return nil // not wired (unit tests, an edition with no mail configured)
	}
	email := strings.TrimSpace(m.Email)
	if email == "" || m.TokenID == "" || m.RawToken == "" {
		return errors.New("notifications: invite mail needs a recipient, a token id and a token")
	}
	payload, err := domain.Payload(domain.InvitePayload{
		RecipientName: m.Name,
		InviteToken:   m.RawToken,
		ExpiresAt:     m.ExpiresAt.UTC(),
	})
	if err != nil {
		return err
	}
	if _, err := n.outbox.Enqueue(ctx, outbox.NewMessage{
		Template:  domain.TemplateMemberInvited,
		Recipient: email,
		Payload:   payload,
		DedupeKey: InviteDedupeKey(m.TokenID),
	}); err != nil {
		return fmt.Errorf("notifications: queue invite mail: %w", err)
	}
	return nil
}

// InviteDedupeKey identifies the one mail an issued invite token is worth.
func InviteDedupeKey(tokenID string) string { return "invite:" + tokenID }
