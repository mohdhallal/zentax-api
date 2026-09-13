package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// The trail entry a given-up message leaves. Delivery mechanics (a claim, a
// retry, a backoff) are plumbing and are not audited; a message the product has
// stopped trying to deliver is a FACT ABOUT A TENANT — "you were owed a
// notification and will not get it" — and belongs in the trail.
//
// These are spelled out again as literals at the Record call below, and must
// be: modules/auditlog derives GET /audit-log's filter vocabulary by scanning
// the source for Record's arguments, and refuses a call whose resource type it
// cannot read (see modules/auditlog/dto.ResourceTypes, which carries
// "outbox_message" for this reason).
const (
	AuditAction   = "notification.undeliverable"
	AuditResource = "outbox_message"
)

// runnerGUC is the Postgres setting that opens the delivery loop's cross-tenant
// door (see the RLS block in migrations/deploy/20260913000027_outbox_messages.sql).
// It is set in exactly one place — withinRunnerTx — with is_local => true, i.e.
// SET LOCAL: it lives for that one transaction and is invisible to every other
// connection and every later transaction on the same pooled connection.
const runnerGUC = "app.outbox_runner"

// messageColumns is what a claim returns; last_error and the timestamps of a
// settled row are operator data, not delivery input, so they stay out.
const messageColumns = `id, tenant_id, channel, template, recipient, payload, dedupe_key,
	status, due_at, next_attempt_at, attempts, created_by`

// AuditRecorder is the slice of platform/audit.Recorder this package needs.
type AuditRecorder interface {
	Record(ctx context.Context, action, resourceType, resourceID string, details map[string]any) error
}

// Store is the delivery loop's view of the queue: claim, settle, prune.
//
// READING ACROSS TENANTS, EXPLICITLY. outbox_messages is RLS-isolated by
// app.tenant_id like every other domain table, and the app's database role is
// NOSUPERUSER + NOBYPASSRLS — so a loop that drains every tenant's due messages
// cannot simply "be the app" and see them. It must not, either: a job that
// quietly ignores row-level security is how a tenant-isolation model dies.
//
// So the cross-tenant path is a second, deliberately narrow door in the policy
// set rather than an absence of one. A transaction that sets the app.outbox_runner
// GUC matches two extra policies — SELECT and UPDATE — and one DELETE policy
// that only reaches SETTLED rows. There is no INSERT policy for the runner at
// all: the delivery loop can read a message and record its fate, and can clear
// away spent ones, but it can never author a message and can never destroy work
// that has not yet been delivered or given up on. Everything a tenant's own
// requests do still goes through the ordinary tenant policy; nothing on a
// request path sets that GUC, and no handler can reach set_config.
//
// The one place a delivery settles under the ORDINARY tenant policy is
// MarkDead: it binds app.tenant_id (and app.user_id) for the owning tenant so
// the row change and its audit entry — which the append-only audit policy will
// only accept from that tenant — commit on one transaction.
type Store struct {
	db    database.ExecerPg
	tx    database.ExecerPgTx
	audit AuditRecorder
	// systemActor attributes an undeliverable entry for a message no user
	// caused (a scheduled producer's). Empty means such a message is settled
	// without an audit entry rather than under an invented actor.
	systemActor string
}

var _ MessageStore = (*Store)(nil)

// NewStore builds the store over the shared Execer seam. Both arguments are
// normally the same *database.Exec.
func NewStore(db database.ExecerPg, tx database.ExecerPgTx) *Store {
	return &Store{db: db, tx: tx}
}

// WithAudit attaches the audit trail. Without it a given-up message is still
// settled and still counted — it just leaves no trail entry.
func (s *Store) WithAudit(a AuditRecorder) *Store {
	s.audit = a
	return s
}

// WithSystemActor sets the actor id used when a given-up message names no
// causing user. Leave it unset until the deployment has a real system identity
// to name: an invented UUID in an actor-by-ID trail is worse than a gap.
func (s *Store) WithSystemActor(userID string) *Store {
	s.systemActor = userID
	return s
}

// ClaimDue leases a batch of due messages to this runner and returns them.
//
// The claim is one statement: the inner SELECT ... FOR UPDATE SKIP LOCKED takes
// row locks on the oldest due messages and steps over any row another
// transaction already holds, so two runners racing on the same batch can never
// be handed the same message. (There should be exactly one runner — the
// scheduler's advisory lock sees to that — but a queue that is only correct
// while its coordinator is correct is not correct.)
//
// The same statement charges the attempt and pushes next_attempt_at out by
// lease. That makes the claim a LEASE rather than a checkout: nothing has to
// reap a runner that dies mid-send, because the row simply becomes claimable
// again when the lease expires. The cost is that a message whose provider call
// succeeded but whose settle never committed will be sent again — at-least-once
// by construction, which the package doc explains is safe.
func (s *Store) ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	if lease <= 0 {
		lease = DefaultClaimLease
	}

	var rows []messageRow
	err := s.withinRunnerTx(ctx, func(ctx context.Context) error {
		return s.db.SelectContext(ctx, &rows, `
			UPDATE outbox_messages
			SET attempts = attempts + 1,
			    next_attempt_at = NOW() + make_interval(secs => $2),
			    updated_at = NOW()
			WHERE id IN (
			    SELECT id FROM outbox_messages
			    WHERE status = 'pending' AND next_attempt_at <= NOW()
			    ORDER BY next_attempt_at, id
			    FOR UPDATE SKIP LOCKED
			    LIMIT $1
			)
			RETURNING `+messageColumns,
			limit, lease.Seconds())
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: claim due messages: %w", err)
	}

	msgs := make([]Message, 0, len(rows))
	for i := range rows {
		msgs = append(msgs, rows[i].message())
	}
	return msgs, nil
}

// ReleaseClaim hands claimed-but-unattempted messages back to the queue.
//
// It is the other half of the claim, and it exists because the claim charges an
// attempt to a whole batch in one statement, BEFORE any of it is sent. That is
// the right trade for a message the runner actually tries (a send that kills
// the process must not be free, or a poisonous message is retried for ever) —
// but it is the wrong trade for the messages a pass never reaches, which is
// most of a batch whenever one provider call goes quiet. Left alone, they spend
// an attempt per pass with no transport contact at all and are dead-lettered
// having never been sent.
//
// So a release does two things: it refunds the attempt, and it drops the lease
// (back to due_at, which restores the queue's oldest-first order rather than
// bunching the released batch at NOW) so the next pass — or the next runner,
// after a shutdown — can take the message at once instead of waiting the lease
// out.
//
// The attempts guard is what makes this safe to do late: it matches only rows
// still carrying the attempt count this runner's claim gave them, so a message
// whose lease expired and was re-claimed by someone else is left alone.
func (s *Store) ReleaseClaim(ctx context.Context, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}

	ids := make([]string, 0, len(msgs))
	attempts := make([]int32, 0, len(msgs))
	for i := range msgs {
		ids = append(ids, msgs[i].ID)
		attempts = append(attempts, int32(msgs[i].Attempts)) //nolint:gosec // attempts is a SMALLINT column.
	}

	err := s.withinRunnerTx(ctx, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE outbox_messages m
			SET next_attempt_at = m.due_at,
			    attempts = GREATEST(m.attempts - 1, 0),
			    updated_at = NOW()
			FROM unnest($1::uuid[], $2::int4[]) AS claimed(id, attempts)
			WHERE m.id = claimed.id
			  AND m.status = 'pending'
			  AND m.attempts = claimed.attempts`,
			ids, attempts)
		return err
	})
	if err != nil {
		return fmt.Errorf("outbox: release %d claimed messages: %w", len(msgs), err)
	}
	return nil
}

// MarkSent records that the transport accepted the message, and forgets what
// was in it.
//
// The payload goes back to '{}' on the same statement. A delivered message has
// no further use for its variable set — the row is terminal, and the state
// trigger refuses to re-open it, so a redelivery is impossible by construction
// — while KEEPING it would leave a live invite credential (sealed, but present)
// at rest until the 30-day pruner happened past. Bounding that window by
// delivery rather than by retention is the cheaper half of the fix; see
// seal.go for the other half.
func (s *Store) MarkSent(ctx context.Context, m Message) error {
	err := s.withinRunnerTx(ctx, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE outbox_messages
			SET status = 'sent', sent_at = NOW(), last_error = NULL,
			    payload = '{}'::jsonb, updated_at = NOW()
			WHERE id = $1 AND status = 'pending'`, m.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("outbox: mark message %s sent: %w", m.ID, err)
	}
	return nil
}

// Reschedule puts a transiently failed message back on the queue at a later
// instant and records why. The row stays pending: it is still owed.
func (s *Store) Reschedule(ctx context.Context, m Message, at time.Time, r Reason) error {
	err := s.withinRunnerTx(ctx, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE outbox_messages
			SET next_attempt_at = $2, last_error = $3, updated_at = NOW()
			WHERE id = $1 AND status = 'pending'`, m.ID, at.UTC(), r.detail())
		return err
	})
	if err != nil {
		return fmt.Errorf("outbox: reschedule message %s: %w", m.ID, err)
	}
	return nil
}

// MarkDead gives a message up, permanently.
//
// This is the one delivery outcome that reaches the audit trail, and it does so
// on the SAME transaction as the state change — the tenant's evidence that a
// notification it was owed will not arrive commits with the fact, or neither
// does. The entry carries the message id, the channel, the template, the
// attempt count and the reason CLASS; never the recipient's address and never
// the provider's message, which can quote it (ADR-0007/0008 PII rule).
//
// Attribution follows ADR-0008 actor-by-ID: the actor is whoever's mutation
// owed the message. A message no user caused (a scheduled producer's) is
// settled without a trail entry unless the deployment has named a system actor
// — an invented actor id would be a worse record than an absent one.
func (s *Store) MarkDead(ctx context.Context, m Message, r Reason) error {
	actor := m.CreatedBy
	if actor == "" {
		actor = s.systemActor
	}

	if s.audit == nil || actor == "" || m.TenantID == "" {
		err := s.withinRunnerTx(ctx, func(ctx context.Context) error {
			return s.settleDead(ctx, m, r)
		})
		if err != nil {
			return fmt.Errorf("outbox: mark message %s dead: %w", m.ID, err)
		}
		return nil
	}

	// Bind the owning tenant (and the actor) so the ordinary tenant policy
	// covers the update and the append-only audit policy accepts the entry.
	tenantCtx := app.WithTenantID(ctx, m.TenantID)
	tenantCtx = app.WithRequester(tenantCtx, &app.Requester{Kind: app.RequesterUser, ID: actor})

	err := s.tx.WithinTransaction(tenantCtx, func(ctx context.Context) error {
		if err := s.settleDead(ctx, m, r); err != nil {
			return err
		}
		return s.audit.Record(ctx, "notification.undeliverable", "outbox_message", m.ID, map[string]any{
			"channel":  m.Channel,
			"template": m.Template,
			"attempts": m.Attempts,
			"reason":   r.class(),
		})
	})
	if err != nil {
		return fmt.Errorf("outbox: mark message %s dead: %w", m.ID, err)
	}
	return nil
}

// settleDead writes the terminal state. The payload is cleared here for the
// same reason as in MarkSent, and the reason it matters MORE: a dead message is
// one nobody is coming back for, so its content would otherwise sit until the
// pruner, having never been delivered. What survives is what diagnosis needs —
// the template, the recipient, the attempt count, last_error, and the audit
// entry this settlement writes.
func (s *Store) settleDead(ctx context.Context, m Message, r Reason) error {
	// The dedupe key is released with the payload. A dead message is one nobody
	// received, so the producer must be free to queue the same thing again: a
	// digest whose provider was down all day is re-queued by the next sweep
	// rather than refused as a duplicate of a message that never arrived. A
	// DELIVERED message keeps its key, which is what stops a second copy.
	_, err := s.db.ExecContext(ctx, `
		UPDATE outbox_messages
		SET status = 'dead', failed_at = NOW(), last_error = $2,
		    payload = '{}'::jsonb, dedupe_key = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'pending'`, m.ID, r.detail())
	return err
}

// Prune clears out settled messages older than the retention window. Pending
// messages are untouchable — the runner's DELETE policy cannot even see them —
// so a bug here can delay a notification but can never drop one.
//
// This is also the erasure path for the personal datum a settled row still
// holds (the recipient address, ADR-0007): a delivered message stops existing
// once the window passes. It is no longer the erasure path for the payload —
// settling clears that on the spot, so retention bounds an address, not a
// credential.
func (s *Store) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		olderThan = DefaultRetention
	}

	var removed int64
	err := s.withinRunnerTx(ctx, func(ctx context.Context) error {
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM outbox_messages
			WHERE status <> 'pending' AND updated_at < NOW() - make_interval(secs => $1)`,
			olderThan.Seconds())
		if err != nil {
			return err
		}
		removed, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("outbox: prune settled messages: %w", err)
	}
	return removed, nil
}

// withinRunnerTx runs fn on a transaction holding the runner GUC. The context
// carries no tenant, so the Tx seam binds no app.tenant_id and the ordinary
// isolation policy matches nothing; the runner policies are what the statements
// inside travel on. set_config's third argument is is_local — SET LOCAL — so
// the setting dies with the transaction and can never leak onto a pooled
// connection's next user.
func (s *Store) withinRunnerTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.db.ExecContext(ctx,
			`SELECT set_config($1, 'on', true)`, runnerGUC); err != nil {
			return fmt.Errorf("open runner scope: %w", err)
		}
		return fn(ctx)
	})
}

// messageRow is the scan target; Payload arrives as jsonb bytes and created_by
// is nullable.
type messageRow struct {
	ID            string         `db:"id"`
	TenantID      string         `db:"tenant_id"`
	Channel       string         `db:"channel"`
	Template      string         `db:"template"`
	Recipient     string         `db:"recipient"`
	Payload       []byte         `db:"payload"`
	DedupeKey     sql.NullString `db:"dedupe_key"`
	Status        string         `db:"status"`
	DueAt         time.Time      `db:"due_at"`
	NextAttemptAt time.Time      `db:"next_attempt_at"`
	Attempts      int            `db:"attempts"`
	CreatedBy     sql.NullString `db:"created_by"`
}

func (r *messageRow) message() Message {
	return Message{
		ID:            r.ID,
		TenantID:      r.TenantID,
		Channel:       r.Channel,
		Template:      r.Template,
		Recipient:     r.Recipient,
		Payload:       json.RawMessage(r.Payload),
		DedupeKey:     r.DedupeKey.String,
		Status:        Status(r.Status),
		DueAt:         r.DueAt,
		NextAttemptAt: r.NextAttemptAt,
		Attempts:      r.Attempts,
		CreatedBy:     r.CreatedBy.String,
	}
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
