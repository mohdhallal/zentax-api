// Package worm is the WORM export of the application audit trail (ADR-0008,
// integrity triad #3) — the scheduled job that writes each tenant's hash chain
// out to object storage as self-contained, independently verifiable segments,
// and the format those segments are written in.
//
// WHY. The chain already makes the trail tamper-EVIDENT, but it evidences
// tampering only for as long as the rows exist: a privileged operator can drop
// a partition, a restore can lose a window, and a tenant whose data has been
// erased leaves nothing behind at all. Nor did anything ever RUN the verifier —
// it existed for tests. An auditor could therefore be shown a trail, but never
// handed one. This package is both halves of the answer: the chain leaves the
// database on a schedule, and it is verified on the way out.
//
// WHAT A SEGMENT IS. One file, one tenant, one contiguous stretch of that
// tenant's chain, carrying:
//
//   - every entry in the range, in the exact form the hash covers;
//   - the range itself (first and last seq, the count) and the chain state at
//     BOTH ends — the prev_hash the first entry links back to, and the hash of
//     the last entry — so a reader knows what the segment claims before
//     checking it;
//   - a pointer to the previous segment (its key, ordinal, end hash and
//     digest), so a SERIES of files is verifiable as a series and a missing
//     file is detectable;
//   - a sha256 digest of the segment document, inside the same file; and
//   - the recipe (Verification) for recomputing both the digest and every
//     entry hash, so a reader who has only the file — no database, no ZenTax
//     source, no Go — can verify it.
//
// WHAT A FILE CANNOT SAY ABOUT ITSELF. A segment's header — when it was
// exported, its id, the window it claims to cover, the predecessor it names —
// is the document's own words about itself, and no entry hash covers it. So the
// writer restates it inside the `audit.exported` ENTRY that the same cut
// appends to the tenant's chain (RecordDetails): that entry's hash covers its
// details, the chain covers the entry, and the next segment covers the chain.
// VerifyDocument cross-checks the two. The same entry states how far the
// tenant's unrecomputable historical prefix reaches, which is what anchors the
// pre-cut-over exemption to the beginning of a TENANT rather than to the top of
// whatever file is in front of the reader.
//
// THE FILE IS {"segment": …, "digest": "…"}. The digest covers the EXACT BYTES
// of the segment member as they appear in the file, so verification never has
// to re-serialise anything and can never disagree with the writer about
// whitespace or key order. cmd/verify-audit-segment is the reference reader;
// README.md in this directory states the procedure for anyone reimplementing
// it.
package worm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

const (
	// Format and FormatVersion identify the document shape. A future change of
	// shape takes the next version; a reader must refuse a version it does not
	// know rather than half-verify it.
	Format        = "zentax.audit.segment"
	FormatVersion = 1

	// ContentType is what the object is stored as.
	ContentType = "application/json"

	// OccurredAtLayout renders an instant at exactly audit.ChainResolution
	// (microseconds) — the resolution the audit_log column stores and therefore
	// the only resolution the hash may cover. It is deliberately identical to
	// the layout platform/audit hashes with; TestOccurredAtFormatMatchesTheHash
	// pins the two together, because a segment whose timestamps round-trip
	// differently would be a file full of unverifiable entries.
	OccurredAtLayout = "2006-01-02T15:04:05.000000Z07:00"

	// digestPrefix labels the digest so the algorithm travels with the value.
	digestPrefix = "sha256:"
)

// entryHashFields is the canonical field order platform/audit.ComputeHash joins
// with a newline before hashing. It is published INSIDE every segment so a
// reader can reimplement the hash; TestDocumentedRecipeReproducesTheChainHash
// checks the published recipe against the real implementation.
//
// It is the list for hashVersion 2. Later versions APPEND to it (see
// credentialHashFields), which is why the recipe publishes both lists and every
// entry carries the version it was hashed under: an older entry inside a newer
// file still hashes the way it did the day it was written.
var entryHashFields = []string{
	"prevHash", "eventId", "tenantId", "seq", "actorId", "action",
	"resourceType", "resourceId", "occurredAt", "requestId", "details",
}

// credentialHashFields is what hashVersion 3 appends: the credential the actor
// authenticated with — kind before id, both empty strings when there was none.
var credentialHashFields = []string{"credentialKind", "credentialId"}

// entryHashFieldsFor is the field order one hashing contract joins.
func entryHashFieldsFor(version int16) []string {
	fields := append([]string(nil), entryHashFields...)
	if audit.HashesCredential(version) {
		fields = append(fields, credentialHashFields...)
	}
	return fields
}

// File is the object as it is stored: the segment document, and a digest over
// that document's exact bytes.
type File struct {
	Segment json.RawMessage `json:"segment"`
	Digest  string          `json:"digest"`
}

// Document is the segment itself.
type Document struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"formatVersion"`

	SegmentID  string `json:"segmentId"`
	SegmentSeq int64  `json:"segmentSeq"`
	TenantID   string `json:"tenantId"`
	// ExportedAt is when the range was CUT (not when the file landed), in
	// OccurredAtLayout. A retry after a crash reuses it, so the rebuilt
	// document is byte-identical to the one that may already be in the store.
	ExportedAt string `json:"exportedAt"`

	Range        Range        `json:"range"`
	Chain        Chain        `json:"chain"`
	Previous     *Previous    `json:"previousSegment"`
	Verification Verification `json:"verification"`
	Entries      []Entry      `json:"entries"`
}

// Range is what the segment covers.
type Range struct {
	FromSeq         int64  `json:"fromSeq"`
	ToSeq           int64  `json:"toSeq"`
	Entries         int    `json:"entries"`
	FirstOccurredAt string `json:"firstOccurredAt"`
	LastOccurredAt  string `json:"lastOccurredAt"`
}

// Chain is the chain state at both ends of the range.
type Chain struct {
	// StartPrevHash is the prev_hash of the first entry: the previous
	// segment's EndHash, or GenesisPrevHash for the first segment of a tenant.
	StartPrevHash string `json:"startPrevHash"`
	// EndHash is the hash of the last entry — the value the NEXT segment must
	// start from.
	EndHash string `json:"endHash"`
	// Genesis is true when this segment starts at the beginning of the tenant's
	// chain, which a reader can check standalone (seq 1, prev_hash all zeros).
	Genesis bool `json:"genesis"`
}

// Previous points at the segment before this one, so a reader holding a folder
// of files can tell a complete series from one with a hole in it.
type Previous struct {
	SegmentID  string `json:"segmentId"`
	SegmentSeq int64  `json:"segmentSeq"`
	ObjectKey  string `json:"objectKey"`
	ToSeq      int64  `json:"toSeq"`
	EndHash    string `json:"endHash"`
	Digest     string `json:"digest"`
}

// RecordDetails is the payload of the `audit.exported` entry every cut appends
// to the tenant's chain — and, because an entry's hash covers its details, the
// one place a segment's HEADER can be stated where nobody can edit it.
//
// The header (segmentId, exportedAt, the range, the predecessor's digest) is
// covered by no entry hash: it is the file talking about itself, and a file
// that talks about itself can be made to say anything. Restating it here binds
// it — changing one of these values now means changing an entry's details,
// which changes that entry's hash, which breaks the chain from there on and,
// through chain.endHash, every later segment too.
//
// PreCutoverThroughSeq is the same trick applied to the cut-over exemption: the
// product STATES, inside the seal, exactly how far its unrecomputable
// historical prefix reaches, so no later entry can be relabelled into it.
//
// The record for a segment is normally the LAST ENTRY OF THAT SEGMENT. A range
// capped by MaxEntriesPerSegment stops short of it, and then a later segment of
// the series carries it — which is why VerifySeries finishes a job
// VerifyDocument can only start.
type RecordDetails struct {
	SegmentSeq int64  `json:"segmentSeq"`
	SegmentID  string `json:"segmentId"`
	FromSeq    int64  `json:"fromSeq"`
	ToSeq      int64  `json:"toSeq"`
	Entries    int64  `json:"entries"`
	ObjectKey  string `json:"objectKey"`
	ExportedAt string `json:"exportedAt"`
	// PreviousDigest is the digest of the segment before this one, empty when
	// this is the tenant's first segment — so "this is where the chain begins"
	// is a sealed claim rather than a field anyone can set.
	PreviousDigest string `json:"previousDigest"`
	// PreCutoverThroughSeq is the last seq IN THIS RANGE that was written under
	// an older hashing contract, 0 when none was.
	PreCutoverThroughSeq int64 `json:"preCutoverThroughSeq"`
	// FormatVersion is the document shape the range was cut as. It is recorded
	// but deliberately NOT cross-checked: a pending segment may legitimately be
	// rebuilt under a newer shape (see Exporter.rebuild), and what the segment
	// attests to does not change when the way it is written does.
	FormatVersion int `json:"formatVersion"`
}

// Map renders the details in the form audit.Recorder takes.
// TestRecordDetailsSurviveTheTrail pins it to the struct tags above.
func (d RecordDetails) Map() map[string]any {
	return map[string]any{
		"segmentSeq":           d.SegmentSeq,
		"segmentId":            d.SegmentID,
		"fromSeq":              d.FromSeq,
		"toSeq":                d.ToSeq,
		"entries":              d.Entries,
		"objectKey":            d.ObjectKey,
		"exportedAt":           d.ExportedAt,
		"previousDigest":       d.PreviousDigest,
		"preCutoverThroughSeq": d.PreCutoverThroughSeq,
		"formatVersion":        d.FormatVersion,
	}
}

// Verification is the recipe. It exists so that "independently verifiable"
// means what it says: everything below is checkable with a sha256
// implementation and a JSON parser.
type Verification struct {
	Algorithm       string   `json:"algorithm"`
	Encoding        string   `json:"encoding"`
	Digest          string   `json:"digest"`
	EntryHashFields []string `json:"entryHashFields"`
	// EntryHashFieldsByVersion is the field order per hashing contract, keyed by
	// the entry's hashVersion as a decimal string. Contracts EXTEND — each list
	// is the one before it plus fields at the end — so a segment may carry
	// entries of several versions and each still verifies as it was written.
	// EntryHashFields remains the list for the oldest recomputable contract.
	EntryHashFieldsByVersion map[string][]string `json:"entryHashFieldsByVersion"`
	EntryHashSeparator       string              `json:"entryHashSeparator"`
	OccurredAtFormat         string              `json:"occurredAtFormat"`
	DetailsForm              string              `json:"detailsForm"`
	GenesisPrevHash          string              `json:"genesisPrevHash"`
	CurrentHashVersion       int16               `json:"currentHashVersion"`
	Procedure                []string            `json:"procedure"`
}

// Entry is one audit entry in the form the hash covers.
type Entry struct {
	Seq          int64  `json:"seq"`
	EventID      string `json:"eventId"`
	TenantID     string `json:"tenantId"`
	ActorID      string `json:"actorId"`
	Action       string `json:"action"`
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	OccurredAt   string `json:"occurredAt"`
	RequestID    string `json:"requestId"`
	// CredentialKind and CredentialID name the credential the actor held — a
	// session or an API token — which is how a reader of the archive can tell
	// two concurrent sessions of one account apart. Empty (and omitted from the
	// file) for an entry with no credential and for every entry hashed under a
	// version below 3, which did not cover them.
	CredentialKind string          `json:"credentialKind,omitempty"`
	CredentialID   string          `json:"credentialId,omitempty"`
	Details        json.RawMessage `json:"details"`
	PrevHash       string          `json:"prevHash"`
	Hash           string          `json:"hash"`
	// HashVersion is the hashing contract the entry was written under. Entries
	// below audit.CurrentHashVersion predate the 2026-09-12 cut-over and hashed
	// an instant finer than the column stores, so their hash cannot be
	// recomputed from any stored form — they are link-checked only, and only
	// inside the one run at the beginning of the TENANT'S chain that the export
	// record bounds (see VerifyDocument and VerifySeries).
	HashVersion int16 `json:"hashVersion"`
}

// auditEntry converts back to the platform/audit envelope so the one true
// ComputeHash — not a copy of it — decides whether an entry verifies.
func (e Entry) auditEntry() (audit.Entry, error) {
	at, err := time.Parse(OccurredAtLayout, e.OccurredAt)
	if err != nil {
		return audit.Entry{}, fmt.Errorf("seq %d: occurredAt %q is not %s: %w", e.Seq, e.OccurredAt, OccurredAtLayout, err)
	}
	return audit.Entry{
		EventID:        e.EventID,
		TenantID:       e.TenantID,
		Seq:            e.Seq,
		ActorID:        e.ActorID,
		Action:         e.Action,
		ResourceType:   e.ResourceType,
		ResourceID:     e.ResourceID,
		OccurredAt:     at,
		RequestID:      e.RequestID,
		CredentialID:   e.CredentialID,
		CredentialKind: e.CredentialKind,
		Details:        e.Details,
		PrevHash:       e.PrevHash,
		Hash:           e.Hash,
		HashVersion:    e.HashVersion,
	}, nil
}

// FromAuditEntry renders a stored envelope into its segment form.
func FromAuditEntry(e audit.Entry) Entry {
	return Entry{
		Seq:            e.Seq,
		EventID:        e.EventID,
		TenantID:       e.TenantID,
		ActorID:        e.ActorID,
		Action:         e.Action,
		ResourceType:   e.ResourceType,
		ResourceID:     e.ResourceID,
		OccurredAt:     FormatInstant(e.OccurredAt),
		RequestID:      e.RequestID,
		CredentialKind: e.CredentialKind,
		CredentialID:   e.CredentialID,
		Details:        canonicalDetails(e.Details),
		PrevHash:       e.PrevHash,
		Hash:           e.Hash,
		HashVersion:    e.HashVersion,
	}
}

// FormatInstant renders an instant the way a segment carries it: UTC,
// truncated to the resolution the column stores, at a fixed six-digit fraction.
func FormatInstant(t time.Time) string {
	return t.UTC().Truncate(audit.ChainResolution).Format(OccurredAtLayout)
}

// BuildInput is one segment's worth of facts.
type BuildInput struct {
	SegmentID     string
	SegmentSeq    int64
	TenantID      string
	ExportedAt    time.Time
	StartPrevHash string
	Entries       []audit.Entry
	Previous      *Previous
}

// Built is a segment ready to store.
type Built struct {
	// Bytes is the object's content.
	Bytes []byte
	// Digest is the hex sha256 of the segment document (no prefix) — the value
	// the ledger row carries.
	Digest string
	// Document is the parsed body, for callers that want the range/chain facts
	// without re-reading their own output.
	Document Document
}

// Build renders a segment. It is DETERMINISTIC: the same entries, segment id
// and ExportedAt produce byte-identical output, which is what lets a retry
// after a crash re-upload the same object under the same key instead of
// inventing a second, differently-shaped record of the same range.
//
// It refuses to build anything it could not later verify: the range must be
// contiguous, single-tenant, and linked to StartPrevHash.
func Build(in BuildInput) (Built, error) {
	if len(in.Entries) == 0 {
		return Built{}, errors.New("worm: a segment needs at least one entry")
	}
	if in.TenantID == "" {
		return Built{}, errors.New("worm: a segment needs a tenant")
	}
	if in.StartPrevHash == "" {
		return Built{}, errors.New("worm: a segment needs the chain state it starts from")
	}

	entries := make([]Entry, 0, len(in.Entries))
	for _, e := range in.Entries {
		if e.TenantID != in.TenantID {
			return Built{}, fmt.Errorf("worm: entry seq %d belongs to tenant %s, not %s", e.Seq, e.TenantID, in.TenantID)
		}
		entries = append(entries, FromAuditEntry(e))
	}

	first, last := entries[0], entries[len(entries)-1]
	doc := Document{
		Format:        Format,
		FormatVersion: FormatVersion,
		SegmentID:     in.SegmentID,
		SegmentSeq:    in.SegmentSeq,
		TenantID:      in.TenantID,
		ExportedAt:    FormatInstant(in.ExportedAt),
		Range: Range{
			FromSeq:         first.Seq,
			ToSeq:           last.Seq,
			Entries:         len(entries),
			FirstOccurredAt: first.OccurredAt,
			LastOccurredAt:  last.OccurredAt,
		},
		Chain: Chain{
			StartPrevHash: in.StartPrevHash,
			EndHash:       last.Hash,
			Genesis:       in.StartPrevHash == audit.GenesisHash,
		},
		Previous:     in.Previous,
		Verification: recipe(),
		Entries:      entries,
	}

	// Verify what is about to be written rather than trusting that the rows
	// that came out of the database are the rows that went in. This is the
	// scheduled run of the verifier ADR-0008 said nothing ever performed: a
	// broken chain fails the export instead of being copied into the evidence
	// store as though it were evidence.
	if _, err := VerifyDocument(doc); err != nil {
		return Built{}, fmt.Errorf("worm: refusing to export an unverifiable segment: %w", err)
	}

	body, err := json.Marshal(doc)
	if err != nil {
		return Built{}, fmt.Errorf("worm: encode segment: %w", err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	out, err := json.Marshal(File{Segment: body, Digest: digestPrefix + digest})
	if err != nil {
		return Built{}, fmt.Errorf("worm: encode file: %w", err)
	}
	return Built{Bytes: out, Digest: digest, Document: doc}, nil
}

// Report is what one verification of a segment can state.
type Report struct {
	TenantID   string
	SegmentSeq int64
	FromSeq    int64
	ToSeq      int64
	Entries    int
	// PreCutoverEntries were written before the hash-chain cut-over and could
	// only be link-checked (see Entry.HashVersion).
	PreCutoverEntries int
	// PreCutoverThroughSeq is the last seq of that run, 0 when there is none.
	PreCutoverThroughSeq int64
	// PreCutoverProven says whether this file ALONE can show that its
	// pre-cut-over run is the tenant's genuine historical prefix — which it can
	// only do when it is the tenant's genesis segment. It is true when there is
	// nothing to prove (no pre-cut-over entries at all). When it is false, the
	// run is not yet evidence of anything: the earlier segments have to be
	// supplied, and VerifySeries has to settle it.
	PreCutoverProven bool
	// HeaderSealed says whether this document carried the `audit.exported`
	// entry that covers its own header. When it is false the header was checked
	// against nothing — the record is in a later segment of the series (a
	// capped range), or has not been exported yet.
	HeaderSealed bool
	// HeaderRecordPreSeal says the record covering this segment WAS found and
	// predates the sealed header: it states the range and nothing else, so the
	// range is still held against the chain while the id, the export instant,
	// the predecessor digest and the pre-cut-over bound are the file's own
	// words. It is not tampering and it is not a capped range — see
	// RecordDetails — and the two are distinct, because a capped range's record
	// arrives later while this one has already arrived and will never say more.
	HeaderRecordPreSeal bool
	// FirstVerifiedSeq / LastVerifiedSeq bound what was actually recomputed.
	FirstVerifiedSeq int64
	LastVerifiedSeq  int64
	Genesis          bool
	DigestOK         bool
	Digest           string
	// Document is the parsed body, so a caller checking a series does not have
	// to decode the file a second time.
	Document Document
}

func (r Report) String() string {
	head := fmt.Sprintf("segment %d of tenant %s: seq %d..%d (%d entries)",
		r.SegmentSeq, r.TenantID, r.FromSeq, r.ToSeq, r.Entries)
	switch {
	case r.PreCutoverEntries == 0:
		return head + fmt.Sprintf(", hash-verified seq %d..%d", r.FirstVerifiedSeq, r.LastVerifiedSeq)
	case r.FirstVerifiedSeq == 0:
		return head + fmt.Sprintf(", ALL %d entries pre-cut-over through seq %d (links intact, none recomputable)",
			r.PreCutoverEntries, r.PreCutoverThroughSeq)
	default:
		return head + fmt.Sprintf(", %d pre-cut-over through seq %d (links intact, not recomputable), hash-verified seq %d..%d",
			r.PreCutoverEntries, r.PreCutoverThroughSeq, r.FirstVerifiedSeq, r.LastVerifiedSeq)
	}
}

// VerifyFile is the whole offline check of one stored object, and the function
// cmd/verify-audit-segment is a thin shell around: the digest over the exact
// bytes, then the document.
func VerifyFile(data []byte) (Report, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return Report{}, fmt.Errorf("worm: not a segment file: %w", err)
	}
	if len(f.Segment) == 0 {
		return Report{}, errors.New("worm: file carries no segment")
	}
	want, ok := strings.CutPrefix(f.Digest, digestPrefix)
	if !ok {
		return Report{}, fmt.Errorf("worm: digest %q is not %s-prefixed", f.Digest, strings.TrimSuffix(digestPrefix, ":"))
	}
	sum := sha256.Sum256(f.Segment)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return Report{}, fmt.Errorf("worm: digest mismatch: the file says %s, its segment hashes to %s (the file has been altered)", want, got)
	}

	var doc Document
	if err := json.Unmarshal(f.Segment, &doc); err != nil {
		return Report{}, fmt.Errorf("worm: decode segment: %w", err)
	}
	rep, err := VerifyDocument(doc)
	rep.DigestOK = err == nil
	rep.Digest = got
	return rep, err
}

// VerifyDocument checks a segment's internal claims: the format, the range, the
// links from StartPrevHash through every entry to EndHash, the hash of every
// entry written under the current contract, and — through the `audit.exported`
// record the chain carries for it — the segment's own header.
//
// Pre-cut-over entries (hashVersion 1 — audit.IsPreCutover; every later version
// is recomputed under its own envelope) hashed an instant finer than any stored
// form keeps, so they cannot be recomputed at
// all. They are tolerated exactly as platform/audit tolerates them: as one
// contiguous run at the beginning of THE TENANT'S CHAIN, link-checked, and
// reported. One file can only ever prove that much of it: that the run leads
// the file, that the file is the tenant's genesis segment (Report.
// PreCutoverProven), and that the run ends exactly where the sealed export
// record says it does. A file that is NOT the genesis segment cannot settle the
// question at all — its leading entries are in the middle of a tenant's chain —
// so it reports PreCutoverProven false and VerifySeries decides, with the
// earlier segments in hand. Nothing may certify such a file on its own: that is
// what would let an attacker rewrite the head of any recent segment.
func VerifyDocument(doc Document) (Report, error) {
	rep := Report{
		TenantID:   doc.TenantID,
		SegmentSeq: doc.SegmentSeq,
		FromSeq:    doc.Range.FromSeq,
		ToSeq:      doc.Range.ToSeq,
		Entries:    len(doc.Entries),
		Genesis:    doc.Chain.Genesis,
		Document:   doc,
	}

	if doc.Format != Format {
		return rep, fmt.Errorf("worm: %q is not a %s document", doc.Format, Format)
	}
	if doc.FormatVersion != FormatVersion {
		return rep, fmt.Errorf("worm: segment format v%d is not this verifier's v%d (use a reader that knows it; do not half-verify)",
			doc.FormatVersion, FormatVersion)
	}
	if len(doc.Entries) == 0 {
		return rep, errors.New("worm: segment carries no entries")
	}
	if doc.Range.Entries != len(doc.Entries) {
		return rep, fmt.Errorf("worm: segment claims %d entries and carries %d", doc.Range.Entries, len(doc.Entries))
	}
	if doc.Chain.Genesis != (doc.Chain.StartPrevHash == audit.GenesisHash) {
		return rep, fmt.Errorf("worm: segment claims genesis=%t but starts from %s", doc.Chain.Genesis, doc.Chain.StartPrevHash)
	}
	if doc.Chain.Genesis && doc.Range.FromSeq != 1 {
		return rep, fmt.Errorf("worm: segment claims genesis but starts at seq %d", doc.Range.FromSeq)
	}
	if doc.Previous != nil {
		if doc.Previous.ToSeq+1 != doc.Range.FromSeq {
			return rep, fmt.Errorf("worm: segment starts at seq %d but the previous segment ended at %d (a segment is missing)",
				doc.Range.FromSeq, doc.Previous.ToSeq)
		}
		if doc.Previous.EndHash != doc.Chain.StartPrevHash {
			return rep, fmt.Errorf("worm: segment starts from %s but names a previous segment ending at %s",
				doc.Chain.StartPrevHash, doc.Previous.EndHash)
		}
	}

	prev := doc.Chain.StartPrevHash
	for i, e := range doc.Entries {
		wantSeq := doc.Range.FromSeq + int64(i)
		if e.Seq != wantSeq {
			return rep, fmt.Errorf("worm: chain broken at index %d: seq %d, want %d (missing or reordered entry)", i, e.Seq, wantSeq)
		}
		if e.TenantID != doc.TenantID {
			return rep, fmt.Errorf("worm: entry seq %d belongs to tenant %s, not %s", e.Seq, e.TenantID, doc.TenantID)
		}
		if e.PrevHash != prev {
			return rep, fmt.Errorf("worm: chain broken at seq %d: prev_hash mismatch", e.Seq)
		}

		switch {
		case e.HashVersion > audit.CurrentHashVersion:
			return rep, fmt.Errorf("worm: entry seq %d was hashed under v%d, newer than this verifier's v%d (upgrade the verifier)",
				e.Seq, e.HashVersion, audit.CurrentHashVersion)
		case audit.IsPreCutover(e.HashVersion):
			if rep.FirstVerifiedSeq != 0 {
				return rep, fmt.Errorf("worm: chain broken at seq %d: pre-cut-over entry (hash format v%d) after verifiable entry seq %d",
					e.Seq, e.HashVersion, rep.FirstVerifiedSeq)
			}
			rep.PreCutoverEntries++
		default:
			ae, err := e.auditEntry()
			if err != nil {
				return rep, fmt.Errorf("worm: %w", err)
			}
			if got := audit.ComputeHash(&ae); got != e.Hash {
				return rep, fmt.Errorf("worm: chain broken at seq %d: hash mismatch (entry modified)", e.Seq)
			}
			if rep.FirstVerifiedSeq == 0 {
				rep.FirstVerifiedSeq = e.Seq
			}
			rep.LastVerifiedSeq = e.Seq
		}
		prev = e.Hash
	}

	first, last := doc.Entries[0], doc.Entries[len(doc.Entries)-1]
	if doc.Range.ToSeq != last.Seq {
		return rep, fmt.Errorf("worm: segment claims it ends at seq %d and ends at %d", doc.Range.ToSeq, last.Seq)
	}
	if doc.Chain.EndHash != last.Hash {
		return rep, fmt.Errorf("worm: segment claims end hash %s, its last entry hashes to %s", doc.Chain.EndHash, last.Hash)
	}
	// The window the segment advertises is a summary of entries the hashes
	// already cover, so it must be recomputed rather than believed: it is what
	// an auditor reads to decide whether a file is the period they asked for.
	if doc.Range.FirstOccurredAt != first.OccurredAt || doc.Range.LastOccurredAt != last.OccurredAt {
		return rep, fmt.Errorf("worm: segment claims it covers %s..%s and its entries run %s..%s (the window was edited)",
			doc.Range.FirstOccurredAt, doc.Range.LastOccurredAt, first.OccurredAt, last.OccurredAt)
	}
	if err := position(doc); err != nil {
		return rep, err
	}

	if rep.PreCutoverEntries > 0 {
		rep.PreCutoverThroughSeq = doc.Range.FromSeq + int64(rep.PreCutoverEntries) - 1
		// Only a genesis segment can show, on its own, that its leading run is
		// the beginning of the TENANT rather than the beginning of a file.
		rep.PreCutoverProven = doc.Chain.Genesis
	} else {
		rep.PreCutoverProven = true
	}

	// Finally the header, against the one statement of it the chain covers.
	records, err := exportRecords(doc)
	if err != nil {
		return rep, err
	}
	if rec, ok := records[doc.SegmentSeq]; ok {
		sealed, err := checkHeader(rep, rec)
		if err != nil {
			return rep, err
		}
		rep.HeaderSealed = sealed
		rep.HeaderRecordPreSeal = !sealed
	}
	return rep, nil
}

// position checks what a segment says about WHERE IT SITS in its tenant's
// chain. The writer makes five statements that are one statement: genesis, the
// ordinal, the first seq, the starting chain state, and the back-pointer. A
// file whose own account of its position is inconsistent is not evidence of a
// position, and — since the pre-cut-over exemption belongs to exactly one
// position, the beginning — that matters more here than it looks.
func position(doc Document) error {
	if doc.SegmentSeq < 1 {
		return fmt.Errorf("worm: segment ordinal %d is not a position in a series", doc.SegmentSeq)
	}
	if doc.Chain.Genesis {
		if doc.SegmentSeq != 1 {
			return fmt.Errorf("worm: segment claims it begins the tenant's chain and calls itself segment %d of the series", doc.SegmentSeq)
		}
		if doc.Previous != nil {
			return errors.New("worm: segment claims it begins the tenant's chain and names a segment before it")
		}
		return nil
	}
	switch {
	case doc.Previous == nil:
		// Without a back-pointer a mid-chain file names nothing, and a pile of
		// such files can be put in any order at all.
		return fmt.Errorf("worm: segment %d starts mid-chain (seq %d) and names no segment before it, so nothing ties it to a position in the series",
			doc.SegmentSeq, doc.Range.FromSeq)
	case doc.SegmentSeq < 2:
		return fmt.Errorf("worm: segment starts mid-chain (seq %d) and calls itself segment %d of the series",
			doc.Range.FromSeq, doc.SegmentSeq)
	case doc.Previous.SegmentSeq+1 != doc.SegmentSeq:
		return fmt.Errorf("worm: segment %d names segment %d before it (the ordinals do not meet)",
			doc.SegmentSeq, doc.Previous.SegmentSeq)
	}
	return nil
}

// sealedField names one of the four header fields a record restates to bind
// them. Which of them a record STATES has to be read from the bytes, because
// the product has written two record shapes: exports before 2026-09-13 stated
// the range and nothing else, and json.Unmarshal cannot tell a field that was
// never written from one written as "" or 0. Reading the absent fields as empty
// is how this reader used to call a genuine older archive an edit.
type sealedField uint8

const (
	sealedSegmentID sealedField = 1 << iota
	sealedExportedAt
	sealedPreviousDigest
	sealedPreCutoverThrough
)

// sealedAll is the whole sealed header: a record states all four or none of
// them, because a record is written whole.
const sealedAll = sealedSegmentID | sealedExportedAt | sealedPreviousDigest | sealedPreCutoverThrough

// sealedFieldNames is in the order the fields are written, so a message about a
// half-written record reads the way the record does.
var sealedFieldNames = []struct {
	bit  sealedField
	name string
}{
	{sealedSegmentID, "segmentId"},
	{sealedExportedAt, "exportedAt"},
	{sealedPreviousDigest, "previousDigest"},
	{sealedPreCutoverThrough, "preCutoverThroughSeq"},
}

func (s sealedField) names() []string {
	var out []string
	for _, f := range sealedFieldNames {
		if s&f.bit != 0 {
			out = append(out, f.name)
		}
	}
	return out
}

// exportRecord is an export record together with which of the sealed-header
// fields it actually states. It stays comparable on purpose: two records
// describing one segment must be identical down to their shape.
type exportRecord struct {
	RecordDetails
	stated sealedField
}

// exportRecords collects the `audit.exported` entries a document carries, by
// the segment ordinal each one describes. These are ordinary chain entries, so
// their details are hash-covered: what they say about a segment's header cannot
// be changed without breaking the chain — and neither can WHICH fields they
// say it in, which is what makes the older shape safe to recognise rather than
// merely tolerate.
func exportRecords(doc Document) (map[int64]exportRecord, error) {
	var out map[int64]exportRecord
	for _, e := range doc.Entries {
		if e.Action != AuditAction || e.ResourceType != AuditResource {
			continue
		}
		var rec exportRecord
		if err := json.Unmarshal(e.Details, &rec.RecordDetails); err != nil {
			return nil, fmt.Errorf("worm: the export record at seq %d is not readable: %w", e.Seq, err)
		}
		var written map[string]json.RawMessage
		if err := json.Unmarshal(e.Details, &written); err != nil {
			return nil, fmt.Errorf("worm: the export record at seq %d is not readable: %w", e.Seq, err)
		}
		for _, f := range sealedFieldNames {
			if _, ok := written[f.name]; ok {
				rec.stated |= f.bit
			}
		}
		if out == nil {
			out = make(map[int64]exportRecord, 1)
		}
		if existing, ok := out[rec.SegmentSeq]; ok && existing != rec {
			return nil, fmt.Errorf("worm: two different export records describe segment %d", rec.SegmentSeq)
		}
		out[rec.SegmentSeq] = rec
	}
	return out, nil
}

// checkHeader holds a segment's own words about itself against the sealed
// record of its export. Everything compared here is covered by no entry hash,
// which is exactly why it has to be compared with something that is.
//
// It reports whether the header is SEALED — whether the record bound all of it.
// The range is bound by every record the product has ever written and is always
// checked. The other four fields were added on 2026-09-13; a record written
// before that states none of them, and then the range is all this can hold the
// file to. That is a real, permanent limit on such an archive, not a fault in
// it: an export record lives in a write-once segment, so it will never say more
// than it said. Saying so is the whole point — reading the absent fields as ""
// and 0 made this reader call every pre-2026-09-13 archive an edit.
//
// A record that states SOME of the four is neither shape, and is refused: the
// fields are hash-covered, so a half-written record is an edit that also rebuilt
// the entry hash.
func checkHeader(rep Report, rec exportRecord) (sealed bool, err error) {
	doc := rep.Document
	// The range, which every record shape states.
	for _, c := range []struct {
		what      string
		got, want any
	}{
		{"the seq it starts at", doc.Range.FromSeq, rec.FromSeq},
		{"the seq it ends at", doc.Range.ToSeq, rec.ToSeq},
		{"how many entries it covers", int64(doc.Range.Entries), rec.Entries},
	} {
		if c.got != c.want {
			return false, headerDisagrees(doc.SegmentSeq, c.what, c.got, c.want)
		}
	}

	switch rec.stated {
	case 0:
		// A record from before the header was sealed. The range above is held
		// against the chain; nothing here can bind the rest.
		return false, nil
	case sealedAll:
	default:
		return false, fmt.Errorf("worm: segment %d has an export record that states %s and none of the rest of the sealed header — a record is written whole, so this one was edited",
			doc.SegmentSeq, strings.Join(rec.stated.names(), ", "))
	}

	previousDigest := ""
	if doc.Previous != nil {
		previousDigest = doc.Previous.Digest
	}
	for _, c := range []struct {
		what      string
		got, want any
	}{
		{"its id", doc.SegmentID, rec.SegmentID},
		{"when it was exported", doc.ExportedAt, rec.ExportedAt},
		{"the digest of the segment before it", previousDigest, rec.PreviousDigest},
		{"how far its pre-cut-over prefix reaches", rep.PreCutoverThroughSeq, rec.PreCutoverThroughSeq},
	} {
		if c.got != c.want {
			return false, headerDisagrees(doc.SegmentSeq, c.what, c.got, c.want)
		}
	}
	return true, nil
}

func headerDisagrees(segmentSeq int64, what string, got, want any) error {
	return fmt.Errorf("worm: segment %d disagrees with the export record the chain carries for it — %s: the file says %v, the record says %v (the header was edited)",
		segmentSeq, what, got, want)
}

// SeriesReport is what a whole series of files can state, and is the only place
// several of these claims can be made at all.
type SeriesReport struct {
	TenantID string
	Segments int
	FromSeq  int64
	ToSeq    int64
	Entries  int
	Genesis  bool
	// PreCutoverThroughSeq is the last seq of the tenant's historical prefix —
	// the one contiguous run of unrecomputable entries at the very beginning of
	// this chain. 0 when the series carries none.
	PreCutoverThroughSeq int64
	// UnsealedFrom is the ordinal of the first segment whose header is covered
	// by no export record in this series, 0 when every header is sealed. It can
	// only be a trailing run: a capped range's record is carried by a later
	// segment, and for the newest segment that segment may not exist yet.
	UnsealedFrom int64
	// PreSealHeaders are the ordinals of segments whose export record was found
	// and predates the sealed header (see Report.HeaderRecordPreSeal). Their
	// ranges are held against the chain; their ids, export instants,
	// predecessor digests and pre-cut-over bounds are not bound by anything and
	// never will be. They are a LEADING run of any archive old enough to have
	// one, and unlike UnsealedFrom no later export closes them.
	PreSealHeaders []int64
}

// VerifySeries proves that a pile of files is a CHAIN of files.
//
// Each report must be one VerifyFile returned — not VerifyDocument — because a
// segment names the DIGEST of the file before it, and a document parsed out of
// its file no longer knows what those bytes hashed to. Checking that
// back-pointer is the difference between "these segments are individually
// consistent" and "this is the series that was written": without it, an earlier
// segment can be re-sealed, or files reordered or replayed, and every one of
// them still verifies.
//
// Three things can only be decided here, with the whole series in hand:
//
//   - the JOIN between consecutive files — ordinals, sequence numbers, the end
//     hash, and the back-pointer's identification of the actual file before it;
//   - whether a pre-cut-over run is the tenant's genuine historical PREFIX,
//     which requires the series to start at genesis and every segment before
//     the run's end to be entirely pre-cut-over;
//   - the header of a segment whose own export record landed in a later file.
//
// A series beginning at genesis and ending at a tenant's current chain head is
// the whole trail, proven without the database.
func VerifySeries(reports []Report) (SeriesReport, error) {
	var series SeriesReport
	if len(reports) == 0 {
		return series, errors.New("worm: no segments to check")
	}
	for _, r := range reports {
		if r.Digest == "" {
			return series, fmt.Errorf("worm: segment %d was checked as a document rather than as a file; a series is checked with the digests VerifyFile computes, because every segment names the digest of the one before it",
				r.SegmentSeq)
		}
	}

	first, last := reports[0], reports[len(reports)-1]
	series = SeriesReport{
		TenantID: first.TenantID,
		Segments: len(reports),
		FromSeq:  first.FromSeq,
		ToSeq:    last.ToSeq,
		Genesis:  first.Genesis,
	}
	for _, r := range reports {
		series.Entries += r.Entries
	}

	for i := 1; i < len(reports); i++ {
		prev, cur := reports[i-1], reports[i]
		switch {
		case cur.TenantID != prev.TenantID:
			return series, fmt.Errorf("worm: segment %d belongs to tenant %s, the one before it to %s", i, cur.TenantID, prev.TenantID)
		case cur.SegmentSeq != prev.SegmentSeq+1:
			return series, fmt.Errorf("worm: segment ordinal jumps from %d to %d (a segment is missing)", prev.SegmentSeq, cur.SegmentSeq)
		case cur.FromSeq != prev.ToSeq+1:
			return series, fmt.Errorf("worm: segment %d starts at seq %d, the one before it ended at %d",
				cur.SegmentSeq, cur.FromSeq, prev.ToSeq)
		case cur.Document.Chain.StartPrevHash != prev.Document.Chain.EndHash:
			return series, fmt.Errorf("worm: segment %d does not link to the one before it", cur.SegmentSeq)
		}
		if err := meets(prev, cur); err != nil {
			return series, err
		}
	}

	if err := anchorPreCutover(reports, &series); err != nil {
		return series, err
	}
	if err := sealHeaders(reports, &series); err != nil {
		return series, err
	}
	return series, nil
}

// meets checks that a segment's back-pointer names the file actually in front
// of it, and not merely A file with the right end hash. The end hash alone is
// reproducible by anyone re-sealing an earlier segment; the digest is not.
func meets(prev, cur Report) error {
	p := cur.Document.Previous
	switch {
	case p == nil:
		return fmt.Errorf("worm: segment %d names no segment before it", cur.SegmentSeq)
	case p.SegmentSeq != prev.SegmentSeq:
		return fmt.Errorf("worm: segment %d names segment %d before it, but the file before it is segment %d",
			cur.SegmentSeq, p.SegmentSeq, prev.SegmentSeq)
	case p.SegmentID != prev.Document.SegmentID:
		return fmt.Errorf("worm: segment %d names segment id %s before it, but the file before it is %s (a different segment has been put in its place)",
			cur.SegmentSeq, p.SegmentID, prev.Document.SegmentID)
	case p.ToSeq != prev.ToSeq:
		return fmt.Errorf("worm: segment %d says the segment before it ended at seq %d, and that file ends at %d",
			cur.SegmentSeq, p.ToSeq, prev.ToSeq)
	case p.EndHash != prev.Document.Chain.EndHash:
		return fmt.Errorf("worm: segment %d says the segment before it ended at hash %s, and that file ends at %s",
			cur.SegmentSeq, p.EndHash, prev.Document.Chain.EndHash)
	case p.Digest != digestPrefix+prev.Digest:
		return fmt.Errorf("worm: segment %d names a segment before it with digest %s, and the file before it hashes to %s%s (the earlier file was re-sealed, replaced or replayed)",
			cur.SegmentSeq, p.Digest, digestPrefix, prev.Digest)
	}
	return nil
}

// anchorPreCutover ties the cut-over exemption to the beginning of the TENANT.
//
// The exemption exists for one thing: rows that were already in the ledger when
// the hash format changed, which are necessarily a contiguous run starting at
// seq 1. Applied per file it is worthless — every file has a top, so an
// attacker rewrites the leading entries of any recent segment, marks them
// pre-cut-over, and the file reads as "the earliest entries, link-checked".
// Here the run may continue into a segment only while every segment before it
// was entirely pre-cut-over, and only when the series starts at genesis.
func anchorPreCutover(reports []Report, series *SeriesReport) error {
	prefixOpen := reports[0].Genesis
	for _, r := range reports {
		if r.PreCutoverEntries > 0 {
			if !prefixOpen {
				return fmt.Errorf("worm: segment %d carries %d entries marked pre-cut-over from seq %d, but this tenant's chain is already hash-verified before that point%s: the exemption covers ONE contiguous run at the very beginning of a tenant's chain, never the leading entries of a later file",
					r.SegmentSeq, r.PreCutoverEntries, r.FromSeq, missingPrefix(reports[0]))
			}
			series.PreCutoverThroughSeq = r.PreCutoverThroughSeq
		}
		if r.FirstVerifiedSeq != 0 {
			prefixOpen = false
		}
	}
	return nil
}

// missingPrefix says which of the two ways a prefix fails to be one this is,
// because the answer decides what the reader should do next.
func missingPrefix(first Report) string {
	if !first.Genesis {
		return fmt.Sprintf(" (this series starts at seq %d, not at the tenant's genesis, so no run in it can be shown to begin the chain — supply the earlier segments)", first.FromSeq)
	}
	return ""
}

// sealHeaders holds every segment's header against the export record the chain
// carries for it, wherever in the series that record landed. A segment whose
// range was capped does not contain its own record; a later segment does.
func sealHeaders(reports []Report, series *SeriesReport) error {
	records := make(map[int64]exportRecord, len(reports))
	for _, r := range reports {
		found, err := exportRecords(r.Document)
		if err != nil {
			return err
		}
		for seq, rec := range found {
			if existing, ok := records[seq]; ok && existing != rec {
				return fmt.Errorf("worm: two different export records describe segment %d", seq)
			}
			records[seq] = rec
		}
	}
	for _, r := range reports {
		rec, ok := records[r.SegmentSeq]
		if !ok {
			if series.UnsealedFrom == 0 {
				series.UnsealedFrom = r.SegmentSeq
			}
			continue
		}
		if series.UnsealedFrom != 0 {
			return fmt.Errorf("worm: the series carries the export record for segment %d but not the one for segment %d, whose header — when it was exported, the window it claims — is therefore covered by nothing (the record was removed)",
				r.SegmentSeq, series.UnsealedFrom)
		}
		sealed, err := checkHeader(r, rec)
		if err != nil {
			return err
		}
		if !sealed {
			series.PreSealHeaders = append(series.PreSealHeaders, r.SegmentSeq)
		}
	}
	return nil
}

// recipe is the Verification block every segment carries.
func recipe() Verification {
	return Verification{
		Algorithm:       "sha256",
		Encoding:        "hex",
		Digest:          `sha256 of the exact bytes of the "segment" member of this file, as written`,
		EntryHashFields: append([]string(nil), entryHashFields...),
		EntryHashFieldsByVersion: map[string][]string{
			strconv.FormatInt(int64(audit.HashVersionMicrosecond), 10): entryHashFieldsFor(audit.HashVersionMicrosecond),
			strconv.FormatInt(int64(audit.HashVersionCredential), 10):  entryHashFieldsFor(audit.HashVersionCredential),
		},
		EntryHashSeparator: "\n",
		OccurredAtFormat:   "RFC 3339 in UTC with exactly six fractional digits",
		DetailsForm:        "compact JSON, object keys sorted lexicographically at every level, as carried in this file",
		GenesisPrevHash:    audit.GenesisHash,
		CurrentHashVersion: audit.CurrentHashVersion,
		Procedure: []string{
			`digest: sha256 of the raw bytes of the "segment" member equals the file's "digest" (minus the "sha256:" prefix)`,
			`entry hash: sha256 of the entry's fields for ITS OWN hashVersion — entryHashFieldsByVersion[hashVersion], and the list of the HIGHEST version present there when the entry states no version (hashVersion 0, "not recorded": an omitted marker must never hash less than the writer did) — joined by "\n", hex-encoded, equals the entry's "hash"; seq is its decimal form. Versions extend: each list is the previous one plus fields at the end, and a field absent from the entry hashes as the empty string, so one file may hold entries of several versions and each verifies as it was written`,
			`links: the first entry's prevHash equals chain.startPrevHash; every later entry's prevHash equals the hash of the entry before it; the last entry's hash equals chain.endHash`,
			`range: entry seq runs from range.fromSeq to range.toSeq with no gap, range.entries is the count, and range.firstOccurredAt/lastOccurredAt are the occurredAt of the first and last entries`,
			`header: segmentId, exportedAt, the range and the digest of the segment before it are the file's own words about itself and no entry hash covers them, so they are restated in the details of the "audit.exported" entry whose segmentSeq is this segment's — an entry the chain DOES cover. The two must agree. That entry is normally the last of the range; when a range was capped it is carried by a later segment of the series`,
			`series: the next segment's chain.startPrevHash equals this one's chain.endHash, its range.fromSeq equals range.toSeq + 1, its segmentSeq equals this one's + 1, and its previousSegment names THIS FILE — same segmentId, same toSeq, same endHash, and a digest equal to this file's digest. Checking that digest is what stops an earlier segment being re-sealed, replaced or replayed`,
			`pre-cut-over: an entry whose hashVersion is 1 hashed a finer instant than any stored form keeps and can only be link-checked (hashVersion 0 means "not recorded" and must be recomputed like any other; every version above 1 is recomputed under its own field list, so a later contract never excuses an earlier entry from verification). It is legitimate ONLY inside the one contiguous run at the very beginning of the TENANT'S chain: the run leads its segment, it ends exactly at the preCutoverThroughSeq the export record states, and either this segment is the genesis segment or every earlier segment of the series is entirely pre-cut-over. The leading entries of a later file are not a cut-over, they are an attempt to exempt entries from recomputation — a file that does not start at genesis cannot settle this alone`,
		},
	}
}

// canonicalDetails puts a details payload in the one form both the writer and
// every verifier hash: compact JSON with object keys sorted at every level,
// which is what encoding/json produces from a map. It mirrors what
// audit.ComputeHash does internally, so what the segment CARRIES is already the
// hashed form and a reader never has to guess.
func canonicalDetails(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw // non-object payloads are carried, and hashed, as they are
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// hashInput renders the documented recipe's input for an entry — the fields
// its OWN hashVersion lists, in order. It exists for the test that checks the
// published recipe against audit.ComputeHash; nothing in production hashes
// through it.
func hashInput(e Entry) string {
	values := map[string]string{
		"prevHash":       e.PrevHash,
		"eventId":        e.EventID,
		"tenantId":       e.TenantID,
		"seq":            strconv.FormatInt(e.Seq, 10),
		"actorId":        e.ActorID,
		"action":         e.Action,
		"resourceType":   e.ResourceType,
		"resourceId":     e.ResourceID,
		"occurredAt":     e.OccurredAt,
		"requestId":      e.RequestID,
		"details":        string(e.Details),
		"credentialKind": e.CredentialKind,
		"credentialId":   e.CredentialID,
	}
	names := entryHashFieldsFor(e.HashVersion)
	fields := make([]string, 0, len(names))
	for _, name := range names {
		fields = append(fields, values[name])
	}
	return strings.Join(fields, "\n")
}
