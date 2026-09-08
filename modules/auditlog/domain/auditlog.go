// Package domain is the read model for the user-facing Audit Trail (ADR-0008
// stream 1). The stored envelope is PII-free and actor-by-ID; the display
// name is resolved at READ time from the (erasable) user row — exactly what
// the ADR prescribes — so an erased user simply renders as no name.
package domain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Entry is one audit event as listed: the stored envelope plus the read-time
// enrichments (actor name, resolved workflow id + name). tenant_id and
// prev_hash are omitted: the former is RLS infra, the latter is verification
// detail (VerifyChain reads the raw table).
type Entry struct {
	ID           string          `db:"event_id"`
	Seq          int64           `db:"seq"`
	Action       string          `db:"action"`
	ResourceType string          `db:"resource_type"`
	ResourceID   string          `db:"resource_id"`
	ActorID      string          `db:"actor_id"`
	ActorName    *string         `db:"actor_name"`
	OccurredAt   time.Time       `db:"occurred_at"`
	RequestID    string          `db:"request_id"`
	Details      json.RawMessage `db:"details"`
	WorkflowID   *string         `db:"workflow_id"`
	WorkflowName *string         `db:"workflow_name"`
	Hash         string          `db:"hash"`
}

// ListArgs are the validated filters + paging. nil / empty means "any". From/To
// are legal calendar dates (inclusive, UTC day boundaries on occurred_at). The
// WorkflowID filter applies to the RESOLVED workflow — a workflow's own events
// plus those of its task templates and task instances. Actions is a set
// (OR-ed): the UI maps one verb choice onto several stored actions. The
// repository assembles a predicate ONLY for the filters that are set.
type ListArgs struct {
	WorkflowID   *string
	ResourceType *string
	ResourceID   *string
	Actions      []string
	From         *dateonly.Date
	To           *dateonly.Date
	Limit        int
	Offset       int
}

// Reader is the read-side port. Sorted seq DESC (ledger order, chronological by construction). Every query
// runs on the request transaction, so RLS confines it to the session tenant.
type Reader interface {
	List(ctx context.Context, args ListArgs) ([]Entry, int, error)
}
