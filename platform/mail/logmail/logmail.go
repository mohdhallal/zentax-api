// Package logmail is the mailer that does not send: it records that a message
// was rendered and reports success. It is the development and test default, and
// the only adapter that needs no credentials, no relay and no network.
//
// It must never run in a deployed tier. A tier whose mail driver is "log" would
// accept every notification, log it, and tell the outbox it was delivered —
// silently dropping the reminders the product exists to send. config.MailConfig
// refuses to boot a deployed tier on this driver (ADR-0014's fail-closed rule),
// which is the gate; this package's job is to be honest about what it is.
//
// WHAT IT LOGS, AND WHY THAT IS SO LITTLE (ADR-0027 decision 9, ADR-0015).
// A line here says that a message was rendered, which template it came from,
// that the recipient is withheld, and how big each part is. It does NOT carry
// the subject, the text body, the HTML body, or any value a template renders
// from — because those are, in order: a live single-use invite credential
// ("…/accept-invite?token=zti_…"), the recipient's display name, and a tenant's
// entity names, task names and filing deadlines. Logs sit OUTSIDE the erasure
// boundary: what reaches one cannot be deleted on request, cannot be scoped to
// a tenant, and travels wherever the log store ships. A credential written
// there can be replayed by anyone with log access for as long as it lives.
//
// This adapter is also the DEFAULT of both shipped Compose stacks, so "only a
// developer ever sees it" was never true: a self-hoster running the product in
// earnest on the development profile would put every invite link and every
// tenant's compliance calendar into container logs.
//
// A developer who wants the invite link still has it — POST /members returns
// the raw token in the response, by deliberate decision. A developer who wants
// to read a rendered mail end to end should point MAIL_DRIVER=smtp at a local
// catcher: that keeps the content in a throwaway inbox instead of the log store.
package logmail

import (
	"context"

	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/mail"
)

// Sender writes one line per message instead of delivering it.
type Sender struct {
	log logger.Logger
}

var _ mail.Sender = (*Sender)(nil)

// New builds the log mailer. A nil logger falls back to a default one so a test
// or a tool can construct it without ceremony.
func New(l logger.Logger) *Sender {
	if l == nil {
		l = logger.New(nil)
	}
	return &Sender{log: l}
}

const op = "logmail: send"

// Send validates the message and records that it was rendered.
//
// Validation runs here rather than being skipped as "it isn't going anywhere":
// the log mailer is what development and the acceptance suite exercise, so it
// is the adapter that has to catch a malformed message — an unset sender, a
// header-injecting subject — before the same message meets a real relay.
//
// The byte counts are the diagnosis this line can honestly offer: they say a
// template rendered something rather than nothing, and that a body is roughly
// the length it should be, without quoting a byte of it.
func (s *Sender) Send(ctx context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return mail.Permanent(op, "", err)
	}
	if err := ctx.Err(); err != nil {
		return mail.Retryable(op, "", err)
	}

	s.log.WithContext(ctx).Info("mail rendered, not sent (log mail driver)",
		logger.String("template", msg.Kind),
		logger.String("recipient", logger.Redacted),
		logger.Int("subjectBytes", len(msg.Subject)),
		logger.Int("textBytes", len(msg.Text)),
		logger.Int("htmlBytes", len(msg.HTML)),
	)
	return nil
}
