package worm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// base is an instant with nanoseconds the audit_log column cannot store. Every
// entry below is stamped from it, so the tests exercise the one resolution rule
// the chain's verifiability rests on rather than whatever the host clock does.
var base = time.Date(2026, 9, 13, 8, 30, 0, 123456789, time.UTC)

// chain builds n linked entries for a tenant, starting from prev, with real
// hashes — the same way platform/audit writes them.
func chain(t *testing.T, tenantID, prev string, firstSeq int64, n int) []audit.Entry {
	t.Helper()
	entries := make([]audit.Entry, 0, n)
	for i := 0; i < n; i++ {
		e := audit.Entry{
			EventID:      uuid.NewString(),
			TenantID:     tenantID,
			Seq:          firstSeq + int64(i),
			ActorID:      uuid.NewString(),
			Action:       "entity.updated",
			ResourceType: "entity",
			ResourceID:   uuid.NewString(),
			// Truncated exactly as the recorder truncates: the hashed instant
			// must be an instant the column can store.
			OccurredAt:  base.Add(time.Duration(i) * time.Second).Truncate(audit.ChainResolution),
			RequestID:   "req-" + uuid.NewString(),
			Details:     json.RawMessage(`{"fields":{"name":{"from":"set","to":"set"}},"country":"NL"}`),
			PrevHash:    prev,
			HashVersion: audit.CurrentHashVersion,
		}
		e.Hash = audit.ComputeHash(&e)
		prev = e.Hash
		entries = append(entries, e)
	}
	return entries
}

func build(t *testing.T, tenantID string, entries []audit.Entry, prev *Previous, startPrev string) Built {
	t.Helper()
	built, err := Build(BuildInput{
		SegmentID:     uuid.NewString(),
		SegmentSeq:    1,
		TenantID:      tenantID,
		ExportedAt:    base.Add(time.Hour),
		StartPrevHash: startPrev,
		Entries:       entries,
		Previous:      prev,
	})
	require.NoError(t, err)
	return built
}

// objectKey is where a segment of this range would be stored.
func objectKey(tenantID string, from, to int64) string {
	return fmt.Sprintf("tenants/%s/audit-segments/seg-%012d-%012d.json", tenantID, from, to)
}

// sealed builds a segment the way a CUT does: the range ends with the
// `audit.exported` entry that restates the document's header, so the header is
// covered by the chain rather than only by the file that asserts it. Everything
// the exporter states there, this states — including how far the range's
// pre-cut-over prefix reaches.
//
// A segment built without this (see build) is what a range capped by
// MaxEntriesPerSegment looks like: its record is carried by a later file.
func sealed(t *testing.T, in BuildInput, preCutoverThrough int64) Built {
	t.Helper()
	last := in.Entries[len(in.Entries)-1]
	record := audit.Entry{
		EventID:      uuid.NewString(),
		TenantID:     in.TenantID,
		Seq:          last.Seq + 1,
		ActorID:      SystemActorID,
		Action:       AuditAction,
		ResourceType: AuditResource,
		ResourceID:   in.TenantID,
		OccurredAt:   in.ExportedAt.UTC().Truncate(audit.ChainResolution),
		PrevHash:     last.Hash,
		HashVersion:  audit.CurrentHashVersion,
	}
	previousDigest := ""
	if in.Previous != nil {
		previousDigest = in.Previous.Digest
	}
	from := in.Entries[0].Seq
	details, err := json.Marshal(RecordDetails{
		SegmentSeq:           in.SegmentSeq,
		SegmentID:            in.SegmentID,
		FromSeq:              from,
		ToSeq:                record.Seq,
		Entries:              record.Seq - from + 1,
		ObjectKey:            objectKey(in.TenantID, from, record.Seq),
		ExportedAt:           FormatInstant(in.ExportedAt),
		PreviousDigest:       previousDigest,
		PreCutoverThroughSeq: preCutoverThrough,
		FormatVersion:        FormatVersion,
	}.Map())
	require.NoError(t, err)
	record.Details = details
	record.Hash = audit.ComputeHash(&record)

	in.Entries = append(append([]audit.Entry(nil), in.Entries...), record)
	built, err := Build(in)
	require.NoError(t, err)
	return built
}

// pointsAt is the back-pointer a segment carries to the one before it, exactly
// as previousOf renders it from the ledger row.
func pointsAt(prev Built) *Previous {
	doc := prev.Document
	return &Previous{
		SegmentID:  doc.SegmentID,
		SegmentSeq: doc.SegmentSeq,
		ObjectKey:  objectKey(doc.TenantID, doc.Range.FromSeq, doc.Range.ToSeq),
		ToSeq:      doc.Range.ToSeq,
		EndHash:    doc.Chain.EndHash,
		Digest:     digestPrefix + prev.Digest,
	}
}

// reseal writes a document back out with its digest recomputed: the attacker
// who has read the verification block and knows how the file is checked.
func reseal(t *testing.T, doc Document) []byte {
	t.Helper()
	body, err := json.Marshal(doc)
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	out, err := json.Marshal(File{Segment: body, Digest: digestPrefix + hex.EncodeToString(sum[:])})
	require.NoError(t, err)
	return out
}

// checked is one file as an auditor's reader sees it.
func checked(t *testing.T, data []byte) Report {
	t.Helper()
	report, err := VerifyFile(data)
	require.NoError(t, err)
	return report
}

// copyEntries detaches a document's entries so a test can tamper with one
// without disturbing the original.
func copyEntries(doc Document) Document {
	doc.Entries = append([]Entry(nil), doc.Entries...)
	return doc
}

// TestDocumentedRecipeReproducesTheChainHash is the load-bearing test of the
// whole format: the "verification" block a segment publishes must be enough to
// recompute an entry's hash. It hashes an entry the way the published recipe
// says to — the listed fields, in order, joined by "\n", sha256, hex — and
// requires the result to be what platform/audit actually stored.
//
// If the recipe and the implementation ever part company, an auditor
// reimplementing the check from the file alone would conclude the evidence was
// forged. That failure has to happen here instead.
func TestDocumentedRecipeReproducesTheChainHash(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 3)
	built := build(t, tenantID, entries, nil, audit.GenesisHash)

	require.Equal(t, entryHashFields, built.Document.Verification.EntryHashFields,
		"the published field list must be the one the recipe below uses")
	require.Equal(t, "\n", built.Document.Verification.EntryHashSeparator)
	require.Equal(t, "sha256", built.Document.Verification.Algorithm)

	for _, e := range built.Document.Entries {
		sum := sha256.Sum256([]byte(hashInput(e)))
		require.Equal(t, e.Hash, hex.EncodeToString(sum[:]),
			"the documented recipe must reproduce the stored hash of seq %d", e.Seq)
	}
}

// TestOccurredAtSurvivesTheFileFormat: the segment carries timestamps as text,
// and the hash covers a timestamp. A format that lost or added a digit would
// make every exported entry unverifiable the moment it was written — which is
// exactly the bug the 2026-09-12 cut-over was about, one layer down.
func TestOccurredAtSurvivesTheFileFormat(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 1)
	built := build(t, tenantID, entries, nil, audit.GenesisHash)

	rendered := built.Document.Entries[0]
	require.Len(t, strings.Split(rendered.OccurredAt, ".")[1], len("000000Z"),
		"an instant must be rendered at exactly microsecond resolution")

	back, err := rendered.auditEntry()
	require.NoError(t, err)
	require.True(t, back.OccurredAt.Equal(entries[0].OccurredAt))
	require.Equal(t, entries[0].Hash, audit.ComputeHash(&back))
}

// TestBuildIsDeterministic is what makes a retry after a crash safe: the same
// range, the same stamped instant and the same segment id must produce the same
// bytes, so re-uploading is idempotent rather than a second, differently-shaped
// record of the same evidence.
func TestBuildIsDeterministic(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 5)
	id := uuid.NewString()
	at := base.Add(time.Hour)

	in := BuildInput{
		SegmentID: id, SegmentSeq: 1, TenantID: tenantID, ExportedAt: at,
		StartPrevHash: audit.GenesisHash, Entries: entries,
	}
	first, err := Build(in)
	require.NoError(t, err)
	second, err := Build(in)
	require.NoError(t, err)

	require.True(t, bytes.Equal(first.Bytes, second.Bytes), "two builds of one range must be byte-identical")
	require.Equal(t, first.Digest, second.Digest)
}

// TestBuildCanonicalisesDetails: what the file carries must already be the form
// the hash covers, or a reader would have to guess at key order.
func TestBuildCanonicalisesDetails(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 1)
	entries[0].Details = json.RawMessage(`{"zeta": 1, "alpha": {"y": 2, "x": 3}}`)
	entries[0].Hash = audit.ComputeHash(&entries[0])

	built := build(t, tenantID, entries, nil, audit.GenesisHash)
	require.JSONEq(t, `{"alpha":{"x":3,"y":2},"zeta":1}`, string(built.Document.Entries[0].Details))
	require.Equal(t, `{"alpha":{"x":3,"y":2},"zeta":1}`, string(built.Document.Entries[0].Details),
		"details must be carried in canonical (key-sorted, compact) form")

	report, err := VerifyFile(built.Bytes)
	require.NoError(t, err)
	require.Equal(t, 1, report.Entries)
}

// TestVerifyFileAcceptsWhatBuildProduces — the round trip an auditor performs.
func TestVerifyFileAcceptsWhatBuildProduces(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 4)
	built := build(t, tenantID, entries, nil, audit.GenesisHash)

	report, err := VerifyFile(built.Bytes)
	require.NoError(t, err)
	require.True(t, report.DigestOK)
	require.True(t, report.Genesis)
	require.Equal(t, 4, report.Entries)
	require.Equal(t, int64(1), report.FromSeq)
	require.Equal(t, int64(4), report.ToSeq)
	require.Equal(t, int64(1), report.FirstVerifiedSeq)
	require.Equal(t, int64(4), report.LastVerifiedSeq)
	require.Zero(t, report.PreCutoverEntries)
	require.Equal(t, built.Digest, report.Digest)
}

// TestOneAlteredByteFailsVerification is the claim the export exists to
// support, tested at its weakest point: a single character changed anywhere in
// the evidence must be caught.
func TestOneAlteredByteFailsVerification(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 3)
	built := build(t, tenantID, entries, nil, audit.GenesisHash)

	// Alter the action of the middle entry, keeping the byte count identical so
	// nothing but the content differs.
	altered := bytes.Replace(built.Bytes, []byte(`"entity.updated"`), []byte(`"entity.deleted"`), 1)
	require.Len(t, altered, len(built.Bytes))
	require.NotEqual(t, built.Bytes, altered)

	_, err := VerifyFile(altered)
	require.Error(t, err)
	require.Contains(t, err.Error(), "digest mismatch")

	// And the same edit with the digest "repaired" — the attacker who knows how
	// the file is checked. The chain is what catches this one.
	var f File
	require.NoError(t, json.Unmarshal(altered, &f))
	sum := sha256.Sum256(f.Segment)
	repaired, err := json.Marshal(File{Segment: f.Segment, Digest: digestPrefix + hex.EncodeToString(sum[:])})
	require.NoError(t, err)

	_, err = VerifyFile(repaired)
	require.Error(t, err)
	require.Contains(t, err.Error(), "hash mismatch")
}

// TestRemovingAnEntryFailsVerification: deletion is the tampering an
// append-only ledger is most exposed to, and the one a digest alone would miss
// once recomputed.
func TestRemovingAnEntryFailsVerification(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 4)
	built := build(t, tenantID, entries, nil, audit.GenesisHash)

	var f File
	require.NoError(t, json.Unmarshal(built.Bytes, &f))
	var doc Document
	require.NoError(t, json.Unmarshal(f.Segment, &doc))

	doc.Entries = append(doc.Entries[:1], doc.Entries[2:]...) // drop seq 2
	doc.Range.Entries = len(doc.Entries)
	body, err := json.Marshal(doc)
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	forged, err := json.Marshal(File{Segment: body, Digest: digestPrefix + hex.EncodeToString(sum[:])})
	require.NoError(t, err)

	_, err = VerifyFile(forged)
	require.Error(t, err)
	require.Contains(t, err.Error(), "seq 3, want 2")
}

// TestSegmentMustMeetItsPredecessor: a series is only evidence if the joins
// hold, so a segment that claims a predecessor it does not link to is refused.
func TestSegmentMustMeetItsPredecessor(t *testing.T) {
	tenantID := uuid.NewString()
	firstEntries := chain(t, tenantID, audit.GenesisHash, 1, 3)
	first := build(t, tenantID, firstEntries, nil, audit.GenesisHash)

	secondEntries := chain(t, tenantID, first.Document.Chain.EndHash, 4, 2)
	second, err := Build(BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
		ExportedAt:    base.Add(2 * time.Hour),
		StartPrevHash: first.Document.Chain.EndHash,
		Entries:       secondEntries,
		Previous:      pointsAt(first),
	})
	require.NoError(t, err)

	_, err = VerifySeries([]Report{checked(t, first.Bytes), checked(t, second.Bytes)})
	require.NoError(t, err)

	// A segment cut from a chain state that is not the previous segment's end
	// is a hole, and must not verify as a series.
	orphan := second.Document
	orphan.Chain.StartPrevHash = strings.Repeat("a", 64)
	orphan.Previous.EndHash = orphan.Chain.StartPrevHash
	_, err = VerifySeries([]Report{checked(t, first.Bytes), {Document: orphan, Digest: "x"}})
	require.Error(t, err)

	// Nor may a segment claim a predecessor whose range does not abut it.
	broken := second.Document
	broken.Previous = &Previous{SegmentSeq: 1, ToSeq: 99, EndHash: broken.Chain.StartPrevHash}
	_, err = VerifyDocument(broken)
	require.ErrorContains(t, err, "a segment is missing")

	// And a segment that starts mid-chain while naming nothing before it is not
	// a position in a series at all — it is a file that can be put anywhere.
	loose := second.Document
	loose.Previous = nil
	_, err = VerifyDocument(loose)
	require.ErrorContains(t, err, "names no segment before it")
}

// TestASeriesIsCheckedAsAChainOfFiles is the defect a pile of individually
// perfect segments hides: each one names the file before it — its id, its last
// seq, its end hash and the DIGEST OF ITS BYTES — and until something compares
// that back-pointer with the file actually in front of it, an earlier segment
// can be re-sealed, swapped or replayed and every file still verifies.
//
// The three segments here carry no export record of their own (the shape of a
// capped range), so the linkage is the only thing standing between the archive
// and a reordered one. That is deliberate: it tests the linkage, not the seal.
func TestASeriesIsCheckedAsAChainOfFiles(t *testing.T) {
	tenantID := uuid.NewString()
	one := build(t, tenantID, chain(t, tenantID, audit.GenesisHash, 1, 3), nil, audit.GenesisHash)

	segment := func(seq int64, prev Built, at time.Time, id string, entries []audit.Entry) Built {
		built, err := Build(BuildInput{
			SegmentID: id, SegmentSeq: seq, TenantID: tenantID,
			ExportedAt:    at,
			StartPrevHash: prev.Document.Chain.EndHash,
			Entries:       entries,
			Previous:      pointsAt(prev),
		})
		require.NoError(t, err)
		return built
	}
	twoID := uuid.NewString()
	twoEntries := chain(t, tenantID, one.Document.Chain.EndHash, one.Document.Range.ToSeq+1, 2)
	two := segment(2, one, base.Add(2*time.Hour), twoID, twoEntries)
	three := segment(3, two, base.Add(3*time.Hour), uuid.NewString(),
		chain(t, tenantID, two.Document.Chain.EndHash, two.Document.Range.ToSeq+1, 2))

	series := func(files ...[]byte) error {
		reports := make([]Report, 0, len(files))
		for _, data := range files {
			reports = append(reports, checked(t, data))
		}
		_, err := VerifySeries(reports)
		return err
	}
	require.NoError(t, series(one.Bytes, two.Bytes, three.Bytes))

	t.Run("an earlier segment re-sealed in place", func(t *testing.T) {
		// Everything about segment 1 that a reader could compare is untouched:
		// same id, same range, same end hash. Only when it was exported has
		// moved — and the file is re-sealed, so its digest is consistent.
		redated := one.Document
		redated.ExportedAt = FormatInstant(base.Add(400 * 24 * time.Hour))

		require.NoError(t, series(one.Bytes, two.Bytes), "control: the untouched pair joins")
		err := series(reseal(t, redated), two.Bytes)
		require.ErrorContains(t, err, "re-sealed, replaced or replayed")
	})

	t.Run("an older version of a segment replayed into the series", func(t *testing.T) {
		// The hard case: a second file over the SAME range, with the same id,
		// the same entries and therefore the same end hash — a rebuild of the
		// segment taken at a different moment. Every field a reader could
		// compare agrees. Only the bytes differ, so only the digest can say
		// which of the two segment 3 was actually built on.
		twoAgain := segment(2, one, base.Add(9*time.Hour), twoID, twoEntries)
		require.NotEqual(t, two.Digest, twoAgain.Digest)
		require.Equal(t, two.Document.Chain.EndHash, twoAgain.Document.Chain.EndHash)

		err := series(one.Bytes, twoAgain.Bytes, three.Bytes)
		require.ErrorContains(t, err, "re-sealed, replaced or replayed")
	})

	t.Run("a back-pointer aimed at a file that is not there", func(t *testing.T) {
		repointed := copyEntries(three.Document)
		previous := *repointed.Previous
		previous.Digest = digestPrefix + strings.Repeat("b", 64)
		repointed.Previous = &previous

		err := series(one.Bytes, two.Bytes, reseal(t, repointed))
		require.ErrorContains(t, err, "re-sealed, replaced or replayed")
	})

	t.Run("a series checked without the digests proves nothing", func(t *testing.T) {
		_, err := VerifySeries([]Report{{Document: one.Document}, {Document: two.Document}})
		require.ErrorContains(t, err, "checked as a document rather than as a file")
	})
}

// TestASegmentsHeaderIsCoveredByTheChain. A segment's own words about itself —
// when it was exported, its id, the window it claims to cover, the predecessor
// it names — are covered by no entry hash, so on their own they can be edited
// and the file re-sealed. The export record the same cut appends restates them
// inside an entry the chain DOES cover, which is what makes them checkable.
func TestASegmentsHeaderIsCoveredByTheChain(t *testing.T) {
	tenantID := uuid.NewString()
	one := sealed(t, BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
		ExportedAt:    base.Add(time.Hour),
		StartPrevHash: audit.GenesisHash,
		Entries:       chain(t, tenantID, audit.GenesisHash, 1, 3),
	}, 0)

	report := checked(t, one.Bytes)
	require.True(t, report.HeaderSealed, "a segment that carries its own export record is sealed by it")

	for _, tc := range []struct {
		name  string
		edit  func(doc *Document)
		wants string
	}{
		{
			name:  "re-dated",
			edit:  func(doc *Document) { doc.ExportedAt = FormatInstant(base.Add(1000 * 24 * time.Hour)) },
			wants: "when it was exported",
		},
		{
			name:  "given another identity",
			edit:  func(doc *Document) { doc.SegmentID = uuid.NewString() },
			wants: "its id",
		},
		{
			name: "the window it claims widened",
			edit: func(doc *Document) {
				doc.Range.FirstOccurredAt = FormatInstant(base.Add(-5000 * time.Hour))
			},
			wants: "the window was edited",
		},
		{
			name: "the window it claims narrowed at the end",
			edit: func(doc *Document) {
				doc.Range.LastOccurredAt = FormatInstant(base.Add(5000 * time.Hour))
			},
			wants: "the window was edited",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := one.Document
			tc.edit(&doc)
			_, err := VerifyFile(reseal(t, doc))
			require.ErrorContains(t, err, tc.wants)
		})
	}

	// The same for the back-pointer: a segment cannot quietly change which file
	// it claims to follow, because the record says which one it followed.
	two := sealed(t, BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
		ExportedAt:    base.Add(2 * time.Hour),
		StartPrevHash: one.Document.Chain.EndHash,
		Entries:       chain(t, tenantID, one.Document.Chain.EndHash, one.Document.Range.ToSeq+1, 2),
		Previous:      pointsAt(one),
	}, 0)
	require.True(t, checked(t, two.Bytes).HeaderSealed)

	repointed := two.Document
	previous := *repointed.Previous
	previous.Digest = digestPrefix + strings.Repeat("c", 64)
	repointed.Previous = &previous
	_, err := VerifyFile(reseal(t, repointed))
	require.ErrorContains(t, err, "the digest of the segment before it")

	// And a genesis claim is sealed too: the record says this segment followed
	// nothing, so a later segment cannot be re-labelled as the start of a chain.
	_, err = VerifySeries([]Report{checked(t, one.Bytes), checked(t, two.Bytes)})
	require.NoError(t, err)
}

// sealedWithRecord is sealed() with the record's details chosen by the caller,
// so a test can build the shapes the product wrote in the PAST as well as the
// one it writes now. The details are hashed into the record entry exactly as
// the recorder hashes them, so every fixture below is a genuinely chained,
// digest-correct file — the point being that the reader must tell these apart
// by shape and not by anything a tamperer could also produce.
func sealedWithRecord(t *testing.T, in BuildInput, details func(from, to int64) map[string]any) Built {
	t.Helper()
	last := in.Entries[len(in.Entries)-1]
	record := audit.Entry{
		EventID:      uuid.NewString(),
		TenantID:     in.TenantID,
		Seq:          last.Seq + 1,
		ActorID:      SystemActorID,
		Action:       AuditAction,
		ResourceType: AuditResource,
		ResourceID:   in.TenantID,
		OccurredAt:   in.ExportedAt.UTC().Truncate(audit.ChainResolution),
		PrevHash:     last.Hash,
		HashVersion:  audit.CurrentHashVersion,
	}
	raw, err := json.Marshal(details(in.Entries[0].Seq, record.Seq))
	require.NoError(t, err)
	record.Details = raw
	record.Hash = audit.ComputeHash(&record)

	in.Entries = append(append([]audit.Entry(nil), in.Entries...), record)
	built, err := Build(in)
	require.NoError(t, err)
	return built
}

// preWaveRecord is the export record as every cut wrote it BEFORE 2026-09-13:
// the ordinal, the range and the object key, and none of the four fields the
// sealed header added. Reading those absent fields as "" and 0 is what made the
// reader call a genuine older archive an edit — and because a record lives in a
// write-once segment, such archives are permanent and cannot be re-issued.
func preWaveRecord(segmentSeq int64, tenantID string) func(from, to int64) map[string]any {
	return func(from, to int64) map[string]any {
		return map[string]any{
			"segmentSeq":    segmentSeq,
			"fromSeq":       from,
			"toSeq":         to,
			"entries":       to - from + 1,
			"objectKey":     objectKey(tenantID, from, to),
			"formatVersion": FormatVersion,
		}
	}
}

// withRecordDetails rewrites the export record a built segment carries, re-hashes
// that entry and re-seals the file: the tamperer who edits a record and rebuilds
// everything downstream of it. Since a segment's record is its LAST entry,
// "everything downstream" is the segment's own end hash — which is why fixtures
// like this cannot go through Build, whose self-check would refuse them.
func withRecordDetails(t *testing.T, built Built, details map[string]any) []byte {
	t.Helper()
	doc := copyEntries(built.Document)
	ae, err := doc.Entries[len(doc.Entries)-1].auditEntry()
	require.NoError(t, err)
	raw, err := json.Marshal(details)
	require.NoError(t, err)
	ae.Details = raw
	ae.Hash = audit.ComputeHash(&ae)
	doc.Entries[len(doc.Entries)-1] = FromAuditEntry(ae)
	doc.Chain.EndHash = ae.Hash
	return reseal(t, doc)
}

// TestARecordWrittenBeforeTheSealedHeaderIsNotAnEdit is the regression for the
// reader's worst possible mistake: calling an untampered archive tampered with.
//
// The sealed header (segmentId, exportedAt, previousDigest, preCutoverThroughSeq)
// was added to the export record on 2026-09-13. Segments exported before that
// carry a record that states the range and nothing else. json.Unmarshal cannot
// tell a field that was never written from one written empty, so the reader
// compared a real segment id against "" and reported "the header was edited" —
// which §6.9 tells the operator to escalate as tampering.
//
// The rule this pins: a record states all four or none of them. None is the
// older shape and is reported, not refused. Some of them is neither shape the
// product has ever written, and IS refused.
func TestARecordWrittenBeforeTheSealedHeaderIsNotAnEdit(t *testing.T) {
	tenantID := uuid.NewString()
	in := BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
		ExportedAt:    base.Add(time.Hour),
		StartPrevHash: audit.GenesisHash,
		Entries:       chain(t, tenantID, audit.GenesisHash, 1, 3),
	}

	old := sealedWithRecord(t, in, preWaveRecord(1, tenantID))
	report, err := VerifyFile(old.Bytes)
	require.NoError(t, err, "a segment exported before the header was sealed is not an edited segment")
	require.False(t, report.HeaderSealed, "the record states no part of the header, so the header is not sealed")
	require.True(t, report.HeaderRecordPreSeal, "and the reader must say WHY it is not sealed")

	from, to := old.Document.Range.FromSeq, old.Document.Range.ToSeq
	edited := func(f func(map[string]any)) []byte {
		r := preWaveRecord(1, tenantID)(from, to)
		f(r)
		return withRecordDetails(t, old, r)
	}

	// The range is bound either way: it is the one thing every record shape has
	// ever stated, so it is still held against the chain.
	_, err = VerifyFile(edited(func(r map[string]any) { r["entries"] = int64(99) }))
	require.ErrorContains(t, err, "how many entries it covers")

	// A record carrying SOME of the sealed fields is neither shape the product
	// has ever written. Its fields are hash-covered, so this is an edit that
	// also rebuilt the entry hash — and it must not be read as the older shape.
	_, err = VerifyFile(edited(func(r map[string]any) {
		r["segmentId"] = in.SegmentID
		r["exportedAt"] = FormatInstant(in.ExportedAt)
	}))
	require.ErrorContains(t, err, "a record is written whole, so this one was edited")
	require.ErrorContains(t, err, "segmentId, exportedAt")

	// A series that begins with such a segment verifies, and names it: the
	// archive is sound, and the report has to say which segments' ids and export
	// instants are the file's own words.
	two := sealed(t, BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
		ExportedAt:    base.Add(2 * time.Hour),
		StartPrevHash: old.Document.Chain.EndHash,
		Entries:       chain(t, tenantID, old.Document.Chain.EndHash, old.Document.Range.ToSeq+1, 2),
		Previous:      pointsAt(old),
	}, 0)

	series, err := VerifySeries([]Report{checked(t, old.Bytes), checked(t, two.Bytes)})
	require.NoError(t, err)
	require.Equal(t, []int64{1}, series.PreSealHeaders)
	require.Zero(t, series.UnsealedFrom, "the record arrived; it is simply older — that is not a capped range")

	// And the older shape buys a tamperer nothing: everything the record DOES
	// state is still checked, and the back-pointer to it is still the next
	// segment's business.
	relabelled := old.Document
	relabelled.SegmentID = uuid.NewString()
	_, err = VerifySeries([]Report{checked(t, reseal(t, relabelled)), checked(t, two.Bytes)})
	require.ErrorContains(t, err, "a different segment has been put in its place")
}

// TestRecordDetailsSurviveTheTrail pins Map to the struct tags: the writer
// records through the map and the verifier reads through the struct, so a key
// spelled differently in one of them would silently unseal every header.
func TestRecordDetailsSurviveTheTrail(t *testing.T) {
	want := RecordDetails{
		SegmentSeq: 4, SegmentID: uuid.NewString(), FromSeq: 11, ToSeq: 20, Entries: 10,
		ObjectKey:  "tenants/t/audit-segments/seg-000000000011-000000000020.json",
		ExportedAt: FormatInstant(base), PreviousDigest: digestPrefix + strings.Repeat("a", 64),
		PreCutoverThroughSeq: 7, FormatVersion: FormatVersion,
	}
	raw, err := json.Marshal(want.Map())
	require.NoError(t, err)

	var got RecordDetails
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want, got)
}

// TestPreCutoverEntriesAreToleratedOnlyAsALeadingRun mirrors the rule
// platform/audit enforces: rows written before the hash-chain cut-over cannot
// be recomputed at all, so they are link-checked as a PREFIX — and a
// pre-cut-over marker appearing after a verified entry is an attempt to exempt
// one row, not a cut-over.
func TestPreCutoverEntriesAreToleratedOnlyAsALeadingRun(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 3)
	// Mark the first as pre-cut-over and give it a hash that cannot be
	// recomputed (which is exactly what a v1 row is).
	entries[0].HashVersion = 1
	built := build(t, tenantID, entries, nil, audit.GenesisHash)

	report, err := VerifyFile(built.Bytes)
	require.NoError(t, err)
	require.Equal(t, 1, report.PreCutoverEntries)
	require.Equal(t, int64(2), report.FirstVerifiedSeq)
	require.Contains(t, report.String(), "pre-cut-over")

	// The same marker in the middle is refused.
	middle := chain(t, tenantID, audit.GenesisHash, 1, 3)
	middle[1].HashVersion = 1
	_, err = Build(BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
		ExportedAt: base, StartPrevHash: audit.GenesisHash, Entries: middle,
	})
	require.ErrorContains(t, err, "after verifiable entry")
}

// TestPreCutoverRunsBelongToATenantNotToAFile is the attack the per-file
// version of that rule invites, and the reason the rule cannot live in one
// file. Every file has a top. An attacker who rewrites the LEADING entries of
// any segment and marks them pre-cut-over never has to recompute a hash: the
// links still hold, the entries read as "the earliest ones", and a reader that
// judges the file on its own reports the trail intact.
//
// The exemption belongs to one contiguous run at the very beginning of a
// TENANT'S chain, ending at a sequence the product states inside the seal. Two
// defences follow from that, and this test holds both:
//
//   - a segment that carries its own export record is caught outright, because
//     the record says how far the prefix reaches and an entry hash covers it;
//   - a segment whose record is elsewhere (a capped range) cannot be certified
//     alone at all — only the series, from genesis, can settle it.
func TestPreCutoverRunsBelongToATenantNotToAFile(t *testing.T) {
	tenantID := uuid.NewString()

	genesis := sealed(t, BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
		ExportedAt:    base.Add(time.Hour),
		StartPrevHash: audit.GenesisHash,
		Entries:       chain(t, tenantID, audit.GenesisHash, 1, 4),
	}, 0)
	first := checked(t, genesis.Bytes)

	later := sealed(t, BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
		ExportedAt:    base.Add(2 * time.Hour),
		StartPrevHash: genesis.Document.Chain.EndHash,
		Entries:       chain(t, tenantID, genesis.Document.Chain.EndHash, genesis.Document.Range.ToSeq+1, 3),
		Previous:      pointsAt(genesis),
	}, 0)

	// The forgery: the first two entries of the RECENT segment are rewritten and
	// relabelled pre-cut-over, so nothing about them has to recompute.
	forge := func(doc Document) Document {
		out := copyEntries(doc)
		for i := 0; i < 2; i++ {
			out.Entries[i].HashVersion = audit.HashVersionNanosecond
			out.Entries[i].Action = "entity.deleted"
			out.Entries[i].ActorID = "00000000-0000-4000-8000-00000000dead"
		}
		return out
	}

	t.Run("the sealed record bounds the prefix", func(t *testing.T) {
		_, err := VerifyFile(reseal(t, forge(later.Document)))
		require.ErrorContains(t, err, "how far its pre-cut-over prefix reaches")
	})

	t.Run("and a capped segment cannot be certified alone", func(t *testing.T) {
		// The same range without its own export record: what a segment capped by
		// MaxEntriesPerSegment looks like.
		capped, err := Build(BuildInput{
			SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
			ExportedAt:    base.Add(2 * time.Hour),
			StartPrevHash: genesis.Document.Chain.EndHash,
			Entries:       chain(t, tenantID, genesis.Document.Chain.EndHash, genesis.Document.Range.ToSeq+1, 3),
			Previous:      pointsAt(genesis),
		})
		require.NoError(t, err)

		clean := checked(t, capped.Bytes)
		require.True(t, clean.PreCutoverProven, "nothing to prove when nothing claims the exemption")
		_, err = VerifySeries([]Report{first, clean})
		require.NoError(t, err)

		// The file itself still verifies — it is internally consistent, which is
		// exactly why a per-file rule is worthless — but it proves nothing, and
		// the series refuses it.
		report := checked(t, reseal(t, forge(capped.Document)))
		require.Equal(t, 2, report.PreCutoverEntries)
		require.False(t, report.PreCutoverProven,
			"a run at the top of a mid-chain file is not the beginning of the tenant's chain")

		_, err = VerifySeries([]Report{first, report})
		require.ErrorContains(t, err, "the very beginning of a tenant's chain")
	})

	t.Run("a genuine prefix is accepted, and stated", func(t *testing.T) {
		// A tenant whose first entries really do predate the cut-over: the run
		// leads the genesis segment and the record says where it ends.
		entries := chain(t, tenantID, audit.GenesisHash, 1, 4)
		entries[0].HashVersion = audit.HashVersionNanosecond
		entries[1].HashVersion = audit.HashVersionNanosecond
		historic := sealed(t, BuildInput{
			SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
			ExportedAt:    base.Add(time.Hour),
			StartPrevHash: audit.GenesisHash,
			Entries:       entries,
		}, 2)

		report := checked(t, historic.Bytes)
		require.Equal(t, 2, report.PreCutoverEntries)
		require.Equal(t, int64(2), report.PreCutoverThroughSeq)
		require.True(t, report.PreCutoverProven)
		require.Contains(t, report.String(), "pre-cut-over through seq 2")

		result, err := VerifySeries([]Report{report})
		require.NoError(t, err)
		require.Equal(t, int64(2), result.PreCutoverThroughSeq)

		// And the run may not be stretched past the sequence the record states.
		stretched := copyEntries(historic.Document)
		stretched.Entries[2].HashVersion = audit.HashVersionNanosecond
		_, err = VerifyFile(reseal(t, stretched))
		require.ErrorContains(t, err, "how far its pre-cut-over prefix reaches")
	})

	t.Run("a run may continue while every earlier segment is entirely pre-cut-over", func(t *testing.T) {
		// The legitimate multi-segment prefix: a tenant whose historical rows
		// are longer than one segment. Neither file carries its own record.
		older := chain(t, tenantID, audit.GenesisHash, 1, 3)
		for i := range older {
			older[i].HashVersion = audit.HashVersionNanosecond
		}
		head, err := Build(BuildInput{
			SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
			ExportedAt: base, StartPrevHash: audit.GenesisHash, Entries: older,
		})
		require.NoError(t, err)

		rest := chain(t, tenantID, head.Document.Chain.EndHash, 4, 3)
		rest[0].HashVersion = audit.HashVersionNanosecond
		tail, err := Build(BuildInput{
			SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
			ExportedAt:    base.Add(time.Hour),
			StartPrevHash: head.Document.Chain.EndHash,
			Entries:       rest,
			Previous:      pointsAt(head),
		})
		require.NoError(t, err)

		result, err := VerifySeries([]Report{checked(t, head.Bytes), checked(t, tail.Bytes)})
		require.NoError(t, err)
		require.Equal(t, int64(4), result.PreCutoverThroughSeq,
			"the prefix is stated as one run of the tenant's chain, across the files it spans")
	})

	t.Run("a window that does not reach genesis can prove no prefix at all", func(t *testing.T) {
		capped, err := Build(BuildInput{
			SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
			ExportedAt:    base.Add(2 * time.Hour),
			StartPrevHash: genesis.Document.Chain.EndHash,
			Entries:       chain(t, tenantID, genesis.Document.Chain.EndHash, genesis.Document.Range.ToSeq+1, 3),
			Previous:      pointsAt(genesis),
		})
		require.NoError(t, err)

		_, err = VerifySeries([]Report{checked(t, reseal(t, forge(capped.Document)))})
		require.ErrorContains(t, err, "not at the tenant's genesis")
	})
}

// TestBuildRefusesWhatItCouldNotVerify: the export must never copy a broken
// chain into the evidence store as though it were evidence.
func TestBuildRefusesWhatItCouldNotVerify(t *testing.T) {
	tenantID := uuid.NewString()
	entries := chain(t, tenantID, audit.GenesisHash, 1, 3)
	entries[2].Hash = strings.Repeat("f", 64) // a row edited in the database

	_, err := Build(BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
		ExportedAt: base, StartPrevHash: audit.GenesisHash, Entries: entries,
	})
	require.ErrorContains(t, err, "refusing to export an unverifiable segment")

	// And a segment whose starting point does not match the first entry.
	_, err = Build(BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
		ExportedAt:    base,
		StartPrevHash: strings.Repeat("b", 64),
		Entries:       chain(t, tenantID, audit.GenesisHash, 1, 2),
	})
	require.ErrorContains(t, err, "prev_hash mismatch")
}

// TestVerifyRefusesAFutureFormat: a reader that does not know the shape must
// say so rather than half-check it and report success.
func TestVerifyRefusesAFutureFormat(t *testing.T) {
	tenantID := uuid.NewString()
	built := build(t, tenantID, chain(t, tenantID, audit.GenesisHash, 1, 2), nil, audit.GenesisHash)

	doc := built.Document
	doc.FormatVersion = FormatVersion + 1
	_, err := VerifyDocument(doc)
	require.ErrorContains(t, err, "is not this verifier's")
}
