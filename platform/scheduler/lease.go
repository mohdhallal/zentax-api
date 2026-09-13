package scheduler

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/jmoiron/sqlx"
)

// Lease is leadership: the right to be the one process in the deployment that
// runs scheduled work. Acquire is idempotent — a holder calls it every probe to
// confirm it still holds; a non-holder calls it to offer itself.
//
// Release is deliberately error-free and is called with a FRESH context at
// shutdown (the run context is already cancelled by then). Giving a lease up is
// best effort: the durable guarantee is that ending the process ends the lease.
type Lease interface {
	Acquire(ctx context.Context) (bool, error)
	Release(ctx context.Context)
	// Name identifies the lease in logs and metrics.
	Name() string
}

// Connector is the slice of *sqlx.DB an advisory lease needs: a connection it
// can pin for its own lifetime, outside the request pool's churn.
type Connector interface {
	Connx(ctx context.Context) (*sqlx.Conn, error)
}

// AdvisoryLease is leadership taken through a Postgres SESSION-level advisory
// lock (pg_try_advisory_lock) held on one pinned connection.
//
// Why an advisory lock rather than a leader-election service, a cloud
// scheduler, or a separate worker process:
//
//   - The self-hosted edition is ONE container against one Postgres. It cannot
//     depend on a cloud scheduler, a queue broker, or a second deployable, and
//     it already has the only coordinator it needs — the database.
//   - The hosted edition runs SEVERAL API tasks behind a load balancer. The
//     lock makes exactly one of them the runner, with no new infrastructure and
//     no configuration that could name two runners by mistake.
//   - The lock lives in the SESSION, not in a table: nothing has to expire it,
//     nothing has to be swept, and a task that dies — crash, OOM, SIGKILL,
//     network partition — releases it the instant Postgres reaps the backend.
//     A table-based lease with a TTL would leave scheduled work stopped for the
//     length of that TTL and would need a heartbeat writer to keep it alive.
//
// The cost is one pooled connection held for as long as the process is leader,
// which is why the lease pins its own connection instead of borrowing one per
// probe: an advisory lock belongs to the session that took it, so a connection
// returned to the pool would release it.
type AdvisoryLease struct {
	db   Connector
	name string
	key  int64

	mu   sync.Mutex
	conn *sqlx.Conn
}

var _ Lease = (*AdvisoryLease)(nil)

// NewAdvisoryLease builds the lease for a namespace. The namespace is hashed to
// the lock key, so two deployments sharing one database contend only when they
// mean to (same namespace).
//
// Separation from the OTHER advisory locks in this system is PROBABILISTIC
// rather than structural, and ADR-0027 decision 5 records it as exactly that.
// ADR-0008's per-tenant audit chain and the member-admin guard both take
// pg_advisory_xact_lock(hashtextextended(<tenant uuid>, 0)); the only key
// spaces Postgres guarantees disjoint are the one-`bigint` form and the
// two-integer form, and both those keys and this one are the one-`bigint`
// form — so nothing but the width of the hash keeps them apart. A collision
// (~2⁻⁶⁴ per key) would corrupt nothing, but it would stall a domain write
// behind leadership until the leader let the lock go. Taking the lease with
// the two-integer form, pg_try_advisory_lock(class, id), would make the
// separation structural, and is the change to make if a third kind of lock is
// ever added.
func NewAdvisoryLease(db Connector, name string) *AdvisoryLease {
	return &AdvisoryLease{db: db, name: name, key: LockKey(name)}
}

// LockKey maps a namespace to its 64-bit advisory-lock key. Exported so a
// runbook (or a test) can ask Postgres who holds it:
//
//	SELECT * FROM pg_locks WHERE locktype = 'advisory' AND objid = <key>::bigint;
func LockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name)) // hash.Hash never errors
	//nolint:gosec // deliberate wrap: an advisory key is 64 bits of namespace, not a magnitude.
	return int64(h.Sum64())
}

func (l *AdvisoryLease) Name() string { return l.name }

// Acquire reports whether this process holds the lease.
//
// A holder is verified rather than re-locked: the pinned session is pinged, and
// a session that answers still owns its advisory lock — only ending the session
// (or unlocking explicitly) releases one. A session that has gone away has
// already released the lock server-side, so the lease drops the dead connection
// and offers itself again on the same call.
//
// (false, nil) means someone else is the runner. That is the normal state of
// every task but one and is NOT an error: nothing is logged, nothing is
// counted, and the caller does no work.
func (l *AdvisoryLease) Acquire(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.conn != nil {
		if err := l.conn.PingContext(ctx); err == nil {
			return true, nil
		}
		l.dropConn()
	}

	conn, err := l.db.Connx(ctx)
	if err != nil {
		return false, fmt.Errorf("scheduler: pin lease connection: %w", err)
	}

	var acquired bool
	if err := conn.QueryRowxContext(ctx, `SELECT pg_try_advisory_lock($1)`, l.key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return false, fmt.Errorf("scheduler: try advisory lock %q: %w", l.name, err)
	}
	if !acquired {
		_ = conn.Close() // another task is the runner; hand the connection back.
		return false, nil
	}

	l.conn = conn
	return true, nil
}

// Release gives the lease up so a surviving task can take over immediately
// instead of waiting for this process's backend to be reaped. Closing the
// connection would be enough on its own; the explicit unlock is what makes a
// graceful shutdown hand over in one probe interval.
func (l *AdvisoryLease) Release(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.conn == nil {
		return
	}
	// Best effort: if the unlock cannot be sent, closing the session below
	// releases the lock anyway.
	_, _ = l.conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, l.key)
	l.dropConn()
}

// dropConn closes and forgets the pinned connection. Caller holds l.mu.
func (l *AdvisoryLease) dropConn() {
	if l.conn == nil {
		return
	}
	_ = l.conn.Close()
	l.conn = nil
}
