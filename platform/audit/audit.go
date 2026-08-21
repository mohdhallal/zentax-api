// Package audit implements the application/business audit trail (ADR-0008,
// stream 1): a PII-free, actor-by-ID, append-only event log with a per-tenant
// hash chain for tamper evidence.
//
// Integrity model:
//   - Each entry's hash = sha256(canonical envelope incl. prev_hash), so any
//     edit, deletion, or reorder within a tenant's chain breaks recomputation.
//   - Appends within a tenant are serialized by a per-tenant advisory xact lock
//     (parallel across tenants); seq is per-tenant monotonic from 1.
//   - The table is append-only at the database (RLS policies only for
//     INSERT/SELECT); audit failures fail the enclosing transaction — the
//     domain write and its evidence commit or roll back together.
//
// PII rule (ADR-0007/0008): details carries only explicitly whitelisted
// non-PII values (enum transitions, counts, ids). Never names, never free text
// from users, never whole-record dumps.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// GenesisHash is the prev_hash of the first entry in a tenant's chain.
var GenesisHash = strings.Repeat("0", 64)

// Entry is the stored envelope (ADR-0008). Exported with db tags so tests and
// future read endpoints can scan and re-verify the chain.
type Entry struct {
	EventID      string          `db:"event_id"`
	TenantID     string          `db:"tenant_id"`
	Seq          int64           `db:"seq"`
	ActorID      string          `db:"actor_id"`
	Action       string          `db:"action"`
	ResourceType string          `db:"resource_type"`
	ResourceID   string          `db:"resource_id"`
	OccurredAt   time.Time       `db:"occurred_at"`
	RequestID    string          `db:"request_id"`
	Details      json.RawMessage `db:"details"`
	PrevHash     string          `db:"prev_hash"`
	Hash         string          `db:"hash"`
}

// Recorder appends audit entries on the caller's transaction. A nil *Recorder
// is a valid no-op, so use cases take it as an optional dependency (unit tests
// construct without one; the container always injects it).
type Recorder struct {
	db database.ExecerPg
}

func NewRecorder(db database.ExecerPg) *Recorder {
	return &Recorder{db: db}
}

// Record appends one audit entry for the acting user on the current tenant
// transaction. details must contain only non-PII values (see package doc); nil
// is fine. Errors propagate so the enclosing transaction rolls back — a domain
// write must not commit without its evidence.
func (r *Recorder) Record(ctx context.Context, action, resourceType, resourceID string, details map[string]any) error {
	if r == nil {
		return nil
	}

	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return errors.New("audit: no tenant in context")
	}
	req := app.GetRequester(ctx)
	if req == nil || !req.IsUser() || req.ID == "" {
		return errors.New("audit: no acting user in context")
	}

	detailsJSON, err := canonicalDetails(details)
	if err != nil {
		return fmt.Errorf("audit: encode details: %w", err)
	}

	// Serialize appends within this tenant's chain (parallel across tenants).
	// Advisory xact locks release automatically at commit/rollback.
	if _, err := r.db.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenantID); err != nil {
		return fmt.Errorf("audit: acquire chain lock: %w", err)
	}

	var last struct {
		Seq  int64  `db:"seq"`
		Hash string `db:"hash"`
	}
	err = r.db.GetContext(ctx, &last,
		`SELECT seq, hash FROM audit_log WHERE tenant_id = $1 ORDER BY seq DESC LIMIT 1`, tenantID)
	prevHash := GenesisHash
	seq := int64(1)
	switch {
	case err == nil:
		prevHash = last.Hash
		seq = last.Seq + 1
	case isNoRows(err):
		// genesis
	default:
		return fmt.Errorf("audit: read chain head: %w", err)
	}

	e := Entry{
		EventID:      uuid.NewString(),
		TenantID:     tenantID,
		Seq:          seq,
		ActorID:      req.ID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		OccurredAt:   time.Now().UTC(),
		RequestID:    app.GetRequestId(ctx),
		Details:      detailsJSON,
		PrevHash:     prevHash,
	}
	e.Hash = ComputeHash(&e)

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO audit_log (event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
		                       occurred_at, request_id, details, prev_hash, hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		e.EventID, e.TenantID, e.Seq, e.ActorID, e.Action, e.ResourceType, e.ResourceID,
		e.OccurredAt, e.RequestID, string(e.Details), e.PrevHash, e.Hash)
	if err != nil {
		return fmt.Errorf("audit: append: %w", err)
	}
	return nil
}

// ComputeHash returns the canonical sha256 (hex) of an entry. The exact stored
// values are hashed — occurred_at in RFC3339Nano UTC, details re-canonicalized
// through a map so write-side and verify-side agree regardless of jsonb
// formatting.
func ComputeHash(e *Entry) string {
	fields := []string{
		e.PrevHash,
		e.EventID,
		e.TenantID,
		strconv.FormatInt(e.Seq, 10),
		e.ActorID,
		e.Action,
		e.ResourceType,
		e.ResourceID,
		e.OccurredAt.UTC().Format(time.RFC3339Nano),
		e.RequestID,
		string(mustRecanonicalize(e.Details)),
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(sum[:])
}

// VerifyChain recomputes one tenant's chain (entries ordered by seq ascending)
// and returns an error naming the first broken link, or nil if intact.
func VerifyChain(entries []Entry) error {
	prev := GenesisHash
	for i := range entries {
		e := &entries[i]
		if e.Seq != int64(i+1) {
			return fmt.Errorf("audit chain broken at index %d: seq %d, want %d (missing or reordered entry)", i, e.Seq, i+1)
		}
		if e.PrevHash != prev {
			return fmt.Errorf("audit chain broken at seq %d: prev_hash mismatch", e.Seq)
		}
		if got := ComputeHash(e); got != e.Hash {
			return fmt.Errorf("audit chain broken at seq %d: hash mismatch (entry modified)", e.Seq)
		}
		prev = e.Hash
	}
	return nil
}

// canonicalDetails encodes details deterministically: encoding/json sorts map
// keys, giving a canonical byte form for hashing and storage.
func canonicalDetails(details map[string]any) (json.RawMessage, error) {
	if details == nil {
		details = map[string]any{}
	}
	return json.Marshal(details)
}

// mustRecanonicalize round-trips stored jsonb text through a map so hashing is
// stable across Postgres jsonb key-ordering/whitespace normalization.
func mustRecanonicalize(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw // non-object payloads hash as-is
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
