package worm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Ledger is one row of audit_export_segments: a segment that has been cut, and
// either has landed in the object store or has not landed YET.
type Ledger struct {
	ID            string     `db:"id"`
	TenantID      string     `db:"tenant_id"`
	SegmentSeq    int64      `db:"segment_seq"`
	FromSeq       int64      `db:"from_seq"`
	ToSeq         int64      `db:"to_seq"`
	Entries       int        `db:"entries"`
	StartPrevHash string     `db:"start_prev_hash"`
	EndHash       string     `db:"end_hash"`
	ObjectKey     string     `db:"object_key"`
	FormatVersion int        `db:"format_version"`
	Digest        string     `db:"digest"`
	SizeBytes     int64      `db:"size_bytes"`
	ExportedAt    time.Time  `db:"exported_at"`
	Status        string     `db:"status"`
	UploadedAt    *time.Time `db:"uploaded_at"`
	Attempts      int        `db:"attempts"`
}

// Segment statuses.
const (
	StatusPending  = "pending"
	StatusUploaded = "uploaded"
)

const ledgerColumns = `id, tenant_id, segment_seq, from_seq, to_seq, entries, start_prev_hash, end_hash,
	object_key, format_version, digest, size_bytes, exported_at, status, uploaded_at, attempts`

// Store is the export's Postgres side: the ledger of segments and the reads
// over the chain itself.
//
// Every method runs on whatever transaction the context carries, and every
// tenant-scoped statement relies on the ordinary app.tenant_id binding (the Tx
// seam sets it). The export needs NO cross-tenant door of the kind the outbox
// delivery loop has: it works one tenant at a time, bound to that tenant, so
// audit_log's tenant_read policy and audit_export_segments' tenant_isolation
// policy are the only doors it goes through.
type Store struct {
	db database.ExecerPg
}

func NewStore(db database.ExecerPg) *Store {
	return &Store{db: db}
}

// LockChain takes the per-tenant chain lock — the SAME advisory lock
// platform/audit.Record serializes appends on. Holding it across "read the
// head, append the export record, read the range" is what makes the cut atomic
// with respect to concurrent domain writes: nothing can slip an entry in
// between the head this pass measured and the record it appends.
//
// It is an xact lock, so it is released by commit or rollback and cannot be
// leaked.
func (s *Store) LockChain(ctx context.Context, tenantID string) error {
	if _, err := s.db.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenantID); err != nil {
		return fmt.Errorf("worm: acquire chain lock: %w", err)
	}
	return nil
}

// ChainHead is the tenant's highest audit_log.seq, or 0 for a tenant that has
// never been written to.
func (s *Store) ChainHead(ctx context.Context, tenantID string) (int64, error) {
	var head int64
	if err := s.db.GetContext(ctx, &head,
		`SELECT COALESCE(MAX(seq), 0) FROM audit_log WHERE tenant_id = $1`, tenantID); err != nil {
		return 0, fmt.Errorf("worm: read chain head: %w", err)
	}
	return head, nil
}

// LoadRange reads one contiguous stretch of a tenant's chain, oldest first. It
// selects audit.ChainColumns — the one list the writer and every verifier
// agree on, rather than a copy that could drift a column behind it: an entry
// loaded without hash_version verifies STRICTLY, and one loaded without the
// credential pair recomputes to a different hash, so either omission turns
// sound rows into false tamper evidence.
func (s *Store) LoadRange(ctx context.Context, tenantID string, from, to int64) ([]audit.Entry, error) {
	var entries []audit.Entry
	if err := s.db.SelectContext(ctx, &entries,
		`SELECT `+audit.ChainColumns+`
		   FROM audit_log
		  WHERE tenant_id = $1 AND seq BETWEEN $2 AND $3
		  ORDER BY seq ASC`, tenantID, from, to); err != nil {
		return nil, fmt.Errorf("worm: load chain range: %w", err)
	}
	want := int(to - from + 1)
	if len(entries) != want {
		// The range was measured under the chain lock, so a short read is not a
		// race: it is a hole in an append-only ledger.
		return nil, fmt.Errorf("worm: chain range %d..%d for tenant %s returned %d entries, want %d (entries are missing)",
			from, to, tenantID, len(entries), want)
	}
	return entries, nil
}

// PreCutoverThrough is the last seq in a range that was written under the one
// hashing contract whose hashes cannot be recomputed at all (audit.IsPreCutover
// — version 1, which hashed an instant finer than the column keeps), or 0 when
// the range holds none.
//
// The export STATES this number inside the entry that records the cut, so the
// cut-over exemption is bounded by something the chain covers rather than by
// whatever a file's leading entries claim about themselves. It is read before
// the record is appended, on the same locked transaction, so it describes the
// same rows the segment will carry.
func (s *Store) PreCutoverThrough(ctx context.Context, tenantID string, from, to int64) (int64, error) {
	var through int64
	if err := s.db.GetContext(ctx, &through,
		`SELECT COALESCE(MAX(seq), 0)
		   FROM audit_log
		  WHERE tenant_id = $1 AND seq BETWEEN $2 AND $3
		    AND hash_version > 0 AND hash_version < $4`,
		tenantID, from, to, audit.HashVersionMicrosecond); err != nil {
		return 0, fmt.Errorf("worm: read pre-cut-over prefix: %w", err)
	}
	return through, nil
}

// Pending returns the tenant's unfinished segment, if it has one. At most one
// can exist (uq_audit_export_segments_pending), which is the whole crash-safety
// story: a pass that finds one FINISHES it rather than cutting a new range.
func (s *Store) Pending(ctx context.Context, tenantID string) (*Ledger, error) {
	return s.one(ctx,
		`SELECT `+ledgerColumns+` FROM audit_export_segments
		  WHERE tenant_id = $1 AND status = $2`, tenantID, StatusPending)
}

// LastLanded returns the tenant's most recent uploaded segment — the cursor the
// next range starts after, and the segment the next one points back to.
func (s *Store) LastLanded(ctx context.Context, tenantID string) (*Ledger, error) {
	return s.one(ctx,
		`SELECT `+ledgerColumns+` FROM audit_export_segments
		  WHERE tenant_id = $1 AND status = $2
		  ORDER BY segment_seq DESC LIMIT 1`, tenantID, StatusUploaded)
}

// Before returns the segment immediately preceding the given ordinal, so a
// rebuilt segment can point back at exactly what it pointed at when it was cut.
func (s *Store) Before(ctx context.Context, tenantID string, segmentSeq int64) (*Ledger, error) {
	if segmentSeq <= 1 {
		return nil, nil
	}
	return s.one(ctx,
		`SELECT `+ledgerColumns+` FROM audit_export_segments
		  WHERE tenant_id = $1 AND segment_seq = $2`, tenantID, segmentSeq-1)
}

func (s *Store) one(ctx context.Context, query string, args ...any) (*Ledger, error) {
	var row Ledger
	err := s.db.GetContext(ctx, &row, query, args...)
	switch {
	case err == nil:
		return &row, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	default:
		return nil, fmt.Errorf("worm: read export ledger: %w", err)
	}
}

// Claim writes the pending row that reserves a range. It commits with the audit
// entry that records the export, so the trail can never claim an export the
// ledger does not know about, or the reverse.
func (s *Store) Claim(ctx context.Context, row Ledger) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_export_segments
			(id, tenant_id, segment_seq, from_seq, to_seq, entries, start_prev_hash, end_hash,
			 object_key, format_version, digest, size_bytes, exported_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		row.ID, row.TenantID, row.SegmentSeq, row.FromSeq, row.ToSeq, row.Entries,
		row.StartPrevHash, row.EndHash, row.ObjectKey, row.FormatVersion, row.Digest,
		row.SizeBytes, row.ExportedAt)
	if err != nil {
		return fmt.Errorf("worm: claim segment %d..%d: %w", row.FromSeq, row.ToSeq, err)
	}
	return nil
}

// Confirm records that the object landed. Only then does the cursor move.
func (s *Store) Confirm(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE audit_export_segments
		   SET status = $2, uploaded_at = NOW(), last_error = NULL, updated_at = NOW()
		 WHERE id = $1 AND status = $3`, id, StatusUploaded, StatusPending)
	if err != nil {
		return fmt.Errorf("worm: confirm segment %s: %w", id, err)
	}
	return nil
}

// Restate updates the document facts of a PENDING segment after it was rebuilt
// under a newer document format than the binary that cut it. The range, the key
// and the chain endpoints cannot move (the database refuses, SQLSTATE ZT030):
// what the segment attests to is fixed at the cut; only how it is written can
// change.
func (s *Store) Restate(ctx context.Context, id string, formatVersion int, digest string, size int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE audit_export_segments
		   SET format_version = $2, digest = $3, size_bytes = $4, updated_at = NOW()
		 WHERE id = $1 AND status = $5`, id, formatVersion, digest, size, StatusPending)
	if err != nil {
		return fmt.Errorf("worm: restate segment %s: %w", id, err)
	}
	return nil
}

// Fail counts a failed upload attempt and keeps the reason. The row stays
// pending — the range is still owed — so the next pass retries the same bytes
// under the same key rather than cutting past them.
func (s *Store) Fail(ctx context.Context, id string, reason error) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE audit_export_segments
		   SET attempts = attempts + 1, last_error = $2, updated_at = NOW()
		 WHERE id = $1 AND status = $3`, id, truncateError(reason), StatusPending)
	if err != nil {
		return fmt.Errorf("worm: record failed upload of segment %s: %w", id, err)
	}
	return nil
}

// TenantIDs lists every tenant in the registry, oldest first.
//
// Every tenant, not only the active ones: a suspended or closing tenant is
// precisely the one whose evidence must be taken before its data goes (ADR-0007
// erasure), and an export is a read plus one append — it neither revives an
// account nor bills anyone.
func (s *Store) TenantIDs(ctx context.Context) ([]string, error) {
	var ids []string
	if err := s.db.SelectContext(ctx, &ids,
		`SELECT id::text FROM tenants ORDER BY created_at, id`); err != nil {
		return nil, fmt.Errorf("worm: list tenants: %w", err)
	}
	return ids, nil
}

// truncateError bounds what goes in last_error: an operator's diagnostic, not a
// provider's essay.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	const max = 500
	msg := err.Error()
	if len(msg) > max {
		return msg[:max] + "…"
	}
	return msg
}
