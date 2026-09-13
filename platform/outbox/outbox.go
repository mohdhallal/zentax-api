// Package outbox is the transactional outbox: the queue of messages the
// product owes a human, written on the SAME transaction as the change that owes
// them and delivered later by the scheduler (platform/scheduler).
//
// The rule the pattern buys. A tax deadline reminder must not be sent for a
// mutation that rolled back, and must not be lost because a mutation committed
// and the process then died — both are ordinary outcomes of calling a mail
// provider inside a request. An outbox row commits or rolls back WITH the
// change, so the change and its notification can never disagree; delivery is a
// separate, retried step, so a provider outage costs a retry rather than a
// message.
//
// Delivery is AT-LEAST-ONCE. No transaction spans Postgres and an SMTP
// provider, so a runner that dies between "the provider accepted it" and "the
// row says sent" will send it again when the claim lease expires. Three things
// make that duplicate harmless:
//
//   - Content is frozen. Payload is a snapshot taken at enqueue time and the
//     rendered mail is derived from the row, never re-read from live data, so a
//     redelivery is the IDENTICAL mail — not a second, different one.
//   - Messages are statements, not actions. A reminder says a task is due on a
//     date; it moves no money and changes no state, so two copies are a
//     redundancy, never a double effect. The one message that does carry a
//     credential — member.invited — is safe for the same reason read another
//     way: the token is single-use and identical in both copies, so the second
//     mail grants exactly what the first did and nothing more. (A message that
//     cannot honestly say that does not belong in this queue.)
//   - Producers are idempotent. DedupeKey is unique per tenant, so re-running a
//     producer — an overlapping reminder pass, a retried API call, a replayed
//     job — enqueues nothing new; Enqueue reports the message that already
//     exists.
//
// Tenancy. outbox_messages is tenant-scoped and RLS-isolated like every other
// domain table (ADR-0004). Enqueue writes through the ordinary tenant
// transaction. The delivery loop, which reads ACROSS tenants, goes through one
// explicit, narrow door — see Store.
//
// Content at rest. The frozen payload is SEALED with the application's
// encryption key and CLEARED when the message settles, so the one row that
// holds a live invite credential holds it as ciphertext, and only until the
// mail goes out. Everything an operator diagnoses with — template, recipient,
// schedule, attempts, last error — stays in the clear. See seal.go.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Status is a message's place in its (one-way) lifecycle.
type Status string

const (
	// StatusPending is owed and not yet settled — the only claimable state.
	StatusPending Status = "pending"
	// StatusSent is accepted by the transport.
	StatusSent Status = "sent"
	// StatusDead is given up on: permanently rejected, or out of attempts.
	StatusDead Status = "dead"
)

// ChannelEmail is the only transport today; the column carries the channel so
// adding one later is a migration, not a new table.
const ChannelEmail = "email"

// maxErrorDetail bounds what is stored in last_error — a provider can return a
// whole SMTP conversation, and the column is an operator hint, not a log sink.
const maxErrorDetail = 500

// Message is one queued message as the delivery loop sees it.
type Message struct {
	ID        string
	TenantID  string
	Channel   string
	Template  string
	Recipient string
	// Payload is the frozen variable set the mail layer renders with, in
	// CLEARTEXT: the delivery loop opens the sealed column before it hands the
	// message to a Sender, so this field is live only between the claim and
	// the transport call. Never log it (the ADR-0015 gate refuses to).
	Payload   json.RawMessage
	DedupeKey string
	Status    Status
	// DueAt is when the message became due — immutable, so lateness is
	// measurable. NextAttemptAt is the delivery schedule, moved by the claim
	// lease and by backoff.
	DueAt         time.Time
	NextAttemptAt time.Time
	Attempts      int
	// CreatedBy is the actor whose mutation owed this message (ADR-0008
	// actor-by-ID). Empty when a scheduled producer queued it with no acting
	// user.
	CreatedBy string
}

// NewMessage is what a use case enqueues.
type NewMessage struct {
	// Channel defaults to ChannelEmail.
	Channel string
	// Template names the message kind the mail layer renders.
	Template string
	// Recipient is the address. It is the only personal datum the row keeps IN
	// THE CLEAR — the payload can hold more (a display name, a tenant's task
	// list, an invite credential) and is sealed for that reason — and it never
	// reaches the audit trail.
	Recipient string
	// Payload is the render's variables, snapshotted now. It is sealed on the
	// way into the column and cleared when the message settles, so a value
	// here may be a credential; it may not be a value the renderer could
	// instead look up, because the runner does not act for the tenant.
	Payload map[string]any
	// DedupeKey makes the producer idempotent: at most one live message per
	// (tenant, key). Empty means "not deduplicated".
	DedupeKey string
	// DueAt is when the message should go out. Zero means now (stamped by the
	// database, like every other instant in the schema).
	DueAt time.Time
}

// Receipt is what Enqueue reports back.
type Receipt struct {
	// ID is the message, whether this call created it or found it.
	ID string
	// Deduplicated is true when an identical key was already queued and this
	// call wrote nothing — the producer's re-run was a no-op.
	Deduplicated bool
}

// Sender is the mail seam. The transport (SMTP, SES, a provider API) implements
// it; nothing in this package knows what it is.
//
// A Send that returns nil means the transport ACCEPTED the message — not that
// it was read, and not that it will not bounce later. A returned error is
// retried on a backoff unless it is Permanent.
//
// Send MUST honour ctx. The delivery loop gives every call a deadline of its
// own (Settings.SendTimeout) and treats silence past it as a failed attempt, so
// a transport that ignores the deadline holds the pass open for as long as it
// likes — the one thing the budget cannot defend against from the outside.
// Setting socket deadlines from ctx, as the shipped SMTP and SES transports do,
// is what makes the budget real.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// PermanentError marks a failure that retrying cannot fix: an address the
// provider rejects outright, a template that does not exist, a payload the
// renderer refuses. Such a message is dead-lettered on the first return rather
// than burning an attempt budget on a certainty.
type PermanentError struct {
	Reason string
	Err    error
}

func (e *PermanentError) Error() string {
	if e.Err == nil {
		return e.Reason
	}
	return e.Reason + ": " + e.Err.Error()
}

func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as a failure not worth retrying.
func Permanent(reason string, err error) error {
	return &PermanentError{Reason: reason, Err: err}
}

// IsPermanent reports whether err (or anything it wraps) says "do not retry".
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// Reason is why a delivery attempt ended the way it did: a stable CLASS, which
// is safe to audit and to label a metric with, and an operator DETAIL, which
// may quote the provider verbatim and therefore may echo the recipient's
// address — so it is stored in last_error and NEVER audited.
type Reason struct {
	Class  string
	Detail string
}

// Reason classes.
const (
	ReasonTransient = "transient_failure"
	ReasonPermanent = "permanent_failure"
	ReasonExhausted = "attempts_exhausted"
	// ReasonTimeout is a transport that never answered within the attempt's
	// window. It is a transient failure like any other — retried, then given
	// up on — but it has its own class because "the provider went silent" and
	// "the provider said no" call for different operator responses.
	ReasonTimeout = "delivery_timeout"
	// ReasonUnreadable is a sealed payload this process cannot open: no
	// encryption key, the wrong one, or an envelope from a newer build. It is
	// retried like a transient failure because the cause is the deployment's
	// and not the message's, and it is its own class because the remedy is
	// nothing like a provider's — see Dispatcher.settleUnreadable.
	ReasonUnreadable = "payload_unreadable"
	ReasonNoRecourse = "unclassified"
)

func (r Reason) detail() string {
	d := strings.TrimSpace(r.Detail)
	if d == "" {
		d = r.Class
	}
	if len(d) > maxErrorDetail {
		d = d[:maxErrorDetail]
	}
	return d
}

func (r Reason) class() string {
	if r.Class == "" {
		return ReasonNoRecourse
	}
	return r.Class
}

// Queue writes messages. It takes the ordinary Execer seam, so an Enqueue
// inside a use case's WithinTransaction lands on THAT transaction — which is
// the whole point of the pattern: the row and the change it announces commit
// together or not at all.
type Queue struct {
	db   database.ExecerPg
	seal *Seal
}

func NewQueue(db database.ExecerPg) *Queue {
	return &Queue{db: db}
}

// WithSeal makes the queue store SEALED payloads (see seal.go). Without it the
// frozen variable set — which for member.invited is a live credential — sits in
// the column in cleartext for as long as the row exists.
func (q *Queue) WithSeal(s *Seal) *Queue {
	q.seal = s
	return q
}

// Enqueue adds a message to the caller's transaction.
//
// tenant_id and created_by come from the transaction's GUCs (app.tenant_id /
// app.user_id), exactly as they do for every other tenant-scoped insert, so a
// caller cannot address a message at another tenant even by mistake.
//
// A DedupeKey already queued for this tenant makes the call a no-op: the
// receipt names the existing message and reports Deduplicated. That is the
// producer half of the at-least-once story.
func (q *Queue) Enqueue(ctx context.Context, m NewMessage) (Receipt, error) {
	if m.Channel == "" {
		m.Channel = ChannelEmail
	}
	if m.Channel != ChannelEmail {
		return Receipt{}, apperrors.NewValidation("outbox: unsupported channel " + m.Channel)
	}
	if strings.TrimSpace(m.Template) == "" {
		return Receipt{}, apperrors.NewValidation("outbox: message needs a template")
	}
	if strings.TrimSpace(m.Recipient) == "" {
		return Receipt{}, apperrors.NewValidation("outbox: message needs a recipient")
	}

	payload, err := json.Marshal(orEmpty(m.Payload))
	if err != nil {
		return Receipt{}, fmt.Errorf("outbox: encode payload: %w", err)
	}
	// Sealed BEFORE it reaches the statement, so the cleartext never leaves
	// this frame: not into the row, not into a statement log, not into a
	// replica. See seal.go for what stays readable and why.
	payload, err = q.seal.Seal(payload)
	if err != nil {
		return Receipt{}, err
	}

	var dedupe *string
	if key := strings.TrimSpace(m.DedupeKey); key != "" {
		dedupe = &key
	}
	var dueAt *time.Time
	if !m.DueAt.IsZero() {
		due := m.DueAt.UTC()
		dueAt = &due
	}

	// due_at and next_attempt_at start equal: a message is claimable from the
	// instant it is due. COALESCE keeps the clock in the database.
	var id string
	err = q.db.GetContext(ctx, &id, `
		INSERT INTO outbox_messages (channel, template, recipient, payload, dedupe_key, due_at, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6::timestamptz, NOW()), COALESCE($6::timestamptz, NOW()))
		ON CONFLICT (tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING id`,
		m.Channel, m.Template, m.Recipient, payload, dedupe, dueAt)
	switch {
	case err == nil:
		return Receipt{ID: id}, nil
	case !isNoRows(err) || dedupe == nil:
		return Receipt{}, fmt.Errorf("outbox: enqueue: %w", err)
	}

	// DO NOTHING returned no row: the key is already queued for this tenant.
	// The SELECT is RLS-scoped to the same tenant, so it can only ever find
	// this tenant's message.
	if err := q.db.GetContext(ctx, &id,
		`SELECT id FROM outbox_messages WHERE dedupe_key = $1`, *dedupe); err != nil {
		return Receipt{}, fmt.Errorf("outbox: resolve deduplicated message: %w", err)
	}
	return Receipt{ID: id, Deduplicated: true}, nil
}

func orEmpty(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return p
}
