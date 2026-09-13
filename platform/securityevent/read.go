package securityevent

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/platform/database"
)

// streamGUC opens the cross-tenant read door on security_events (see the RLS
// block in migrations/deploy/20260913000031_security_events.sql).
//
// It is set in EXACTLY ONE place — Reader.within, below — with
// set_config(..., is_local => true), i.e. SET LOCAL: it lives for one
// transaction and is invisible to every other connection and to every later
// transaction on the same pooled connection. Nothing on a request path can
// reach it: the Tx seam binds only app.tenant_id and app.user_id, both from the
// authenticated session, and no handler can call set_config.
//
// The door exists because the stream's most important rows belong to NO tenant:
// a failed login for an address that resolves to no account is invisible to
// every tenant policy, by construction and rightly — it is not any customer's
// event to read. Somebody has to be able to see it, and "the job that quietly
// ignores row-level security" is how a tenant-isolation model dies, so the
// reader asks for the door by name.
const streamGUC = "app.security_stream"

// Entry is one stored authentication event. Everything about WHO is nullable,
// which is the property the whole stream is built around.
type Entry struct {
	EventID       string         `db:"event_id"`
	OccurredAt    time.Time      `db:"occurred_at"`
	Event         string         `db:"event"`
	Outcome       string         `db:"outcome"`
	Method        string         `db:"method"`
	Reason        sql.NullString `db:"reason"`
	TenantID      sql.NullString `db:"tenant_id"`
	PrincipalID   sql.NullString `db:"principal_id"`
	ActorID       sql.NullString `db:"actor_id"`
	SessionID     sql.NullString `db:"session_id"`
	SubjectDigest sql.NullString `db:"subject_digest"`
	ClientIP      sql.NullString `db:"client_ip"`
	// ClientIPSource says how ClientIP was arrived at (AddrFromPeer,
	// AddrFromForwarded, AddrFromProxy). It travels with the address everywhere
	// the address does: an investigator who cannot tell an unforgeable socket
	// peer from a proxy hop reads our own load balancer as the caller.
	ClientIPSource sql.NullString `db:"client_ip_source"`
	RequestID      sql.NullString `db:"request_id"`
}

// Filter narrows a stream read to one of the questions the table is indexed
// for. A zero Filter reads the whole (limited) stream, newest first.
type Filter struct {
	// TenantID / PrincipalID / SubjectDigest are the correlators. Digest a
	// candidate address with the same key to count attempts against it —
	// see Digest.
	TenantID      string
	PrincipalID   string
	SubjectDigest string
	// Event narrows to one vocabulary entry.
	Event string
	// ClientIP is the "from where" question, as an exact address.
	ClientIP string
	// Since bounds the window; the table is partitioned by month, so a bounded
	// window is also a bounded scan.
	Since time.Time
	// Limit defaults to DefaultReadLimit and is capped at MaxReadLimit.
	Limit int
}

const (
	// DefaultReadLimit is a screenful of timeline.
	DefaultReadLimit = 100
	// MaxReadLimit bounds one read. An export walks windows, not one huge page.
	MaxReadLimit = 1000
)

// entryColumns selects client_ip through host() so the INET reaches Go as the
// plain address it is, without depending on how the driver renders an inet.
const entryColumns = `event_id, occurred_at, event, outcome, method, reason,
	tenant_id, principal_id, actor_id, session_id, subject_digest,
	host(client_ip) AS client_ip, client_ip_source, request_id`

// Reader reads the stream through the cross-tenant door. It is the investigator's
// and the future log-shipper's seam — there is no request route onto this table.
type Reader struct {
	db database.ExecerPg
	tx database.ExecerPgTx
}

func NewReader(db database.ExecerPg, tx database.ExecerPgTx) *Reader {
	return &Reader{db: db, tx: tx}
}

// List returns matching events, newest first.
func (r *Reader) List(ctx context.Context, f Filter) ([]Entry, error) {
	where := []string{}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, clause+"$"+strconv.Itoa(len(args)))
	}
	if f.TenantID != "" {
		add("tenant_id = ", f.TenantID)
	}
	if f.PrincipalID != "" {
		add("principal_id = ", f.PrincipalID)
	}
	if f.SubjectDigest != "" {
		add("subject_digest = ", f.SubjectDigest)
	}
	if f.Event != "" {
		add("event = ", f.Event)
	}
	if f.ClientIP != "" {
		add("client_ip = ", f.ClientIP)
	}
	if !f.Since.IsZero() {
		add("occurred_at >= ", f.Since)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	if limit > MaxReadLimit {
		limit = MaxReadLimit
	}
	args = append(args, limit)

	query := `SELECT ` + entryColumns + ` FROM security_events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY occurred_at DESC, event_id DESC LIMIT $" + strconv.Itoa(len(args))

	var entries []Entry
	err := r.within(ctx, func(ctx context.Context) error {
		return r.db.SelectContext(ctx, &entries, query, args...)
	})
	if err != nil {
		return nil, fmt.Errorf("securityevent: read stream: %w", err)
	}
	return entries, nil
}

// within opens the cross-tenant read scope for one transaction.
func (r *Reader) within(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := r.db.ExecContext(ctx, `SELECT set_config($1, 'on', true)`, streamGUC); err != nil {
			return fmt.Errorf("open stream scope: %w", err)
		}
		return fn(ctx)
	})
}
