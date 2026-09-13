// Package mail is the provider-agnostic outbound-mail seam — ADR-0009's
// "Email — SES / SMTP" adapter, the third after object storage (ADR-0022) and
// before key management. Everything that sends mail speaks only this interface;
// the concrete adapters (logmail, smtpmail, sesmail) live in their own packages
// so no provider SDK, no net/smtp connection and no AWS credential type ever
// leaves an adapter.
//
// Two shapes matter to every caller:
//
//  1. A Message is ALREADY RENDERED. Rendering (text + HTML from typed values,
//     with the product name and public base address from configuration) happens
//     in template.go, before a Sender is ever reached. An adapter's only job is
//     delivery, which is what makes the adapters interchangeable and testable
//     without a network.
//
//  2. A failure is CLASSIFIED. The transactional outbox — a row written in the
//     same transaction as the change that causes it, delivered later by the
//     scheduler — has exactly one decision to make when delivery fails: retry,
//     or give up and record why. So every adapter reports failure as an *Error
//     carrying that verdict, and the outbox asks IsPermanent / IsRetryable
//     rather than string-matching a provider's prose.
//
// One recipient per Message, deliberately. The outbox stores one row per
// recipient, so a retry, a bounce and a give-up all belong to one person rather
// than to a list; a compliance product also has no business assembling
// cross-tenant recipient lists in a single envelope, where one mistake is a
// disclosure.
package mail

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"unicode/utf8"
)

// Sender delivers one already-rendered message.
//
// An implementation MUST classify its failures (Permanent / Retryable) and MUST
// honour ctx cancellation — the scheduler runs delivery under a deadline and a
// shutdown must not wait on a hung MTA.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Address is one mailbox, optionally with a display name.
type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// MaxDisplayNameRunes bounds a display name. It is rendered into a header, and
// a header line has a hard length limit no folding can rescue.
const MaxDisplayNameRunes = 128

// MaxSubjectRunes bounds a subject for the same reason.
const MaxSubjectRunes = 255

// String renders the address RFC 5322 style: `Jane Doe <jane@acme.test>`, or a
// bare mailbox when there is no display name. A non-ASCII name is encoded as an
// RFC 2047 encoded-word by net/mail.
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return (&netmail.Address{Name: a.Name, Address: a.Email}).String()
}

// IsZero reports whether the address is unset (an optional Reply-To).
func (a Address) IsZero() bool { return a.Name == "" && a.Email == "" }

// Validate rejects an address that could not be delivered to, and — the part
// that is security rather than hygiene — one that could inject a header. A
// display name or mailbox carrying CR or LF would end the header it sits in and
// start another one the attacker chose (a second Bcc, say), so control
// characters are refused here, at the seam, rather than by each adapter.
func (a Address) Validate() error {
	if strings.TrimSpace(a.Email) == "" {
		return errors.New("mail: address is empty")
	}
	if hasControl(a.Email) || hasControl(a.Name) {
		return fmt.Errorf("mail: address %s contains a control character", quotedForError(a.Email))
	}
	if !utf8.ValidString(a.Email) || !utf8.ValidString(a.Name) {
		return errors.New("mail: address is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(a.Name); n > MaxDisplayNameRunes {
		return fmt.Errorf("mail: display name is %d characters, at most %d", n, MaxDisplayNameRunes)
	}
	parsed, err := netmail.ParseAddress(a.Email)
	if err != nil {
		return fmt.Errorf("mail: %s is not a valid e-mail address", quotedForError(a.Email))
	}
	// ParseAddress accepts `Jane <jane@acme.test>` in the mailbox field; the
	// mailbox must be the mailbox alone, or String() would render two names.
	if parsed.Address != a.Email || parsed.Name != "" {
		return fmt.Errorf("mail: %s must be a bare mailbox (put the display name in Name)", quotedForError(a.Email))
	}
	if _, domain, ok := strings.Cut(a.Email, "@"); !ok || domain == "" {
		return fmt.Errorf("mail: %s has no domain", quotedForError(a.Email))
	}
	return nil
}

// Domain is the part after the "@", lowercased; "" when there is none.
func (a Address) Domain() string {
	_, domain, ok := strings.Cut(a.Email, "@")
	if !ok {
		return ""
	}
	return strings.ToLower(domain)
}

// ParseAddress reads `Jane Doe <jane@acme.test>` or a bare mailbox.
func ParseAddress(s string) (Address, error) {
	parsed, err := netmail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return Address{}, fmt.Errorf("mail: %s is not a valid e-mail address", quotedForError(s))
	}
	addr := Address{Name: parsed.Name, Email: parsed.Address}
	if err := addr.Validate(); err != nil {
		return Address{}, err
	}
	return addr, nil
}

// Message is one rendered e-mail, ready to hand to a Sender.
type Message struct {
	// Kind names the template this message came from ("invite",
	// "deadline-reminder"). It is a fixed label, never personal data, so it is
	// the one part of a message that is safe to log and to key metrics on.
	Kind string `json:"kind"`

	From    Address `json:"from"`
	ReplyTo Address `json:"replyTo,omitempty"`
	To      Address `json:"to"`

	Subject string `json:"subject"`
	// Text is required: it is what a plain-text client, a screen reader and a
	// spam filter all read, and the fallback part of the multipart body.
	Text string `json:"text"`
	// HTML is optional. When present it must carry the SAME content as Text —
	// a multipart/alternative body where the parts disagree is how a recipient
	// ends up acting on a different fact than the one that was sent.
	HTML string `json:"html,omitempty"`
}

// Validate checks a message can be delivered and cannot inject headers. Every
// adapter calls it before touching a backend, so a malformed message is a
// permanent failure the outbox records rather than a provider round-trip.
func (m Message) Validate() error {
	if err := m.From.Validate(); err != nil {
		return fmt.Errorf("from: %w", err)
	}
	if err := m.To.Validate(); err != nil {
		return fmt.Errorf("to: %w", err)
	}
	if !m.ReplyTo.IsZero() {
		if err := m.ReplyTo.Validate(); err != nil {
			return fmt.Errorf("replyTo: %w", err)
		}
	}
	subject := strings.TrimSpace(m.Subject)
	switch {
	case subject == "":
		return errors.New("mail: subject is empty")
	case hasControl(m.Subject):
		return errors.New("mail: subject contains a control character")
	case !utf8.ValidString(m.Subject):
		return errors.New("mail: subject is not valid UTF-8")
	case utf8.RuneCountInString(m.Subject) > MaxSubjectRunes:
		return fmt.Errorf("mail: subject is %d characters, at most %d", utf8.RuneCountInString(m.Subject), MaxSubjectRunes)
	}
	if strings.TrimSpace(m.Text) == "" {
		return errors.New("mail: text body is empty (the plain-text part is required)")
	}
	if !utf8.ValidString(m.Text) || !utf8.ValidString(m.HTML) {
		return errors.New("mail: body is not valid UTF-8")
	}
	if hasBodyControl(m.Text) || hasBodyControl(m.HTML) {
		return errors.New("mail: body contains a control character")
	}
	return nil
}

// --- Failure classification -------------------------------------------------

// The two verdicts a Sender returns. They are sentinels rather than a bool so
// that a caller writes errors.Is(err, mail.ErrPermanent) and so that a wrapped
// error keeps its verdict all the way up to the outbox.
var (
	// ErrPermanent marks a failure that retrying cannot fix: the mailbox does
	// not exist, the sender is not verified, the message was rejected. The
	// outbox gives up and records the reason.
	ErrPermanent = errors.New("mail: permanent failure")

	// ErrRetryable marks a failure that is expected to pass later: a refused
	// connection, a throttle, a 4xx greylist, a provider outage. The outbox
	// backs off and tries again.
	ErrRetryable = errors.New("mail: temporary failure")
)

// Error is what every adapter returns. Op names the step that failed
// ("smtp: rcpt", "ses: send-email") and Code carries the provider's own code
// when there is one (an SMTP reply code, an SES error type) so an operator can
// look it up without the adapter having to translate it.
type Error struct {
	Op        string
	Code      string
	Permanent bool
	Err       error
}

func (e *Error) Error() string {
	verdict := "temporary"
	if e.Permanent {
		verdict = "permanent"
	}
	var b strings.Builder
	b.WriteString(e.Op)
	b.WriteString(": ")
	b.WriteString(verdict)
	if e.Code != "" {
		b.WriteString(" (")
		b.WriteString(e.Code)
		b.WriteString(")")
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap exposes the cause, so errors.Is/As still reach a provider error or a
// context.DeadlineExceeded underneath.
func (e *Error) Unwrap() error { return e.Err }

// Is answers the two verdict sentinels. Every *Error is one or the other, which
// is what lets IsRetryable be "not permanent" without ambiguity.
func (e *Error) Is(target error) bool {
	switch target { //nolint:errorlint // comparing the sentinel itself is what Is is for
	case ErrPermanent:
		return e.Permanent
	case ErrRetryable:
		return !e.Permanent
	default:
		return false
	}
}

// Permanent builds a failure the outbox must not retry.
func Permanent(op, code string, err error) error {
	return &Error{Op: op, Code: code, Permanent: true, Err: err}
}

// Retryable builds a failure the outbox should try again later.
func Retryable(op, code string, err error) error {
	return &Error{Op: op, Code: code, Permanent: false, Err: err}
}

// IsPermanent reports whether err was explicitly classified as permanent.
func IsPermanent(err error) bool {
	return err != nil && errors.Is(err, ErrPermanent)
}

// IsRetryable reports whether the outbox should try again.
//
// Note the default: anything that is NOT explicitly permanent is retryable —
// an unclassified error from a code path nobody anticipated, a context
// deadline, a panic recovered into an error. Retrying a hopeless message costs
// a bounded number of attempts (the outbox caps them); giving up on a message
// that would have gone through the next minute silently loses the notification
// a tax deadline depends on. The cheap mistake is the one this makes.
func IsRetryable(err error) bool {
	return err != nil && !IsPermanent(err)
}

// --- helpers ----------------------------------------------------------------

// hasControl reports whether s contains any C0 control character or DEL. Used
// on header material, where a newline is an injection and a tab is noise.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// hasBodyControl is hasControl for body text, where CR, LF and TAB are legal.
func hasBodyControl(s string) bool {
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// quotedForError renders a value inside an error message, bounded. An address
// in an error is personal data, so a boot-time or delivery error that reaches a
// log goes through logger.Error, which scrubs e-mail-shaped text (ADR-0015);
// bounding it here keeps the line from becoming a blob either way.
func quotedForError(s string) string {
	const maxRunes = 96
	if utf8.RuneCountInString(s) > maxRunes {
		s = string([]rune(s)[:maxRunes]) + "…"
	}
	return `"` + s + `"`
}
