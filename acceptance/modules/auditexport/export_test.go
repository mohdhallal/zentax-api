package auditexport_test

import (
	"bytes"
	"context"
	"os"

	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/audit/worm"
)

// TestFirstExportCarriesTheChainAndItsOwnRecord is the base case: a tenant that
// has never been exported, and what an auditor gets.
//
// The segment must start at genesis, carry every entry, verify from its bytes
// alone — and END with the record of its own export, which is what makes "when
// was evidence taken" answerable from the trail AND from the file, and what
// stops the export from generating an endless series of files each recording
// the last one.
func (s *AuditExportSuite) TestFirstExportCarriesTheChainAndItsOwnRecord() {
	tenant := s.InsertTenant("worm-first", "WORM First").String()
	actor := s.seedActor(tenant)
	s.appendEntries(tenant, actor, 3)

	report := s.run(worm.Settings{})
	s.Require().GreaterOrEqual(report.Segments, 1)
	s.Require().GreaterOrEqual(report.TenantsExported, 1)
	s.Require().Zero(report.TenantsFailed)

	rows := s.ledger(tenant)
	s.Require().Len(rows, 1)
	row := rows[0]
	s.Require().Equal("uploaded", row.Status)
	s.Require().Equal(int64(1), row.SegmentSeq)
	s.Require().Equal(int64(1), row.FromSeq)
	// Three domain entries plus the export's own record.
	s.Require().Equal(int64(4), row.ToSeq)
	s.Require().Equal(4, row.Entries)
	s.Require().Equal(s.chainHead(tenant), row.ToSeq,
		"a completed export must leave the cursor at the chain head")

	rep, doc := s.verifyOffline(row.ObjectKey)
	s.Require().True(rep.DigestOK)
	s.Require().True(rep.Genesis)
	s.Require().Equal(4, rep.Entries)
	s.Require().Equal(int64(1), rep.FirstVerifiedSeq)
	s.Require().Equal(int64(4), rep.LastVerifiedSeq)
	s.Require().Equal(row.Digest, rep.Digest, "the ledger's digest must be the file's digest")
	s.Require().Equal(audit.GenesisHash, doc.Chain.StartPrevHash)
	s.Require().Nil(doc.Previous)
	s.Require().Equal(tenant, doc.TenantID)

	// The last entry IS the export record, and it is attributed to the derived
	// system actor rather than to a person.
	last := doc.Entries[len(doc.Entries)-1]
	s.Require().Equal(worm.AuditAction, last.Action)
	s.Require().Equal(worm.AuditResource, last.ResourceType)
	s.Require().Equal(tenant, last.ResourceID)
	s.Require().Equal(worm.SystemActorID, last.ActorID)

	// And the same fact is in the trail, where an auditor looks for it — with
	// the document's whole header restated, because nothing else covers it.
	entries := s.exportEntries(tenant)
	s.Require().Len(entries, 1)
	details := s.detailsOf(entries[0])
	s.Require().EqualValues(1, details["fromSeq"])
	s.Require().EqualValues(4, details["toSeq"])
	s.Require().EqualValues(4, details["entries"])
	s.Require().Equal(row.ObjectKey, details["objectKey"])
	s.Require().Equal(doc.SegmentID, details["segmentId"])
	s.Require().Equal(doc.ExportedAt, details["exportedAt"])
	s.Require().Equal("", details["previousDigest"], "the tenant's first segment follows nothing, and says so where it counts")
	s.Require().EqualValues(0, details["preCutoverThroughSeq"])

	// Which is what makes the header checkable: the file alone could say
	// anything, and the record is inside the chain.
	s.Require().True(rep.HeaderSealed)
	_, err := worm.VerifyFile(s.reseal(row.ObjectKey, func(d *worm.Document) {
		d.ExportedAt = "2030-01-01T00:00:00.000000Z"
	}))
	s.Require().ErrorContains(err, "when it was exported")

	// The file carries the recipe for checking it without us.
	s.Require().Equal("sha256", doc.Verification.Algorithm)
	s.Require().NotEmpty(doc.Verification.EntryHashFields)
	s.Require().NotEmpty(doc.Verification.Procedure)
}

// TestATenantWithNothingNewExportsNothing. The quiet case has to be genuinely
// quiet: no object, no ledger row, and — the part that is easy to get wrong —
// no audit entry either, or every pass would create the work for the next one.
func (s *AuditExportSuite) TestATenantWithNothingNewExportsNothing() {
	tenant := s.InsertTenant("worm-quiet", "WORM Quiet").String()
	actor := s.seedActor(tenant)
	s.appendEntries(tenant, actor, 2)

	first := s.run(worm.Settings{})
	s.Require().GreaterOrEqual(first.Segments, 1)
	headAfterFirst := s.chainHead(tenant)

	second := s.run(worm.Settings{})
	s.Require().Zero(second.TenantsFailed)

	s.Require().Len(s.ledger(tenant), 1, "a quiet pass must not cut a segment")
	s.Require().Equal(headAfterFirst, s.chainHead(tenant), "a quiet pass must not append to the chain")
	s.Require().Len(s.exportEntries(tenant), 1)

	// A third pass is still quiet — the fixed point holds.
	s.run(worm.Settings{})
	s.Require().Len(s.ledger(tenant), 1)
	s.Require().Equal(headAfterFirst, s.chainHead(tenant))
}

// TestATenantWithNoTrailAtAllIsNeverExported: nothing to attest to, nothing
// written, and no empty file to explain to an auditor.
func (s *AuditExportSuite) TestATenantWithNoTrailAtAllIsNeverExported() {
	tenant := s.InsertTenant("worm-empty", "WORM Empty").String()

	report := s.run(worm.Settings{})
	s.Require().Zero(report.TenantsFailed)
	s.Require().Empty(s.ledger(tenant))
	s.Require().Zero(s.chainHead(tenant))
}

// TestTheNextSegmentContinuesTheChain: consecutive segments must MEET — same
// tenant, contiguous sequence numbers, each starting from the previous one's
// end hash — or a series of files is not evidence of a series of events.
func (s *AuditExportSuite) TestTheNextSegmentContinuesTheChain() {
	tenant := s.InsertTenant("worm-series", "WORM Series").String()
	actor := s.seedActor(tenant)

	s.appendEntries(tenant, actor, 2)
	s.Require().Zero(s.run(worm.Settings{}).TenantsFailed)

	s.appendEntries(tenant, actor, 3)
	s.Require().Zero(s.run(worm.Settings{}).TenantsFailed)

	rows := s.ledger(tenant)
	s.Require().Len(rows, 2)
	s.Require().Equal(rows[0].ToSeq+1, rows[1].FromSeq, "ranges must be contiguous")
	s.Require().Equal(rows[0].EndHash, rows[1].StartPrev, "the second segment must start where the first ended")

	firstReport, first := s.verifyOffline(rows[0].ObjectKey)
	secondReport, second := s.verifyOffline(rows[1].ObjectKey)

	s.Require().NotNil(second.Previous)
	s.Require().Equal(first.SegmentID, second.Previous.SegmentID)
	s.Require().Equal(first.Range.ToSeq, second.Previous.ToSeq)
	s.Require().Equal(first.Chain.EndHash, second.Previous.EndHash)
	s.Require().Equal("sha256:"+rows[0].Digest, second.Previous.Digest,
		"a segment must name the digest of the one before it, so a missing file is detectable")
	s.Require().Equal(rows[0].ObjectKey, second.Previous.ObjectKey,
		"and where to find it")

	series, err := worm.VerifySeries([]worm.Report{firstReport, secondReport})
	s.Require().NoError(err)
	s.Require().Equal(2, series.Segments)
	s.Require().Zero(series.UnsealedFrom, "every segment here carries its own export record")
	s.Require().True(first.Chain.Genesis)
	s.Require().False(second.Chain.Genesis)
	s.Require().Equal(s.chainHead(tenant), second.Range.ToSeq)

	// The join is proven against the FILE, not against a document lifted out of
	// it. Re-date the first segment and re-seal it — the attacker who has read
	// the verification block — and both defences must hold on real stored
	// bytes: the header no longer matches the record the chain carries, and
	// even a reader that skipped that check finds the second segment naming a
	// digest that is no longer the file in front of it.
	redated := s.reseal(rows[0].ObjectKey, func(doc *worm.Document) {
		doc.ExportedAt = "2030-01-01T00:00:00.000000Z"
	})
	_, err = worm.VerifyFile(redated)
	s.Require().ErrorContains(err, "when it was exported")

	forged := firstReport
	forged.Document.ExportedAt = "2030-01-01T00:00:00.000000Z"
	forged.Digest = s.digestOf(redated)
	_, err = worm.VerifySeries([]worm.Report{forged, secondReport})
	s.Require().ErrorContains(err, "re-sealed, replaced or replayed")
}

// TestAFailedUploadKeepsTheRangeOwed. The store is down; nothing may be lost,
// and — the failure that would be invisible — the cursor must NOT move, or the
// next pass would cut past entries that were never written anywhere.
func (s *AuditExportSuite) TestAFailedUploadKeepsTheRangeOwed() {
	tenant := s.InsertTenant("worm-fail", "WORM Fail").String()
	actor := s.seedActor(tenant)
	s.appendEntries(tenant, actor, 3)

	_, err := s.exporter(refusingStore{}, worm.Settings{}).Run(context.Background())
	s.Require().Error(err)
	s.Require().ErrorContains(err, "upload segment")

	rows := s.ledger(tenant)
	s.Require().Len(rows, 1)
	s.Require().Equal("pending", rows[0].Status, "an unlanded segment stays owed")
	s.Require().Equal(1, rows[0].Attempts)
	s.Require().NoFileExists(s.segmentPath(rows[0].ObjectKey))

	pendingRange := rows[0]

	// A second failing pass must retry the SAME range rather than cut a new one.
	_, err = s.exporter(refusingStore{}, worm.Settings{}).Run(context.Background())
	s.Require().Error(err)
	rows = s.ledger(tenant)
	s.Require().Len(rows, 1, "a failed upload must never leave a second claim behind")
	s.Require().Equal(pendingRange.FromSeq, rows[0].FromSeq)
	s.Require().Equal(pendingRange.ToSeq, rows[0].ToSeq)
	s.Require().Equal(2, rows[0].Attempts)

	// And when the store comes back, the same range lands — with exactly one
	// export record in the trail, not one per attempt.
	report := s.run(worm.Settings{})
	s.Require().GreaterOrEqual(report.Resumed, 1)

	rows = s.ledger(tenant)
	s.Require().Len(rows, 1)
	s.Require().Equal("uploaded", rows[0].Status)
	s.Require().Equal(pendingRange.Digest, rows[0].Digest, "the retry must be the same bytes")
	s.Require().Len(s.exportEntries(tenant), 1, "a retried export is one export")

	rep, doc := s.verifyOffline(rows[0].ObjectKey)
	s.Require().True(rep.Genesis)
	s.Require().Equal(int64(1), doc.Range.FromSeq)
	s.Require().Equal(s.chainHead(tenant), doc.Range.ToSeq)
}

// TestACrashAfterTheObjectLandedIsIdempotent is the case the ledger cannot
// distinguish from a failed upload: the bytes reached the store and the process
// died before the ledger was told. Re-uploading must be a no-op in substance —
// the identical object, under the identical key — and must not duplicate the
// entries or the trail record.
func (s *AuditExportSuite) TestACrashAfterTheObjectLandedIsIdempotent() {
	tenant := s.InsertTenant("worm-crash", "WORM Crash").String()
	actor := s.seedActor(tenant)
	s.appendEntries(tenant, actor, 4)

	_, err := s.exporter(crashingStore{inner: s.dest}, worm.Settings{}).Run(context.Background())
	s.Require().Error(err)

	rows := s.ledger(tenant)
	s.Require().Len(rows, 1)
	s.Require().Equal("pending", rows[0].Status)
	landed := s.readSegment(rows[0].ObjectKey)
	s.Require().NotEmpty(landed, "the crash left the object in the store")

	// The next pass finishes it.
	report := s.run(worm.Settings{})
	s.Require().GreaterOrEqual(report.Resumed, 1)

	rows = s.ledger(tenant)
	s.Require().Len(rows, 1)
	s.Require().Equal("uploaded", rows[0].Status)
	s.Require().True(bytes.Equal(landed, s.readSegment(rows[0].ObjectKey)),
		"a resumed segment must be byte-identical to the one already in the store")
	s.Require().Len(s.exportEntries(tenant), 1, "resuming must not record a second export")

	rep, _ := s.verifyOffline(rows[0].ObjectKey)
	s.Require().True(rep.DigestOK)
	s.Require().Equal(5, rep.Entries)

	// And the tenant is caught up: the next pass leaves it alone.
	s.run(worm.Settings{})
	s.Require().Len(s.ledger(tenant), 1)
	s.Require().Len(s.exportEntries(tenant), 1)
}

// TestOneAlteredByteInTheStoredFileIsDetected — the property the whole export
// exists to give an auditor, checked against a real file on a real disk.
func (s *AuditExportSuite) TestOneAlteredByteInTheStoredFileIsDetected() {
	tenant := s.InsertTenant("worm-tamper", "WORM Tamper").String()
	actor := s.seedActor(tenant)
	s.appendEntries(tenant, actor, 3)
	s.Require().Zero(s.run(worm.Settings{}).TenantsFailed)

	row := s.ledger(tenant)[0]
	path := s.segmentPath(row.ObjectKey)
	original := s.readSegment(row.ObjectKey)

	// Flip exactly one byte inside the entries, keeping the length identical.
	altered := bytes.Replace(original, []byte(`"entity.updated"`), []byte(`"entity.deleted"`), 1)
	s.Require().Len(altered, len(original))
	s.Require().NotEqual(original, altered)
	s.Require().NoError(os.WriteFile(path, altered, 0o600))

	_, err := worm.VerifyFile(s.readSegment(row.ObjectKey))
	s.Require().Error(err, "an altered segment must not verify")
	s.Require().ErrorContains(err, "digest mismatch")

	// Put it back and confirm the failure was the edit, not the reading.
	s.Require().NoError(os.WriteFile(path, original, 0o600))
	_, err = worm.VerifyFile(s.readSegment(row.ObjectKey))
	s.Require().NoError(err)
}

// TestABacklogIsExportedAsASeriesThatCoversEveryEntry: a tenant far behind must
// end up fully covered, once, with no gap and no overlap between the files.
func (s *AuditExportSuite) TestABacklogIsExportedAsASeriesThatCoversEveryEntry() {
	tenant := s.InsertTenant("worm-backlog", "WORM Backlog").String()
	actor := s.seedActor(tenant)
	s.appendEntries(tenant, actor, 9)

	report := s.run(worm.Settings{MaxEntriesPerSegment: 3})
	s.Require().GreaterOrEqual(report.Segments, 3)

	rows := s.ledger(tenant)
	s.Require().GreaterOrEqual(len(rows), 3)

	reports := make([]worm.Report, 0, len(rows))
	var next int64 = 1
	for _, row := range rows {
		s.Require().Equal("uploaded", row.Status)
		s.Require().Equal(next, row.FromSeq, "segments must tile the chain without a gap or an overlap")
		s.Require().LessOrEqual(row.Entries, 3)
		next = row.ToSeq + 1
		report, _ := s.verifyOffline(row.ObjectKey)
		reports = append(reports, report)
	}
	// A capped range stops short of its own export record, so some of these
	// headers are sealed by a LATER segment — which only the series can resolve.
	series, err := worm.VerifySeries(reports)
	s.Require().NoError(err)
	s.Require().Equal(len(rows), series.Segments)
	s.Require().True(reports[0].Genesis)
	s.Require().Equal(s.chainHead(tenant), rows[len(rows)-1].ToSeq,
		"the series must reach the chain head")

	// Every export appended one record of its own, and each is inside a segment.
	s.Require().Len(s.exportEntries(tenant), len(rows))
}

// TestTenantsAreExportedSeparately: one tenant's evidence may never contain
// another's entries, and one tenant's failure may not stop another's export.
func (s *AuditExportSuite) TestTenantsAreExportedSeparately() {
	first := s.InsertTenant("worm-t1", "WORM One").String()
	second := s.InsertTenant("worm-t2", "WORM Two").String()
	s.appendEntries(first, s.seedActor(first), 2)
	s.appendEntries(second, s.seedActor(second), 3)

	report := s.run(worm.Settings{})
	s.Require().Zero(report.TenantsFailed)
	s.Require().GreaterOrEqual(report.TenantsExported, 2)

	firstRow, secondRow := s.ledger(first)[0], s.ledger(second)[0]
	s.Require().NotEqual(firstRow.ObjectKey, secondRow.ObjectKey)
	s.Require().Contains(firstRow.ObjectKey, first)
	s.Require().Contains(secondRow.ObjectKey, second)

	_, firstDoc := s.verifyOffline(firstRow.ObjectKey)
	_, secondDoc := s.verifyOffline(secondRow.ObjectKey)
	s.Require().Len(firstDoc.Entries, 3)
	s.Require().Len(secondDoc.Entries, 4)
	for _, e := range firstDoc.Entries {
		s.Require().Equal(first, e.TenantID)
	}
	for _, e := range secondDoc.Entries {
		s.Require().Equal(second, e.TenantID)
	}
}
