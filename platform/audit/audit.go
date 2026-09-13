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
// from users, never whole-record dumps. The envelope obeys the same rule, and
// more strictly: every field on it is server-generated or drawn from a closed
// vocabulary. The one value a CALLER supplies — the X-Request-Id header — is
// admitted only in the canonical UUID shape (NormalizeRequestID, enforced again
// by a CHECK on the column), because this store is exported to a bucket that
// keeps it for a decade and lets nobody delete from it.
//
// Correlation rule (ADR-0008, both streams): every entry names the CREDENTIAL
// its actor held — a session id or an API-token id — so the security stream's
// "this session began, from this address, after these refusals" joins to this
// stream's "and then it changed these rows". Attribution to a person is not
// enough for an incident review: two live sessions of one account are one actor
// in a trail that names only the user.
//
// Resolution rule — the one that makes the chain verifiable at all: the hashed
// instant must be an instant the column can store. audit_log.occurred_at is
// TIMESTAMPTZ, which keeps MICROseconds, so a hash covering nanoseconds can
// never be recomputed from the stored row (Record stamped time.Now() and
// hashed it as RFC3339Nano until the 2026-09-12 cut-over; on a Linux host,
// where the clock is nanosecond-granular, every entry written that way is
// permanently unverifiable — every row in the local database is). Both halves
// now agree on ChainResolution: Record truncates the instant it stamps, and
// ComputeHash formats it at exactly that resolution, so no future writer can
// reintroduce the bug by stamping a finer clock.
//
// Cut-over: hash_version records which hashing contract wrote a row — 1 for
// pre-cut-over entries (hashed at nanosecond precision; unverifiable by
// construction), 2 for the envelope as it stood on 2026-09-12, 3 for that
// envelope plus the acting credential. Verify treats a contiguous v1 PREFIX as
// pre-cut-over and reports the first verifiable seq; anything else that fails
// to recompute is tamper evidence, as before.
//
// Versions EXTEND rather than replace: ComputeHash renders the envelope of the
// version the ROW carries, so adding a field costs the rows written before it
// nothing. A version bump that made older rows merely link-checked would delete
// the product's tamper evidence with a one-line constant — it is the failure
// mode this design exists to prevent.
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

const (
	// ChainResolution is the resolution of a chain instant: the resolution
	// audit_log.occurred_at (TIMESTAMPTZ) stores, which is therefore the
	// finest resolution the hash may cover. Stamp and hash both go through it.
	ChainResolution = time.Microsecond

	// occurredAtLayout formats an instant at exactly ChainResolution — a fixed
	// six-digit fraction, unlike RFC3339Nano, which both carries digits
	// Postgres drops and varies its width with trailing zeros.
	occurredAtLayout = "2006-01-02T15:04:05.000000Z07:00"

	// HashVersionNanosecond is the pre-cut-over contract: occurred_at hashed at
	// nanosecond precision, which the TIMESTAMPTZ column never stored, so an
	// entry written under it can never be recomputed from its own row.
	HashVersionNanosecond int16 = 1

	// HashVersionMicrosecond hashes the instant at ChainResolution — the value
	// the column actually keeps — over the envelope as it stood on 2026-09-12.
	HashVersionMicrosecond int16 = 2

	// HashVersionCredential adds the acting CREDENTIAL (kind, then id) to the
	// end of that envelope, so the field the two ADR-0008 streams join on is
	// covered by the chain rather than sitting beside it, where a privileged
	// operator could rewrite who did what without breaking a single hash.
	HashVersionCredential int16 = 3

	// CurrentHashVersion is the hashing contract this build WRITES. It can
	// verify every version from HashVersionMicrosecond up: ComputeHash renders
	// the envelope of the version the row carries, so a cut-over adds a
	// contract instead of retiring the rows written under the last one.
	CurrentHashVersion = HashVersionCredential
)

// Entry is the stored envelope (ADR-0008). Exported with db tags so tests and
// future read endpoints can scan and re-verify the chain.
type Entry struct {
	EventID      string    `db:"event_id"`
	TenantID     string    `db:"tenant_id"`
	Seq          int64     `db:"seq"`
	ActorID      string    `db:"actor_id"`
	Action       string    `db:"action"`
	ResourceType string    `db:"resource_type"`
	ResourceID   string    `db:"resource_id"`
	OccurredAt   time.Time `db:"occurred_at"`
	RequestID    string    `db:"request_id"`
	// CredentialID is the credential the actor held when the change was made —
	// a session id or an API-token id — and CredentialKind which of the two
	// (app.CredentialSession / app.CredentialAPIToken). It is the join to
	// ADR-0008's second stream, whose security_events.session_id names the same
	// ids: the trail can now answer "what did THAT session change", not merely
	// "which person".
	//
	// Both are empty for an entry recorded by a principal that authenticated
	// with neither credential (the export scheduler's system actor), and for
	// every entry written before hash version HashVersionCredential.
	CredentialID   string          `db:"credential_id"`
	CredentialKind string          `db:"credential_kind"`
	Details        json.RawMessage `db:"details"`
	PrevHash       string          `db:"prev_hash"`
	Hash           string          `db:"hash"`
	// HashVersion is the hashing contract the row was written under (see
	// CurrentHashVersion). Zero means "not selected" — an entry loaded without
	// the column, or built in memory — and is verified STRICTLY, so a verifier
	// can never skip recomputation because a column was left out of a SELECT.
	HashVersion int16 `db:"hash_version"`
}

// Recorder appends audit entries on the caller's transaction. A nil *Recorder
// is a valid no-op, so use cases take it as an optional dependency (unit tests
// construct without one; the container always injects it).
type Recorder struct {
	db  database.ExecerPg
	now func() time.Time
}

func NewRecorder(db database.ExecerPg) *Recorder {
	return &Recorder{db: db, now: time.Now}
}

// NewRecorderWithClock builds a recorder over an explicit clock. It exists for
// one test: the host's clock decides whether a stamped instant even carries
// nanoseconds (macOS is microsecond-granular, Linux is not), so the round-trip
// that proves the stored row is verifiable has to supply the nanoseconds
// itself. Production uses NewRecorder.
func NewRecorderWithClock(db database.ExecerPg, now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	return &Recorder{db: db, now: now}
}

// stamp is the instant an entry is recorded at, truncated to the resolution the
// column stores so that the hashed value IS the stored value.
func (r *Recorder) stamp() time.Time {
	now := r.now
	if now == nil {
		now = time.Now
	}
	return now().UTC().Truncate(ChainResolution)
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

	credentialID, credentialKind := credentialOf(req)
	e := Entry{
		EventID:        uuid.NewString(),
		TenantID:       tenantID,
		Seq:            seq,
		ActorID:        req.ID,
		Action:         action,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		OccurredAt:     r.stamp(),
		RequestID:      requestIDOf(ctx),
		CredentialID:   credentialID,
		CredentialKind: credentialKind,
		Details:        detailsJSON,
		PrevHash:       prevHash,
		HashVersion:    CurrentHashVersion,
	}
	e.Hash = ComputeHash(&e)

	// hash_version is written explicitly: the column defaults to 1
	// (unverifiable) so that any writer unaware of the cut-over is recorded as
	// such instead of claiming a verifiability it does not have.
	//
	// The credential columns are UUID / a two-value vocabulary, not text, and
	// the pair is absent together or present together (the CHECK says so), so
	// "no credential" cannot be spelled two ways in the store and hashed one.
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO audit_log (event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
		                       occurred_at, request_id, credential_id, credential_kind,
		                       details, prev_hash, hash, hash_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, '')::uuid, NULLIF($11, ''),
		        $12, $13, $14, $15)`,
		e.EventID, e.TenantID, e.Seq, e.ActorID, e.Action, e.ResourceType, e.ResourceID,
		e.OccurredAt, e.RequestID, e.CredentialID, e.CredentialKind,
		string(e.Details), e.PrevHash, e.Hash, e.HashVersion)
	if err != nil {
		return fmt.Errorf("audit: append: %w", err)
	}
	return nil
}

// credentialOf reads the acting credential off the requester, refusing to let
// half of it through: an id without a kind (or the reverse) would be a join key
// pointing at no table, and the column pair's CHECK would reject the row
// anyway — after the hash had already been computed over it.
func credentialOf(req *app.Requester) (id, kind string) {
	switch {
	case req == nil, req.CredentialID == "", req.CredentialKind == "":
		return "", ""
	case req.CredentialKind != app.CredentialSession && req.CredentialKind != app.CredentialAPIToken:
		return "", ""
	default:
		return req.CredentialID, req.CredentialKind
	}
}

// requestIDOf is the writer-side half of the rule the middleware states: only a
// canonical request id is stored. The middleware already guarantees it for
// every HTTP path, so this gate is for the paths that have no middleware —
// scheduled work, a future queue consumer, a test — where an ad-hoc string
// would otherwise be hashed into an append-only, compliance-locked store. A
// value of any other shape costs the correlation, never the transaction.
func requestIDOf(ctx context.Context) string {
	id, ok := app.NormalizeRequestID(app.GetRequestId(ctx))
	if !ok {
		return ""
	}
	return id
}

// ComputeHash returns the canonical sha256 (hex) of an entry under the hashing
// contract the entry itself carries (HashVersion; zero means the current one).
// The exact STORED values are hashed — occurred_at at ChainResolution in UTC,
// details re-canonicalized through a map so write-side and verify-side agree
// regardless of jsonb formatting — which is what lets the chain be recomputed
// from nothing but the rows.
//
// Versions EXTEND: HashVersionCredential is the HashVersionMicrosecond input
// with two fields appended, so an older row keeps hashing exactly as it did the
// day it was written and a cut-over never costs the entries before it their
// verifiability. That is the whole reason this function reads e.HashVersion
// instead of the package constant.
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
		e.OccurredAt.UTC().Truncate(ChainResolution).Format(occurredAtLayout),
		e.RequestID,
		string(mustRecanonicalize(e.Details)),
	}
	if HashesCredential(e.HashVersion) {
		fields = append(fields, e.CredentialKind, e.CredentialID)
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(sum[:])
}

// HashesCredential reports whether entries written under a hashing contract
// cover the acting credential. Version 0 ("not selected", or an entry built in
// memory) reads as the current contract, the same fail-safe Verify applies:
// an omitted column must never be a way to hash less than the writer did.
func HashesCredential(version int16) bool {
	return version == 0 || version >= HashVersionCredential
}

// IsPreCutover reports whether a hashing contract produced a hash that cannot
// be recomputed from the stored row — true only of HashVersionNanosecond, which
// hashed an instant finer than the column keeps. Every later version is
// recomputable, so a version bump link-checks nothing it could verify.
//
// Version 0 is NOT pre-cut-over: it means the column was not selected, and the
// fail-safe reading of "unknown" is to demand recomputation.
func IsPreCutover(version int16) bool {
	return version > 0 && version < HashVersionMicrosecond
}

// Report is what one verification run can tell an operator.
type Report struct {
	// Entries examined.
	Entries int
	// PreCutoverEntries is how many leading entries predate the hash-chain
	// cut-over (hash_version < HashVersionMicrosecond) and were therefore link-
	// checked but not recomputed.
	PreCutoverEntries int
	// FirstVerifiableSeq is the seq of the first entry whose hash was actually
	// recomputed; 0 when the range holds no verifiable entry at all.
	FirstVerifiableSeq int64
	// LastVerifiedSeq is the seq of the last entry whose hash recomputed.
	LastVerifiedSeq int64
}

func (r Report) String() string {
	switch {
	case r.Entries == 0:
		return "audit chain: empty"
	case r.PreCutoverEntries == 0:
		return fmt.Sprintf("audit chain: %d entries, hash-verified seq %d..%d",
			r.Entries, r.FirstVerifiableSeq, r.LastVerifiedSeq)
	case r.FirstVerifiableSeq == 0:
		return fmt.Sprintf("audit chain: %d entries, ALL pre-cut-over (hash format < v%d) — links intact, no entry hash-verifiable",
			r.Entries, HashVersionMicrosecond)
	default:
		return fmt.Sprintf("audit chain: %d entries, %d pre-cut-over (links intact, not recomputable), hash-verified seq %d..%d",
			r.Entries, r.PreCutoverEntries, r.FirstVerifiableSeq, r.LastVerifiedSeq)
	}
}

// Verify walks one tenant's chain (entries ordered by seq ascending, starting
// at seq 1) and returns what it could prove. It checks sequence continuity and
// prev_hash linkage for EVERY entry, and recomputes the hash of every entry
// written under a recomputable contract — each under the envelope of the
// version it carries, so a chain that spans a cut-over verifies on both sides
// of it.
//
// Pre-cut-over entries (hash_version < HashVersionMicrosecond) hashed an
// instant finer than the column stores, so their hash cannot be recomputed from
// the row. They are tolerated only as a contiguous PREFIX — their links are
// still checked, and Report.FirstVerifiableSeq says where recomputation began. A
// pre-cut-over entry AFTER a verified one is an error: relabelling a row in the
// middle of a live chain is not a cut-over, it is an attempt to exempt a row
// from recomputation.
//
// An error names the first broken link. Use VerifyChain when the whole range
// must verify.
func Verify(entries []Entry) (Report, error) {
	rep := Report{Entries: len(entries)}
	prev := GenesisHash
	for i := range entries {
		e := &entries[i]
		if e.Seq != int64(i+1) {
			return rep, fmt.Errorf("audit chain broken at index %d: seq %d, want %d (missing or reordered entry)", i, e.Seq, i+1)
		}
		if e.PrevHash != prev {
			return rep, fmt.Errorf("audit chain broken at seq %d: prev_hash mismatch", e.Seq)
		}
		switch {
		case e.HashVersion > CurrentHashVersion:
			return rep, fmt.Errorf("audit chain unverifiable at seq %d: hash format v%d is newer than this verifier's v%d (upgrade the verifier)",
				e.Seq, e.HashVersion, CurrentHashVersion)
		case IsPreCutover(e.HashVersion):
			if rep.FirstVerifiableSeq != 0 {
				return rep, fmt.Errorf("audit chain broken at seq %d: pre-cut-over entry (hash format v%d) after verifiable entry seq %d",
					e.Seq, e.HashVersion, rep.FirstVerifiableSeq)
			}
			rep.PreCutoverEntries++
		default:
			if got := ComputeHash(e); got != e.Hash {
				return rep, fmt.Errorf("audit chain broken at seq %d: hash mismatch (entry modified)", e.Seq)
			}
			if rep.FirstVerifiableSeq == 0 {
				rep.FirstVerifiableSeq = e.Seq
			}
			rep.LastVerifiedSeq = e.Seq
		}
		prev = e.Hash
	}
	return rep, nil
}

// VerifyChain is Verify with nothing forgiven: every entry in the range must
// recompute, so a chain carrying pre-cut-over entries is an error naming the
// first seq that can be verified. This is the form to assert in tests and in
// any "the chain is intact" claim.
func VerifyChain(entries []Entry) error {
	rep, err := Verify(entries)
	if err != nil {
		return err
	}
	switch {
	case rep.PreCutoverEntries == 0:
		return nil
	case rep.FirstVerifiableSeq == 0:
		return fmt.Errorf("audit chain not verifiable: all %d entries predate the hash-chain cut-over (hash format < v%d)",
			rep.PreCutoverEntries, HashVersionMicrosecond)
	default:
		return fmt.Errorf("audit chain verifiable only from seq %d: %d earlier entries predate the hash-chain cut-over (hash format < v%d)",
			rep.FirstVerifiableSeq, rep.PreCutoverEntries, HashVersionMicrosecond)
	}
}

// ChainColumns is the column list a verifier must read — hash_version
// included, or every pre-cut-over row reads as tampering, and the credential
// pair included, or every entry that names one reads as modified. The
// credential columns are nullable (an entry can have no credential) and are
// read as their canonical empty string, which is the form the hash covers.
const ChainColumns = `event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
	                  occurred_at, request_id,
	                  COALESCE(credential_id::text, '') AS credential_id,
	                  COALESCE(credential_kind, '') AS credential_kind,
	                  details, prev_hash, hash, hash_version`

// LoadChain reads one tenant's whole chain, oldest first, ready for Verify.
// audit_log is RLS'd, so db must be bound to that tenant (the Tx seam does it);
// the explicit predicate is belt and braces.
func LoadChain(ctx context.Context, db database.ExecerPg, tenantID string) ([]Entry, error) {
	var entries []Entry
	if err := db.SelectContext(ctx, &entries,
		`SELECT `+ChainColumns+` FROM audit_log WHERE tenant_id = $1 ORDER BY seq ASC`, tenantID); err != nil {
		return nil, fmt.Errorf("audit: load chain: %w", err)
	}
	return entries, nil
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
