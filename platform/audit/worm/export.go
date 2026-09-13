package worm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/storage"
)

// The trail entry an export leaves. Taking evidence out of the database is a
// FACT ABOUT THE TENANT — "on this date, this stretch of your trail was written
// to <key>" — and an auditor's first question is when evidence was taken, so it
// belongs in the trail rather than only in a log line.
//
// The resource is the TENANT itself, and deliberately so: the segment is not a
// domain object anyone can open, the export is a thing that happened to the
// tenant's ledger, and reusing the existing tenant-level vocabulary keeps the
// entry inside the read scope an auditor already has (a tenant-wide grant, per
// ADR-0012) instead of inventing a family with no reader.
//
// These are spelled out as literals at the Record call below, and must be:
// modules/auditlog derives GET /audit-log's filter vocabulary by scanning the
// source for Record's arguments.
const (
	AuditAction   = "audit.exported"
	AuditResource = "tenant"
)

// SystemActorID is who an export is attributed to. ADR-0008 records actors by
// id and never by name, and this work has no acting person: it is the product
// itself, on a schedule.
//
// It is DERIVED rather than invented — a version-5 UUID of a fixed URN — so it
// is the same id in every deployment, reproducible by anyone who wants to check
// it (uuid.NewSHA1(uuid.NameSpaceURL, "urn:zentax:actor:audit-worm-export")),
// and impossible to confuse with a real user's id. It resolves to no row in
// `users`, so the trail shows the id with no name, which is the honest
// rendering: nobody did this.
var SystemActorID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("urn:zentax:actor:audit-worm-export")).String()

// AuditRecorder is the slice of platform/audit.Recorder this package needs.
type AuditRecorder interface {
	Record(ctx context.Context, action, resourceType, resourceID string, details map[string]any) error
}

// Default settings.
const (
	// DefaultMaxEntriesPerSegment bounds one file. A segment is built in memory
	// and must stay something an auditor can open, so a tenant with a large
	// backlog is exported as a SERIES rather than as one enormous object.
	DefaultMaxEntriesPerSegment = 5000
	// DefaultMaxSegmentsPerRun bounds how much of one tenant's backlog a single
	// pass will work through, so a tenant catching up cannot starve the others.
	// Whatever is left is taken by the next pass.
	DefaultMaxSegmentsPerRun = 20
	// DefaultKeyPrefix is the root of the object keys. Segments live beside the
	// documents of the same tenant when the two share a store, and never
	// collide with them.
	DefaultKeyPrefix = "tenants"

	// minEntriesPerSegment: each cut appends one entry of its own (the export
	// record), so a segment that could hold only one entry would never catch
	// up with the chain.
	minEntriesPerSegment = 2
)

// Settings tune one exporter. Zero values take the defaults above.
type Settings struct {
	MaxEntriesPerSegment int
	MaxSegmentsPerRun    int
	KeyPrefix            string
}

func (s Settings) withDefaults() Settings {
	if s.MaxEntriesPerSegment <= 0 {
		s.MaxEntriesPerSegment = DefaultMaxEntriesPerSegment
	}
	if s.MaxEntriesPerSegment < minEntriesPerSegment {
		// Each cut appends one entry of its own, so a smaller cap would never
		// catch up. Config validation refuses it too; this is the floor for a
		// Settings assembled in code.
		s.MaxEntriesPerSegment = minEntriesPerSegment
	}
	if s.MaxSegmentsPerRun <= 0 {
		s.MaxSegmentsPerRun = DefaultMaxSegmentsPerRun
	}
	if s.KeyPrefix == "" {
		s.KeyPrefix = DefaultKeyPrefix
	}
	return s
}

// Exporter is the scheduled WORM export.
//
// One pass, per tenant, does at most three things, in this order, and each one
// is safe to interrupt anywhere:
//
//  1. FINISH what an earlier pass left. A tenant has at most one unfinished
//     segment (the database enforces it). Its bytes are rebuilt from the same
//     rows, under the same exportedAt, so they are byte-identical to the ones
//     that may already be in the store — re-uploading is therefore idempotent,
//     whether the crash happened before the object landed or between the object
//     landing and the ledger being told.
//  2. CUT the next range. Under the tenant's chain lock: read the cursor and
//     the chain head, append the audit entry that records the export — which
//     restates the document's header, so the header is covered by the chain and
//     not only by the file that makes the claim — read the entries, the export
//     record INCLUDED, so the segment attests to its own creation, then build
//     and verify the document and write the pending ledger row. All on one
//     transaction, so a crash here leaves nothing at all.
//  3. UPLOAD it and confirm. Only the confirmation moves the cursor, so a
//     failed upload costs a retry, never an entry.
//
// What cannot happen, and why:
//
//	a lost entry        the cursor only ever moves to the to_seq of a segment
//	                    that has LANDED; anything not yet landed is re-cut or
//	                    re-uploaded.
//	a duplicated entry  ranges are contiguous by construction and unique in the
//	                    database (tenant_id, from_seq); a resumed segment keeps
//	                    its range, its key and its bytes.
//	a false "done"      the ledger row is written on the same transaction as the
//	                    audit entry and is only marked uploaded after Put
//	                    returns; there is no state in the process.
type Exporter struct {
	tx    database.ExecerPgTx
	store *Store
	dest  storage.Storage
	audit AuditRecorder

	settings Settings
	actor    string
	now      func() time.Time
}

// NewExporter builds the job. dest is the object store the segments are written
// to — normally a dedicated, locked bucket; see README.md for what a filesystem
// destination can and cannot promise.
func NewExporter(tx database.ExecerPgTx, store *Store, dest storage.Storage, recorder AuditRecorder, settings Settings) *Exporter {
	return &Exporter{
		tx:       tx,
		store:    store,
		dest:     dest,
		audit:    recorder,
		settings: settings.withDefaults(),
		actor:    SystemActorID,
		now:      time.Now,
	}
}

// WithClock replaces the exporter's clock. The stamped instant is inside the
// hashed document, so the tests that assert byte-identical rebuilds choose it.
func (x *Exporter) WithClock(now func() time.Time) *Exporter {
	if now != nil {
		x.now = now
	}
	return x
}

// JobName identifies the export to the scheduler and to metrics.
const JobName = "audit.worm-export"

// Name identifies the job to the scheduler and to metrics.
func (x *Exporter) Name() string { return JobName }

// RunReport is what one pass did. Counts and tenant ids only — no entry
// content, so the whole report is safe to log.
type RunReport struct {
	Tenants         int
	TenantsExported int
	TenantsFailed   int
	// Segments landed in the object store this pass; Resumed is how many of
	// those had been cut by an EARLIER pass and were finished by this one.
	Segments int
	Resumed  int
	Entries  int
	// AtBudget is the tenants that used up MaxSegmentsPerRun in this pass. Not
	// a failure, and not necessarily a backlog either — whatever is left, if
	// anything, is taken by the next pass.
	AtBudget int
}

// Run executes one pass over every tenant. One tenant's failure never stops
// another's: the error is the joined tenant failures and the report is still
// the truth about the rest.
func (x *Exporter) Run(ctx context.Context) (RunReport, error) {
	var report RunReport

	if x.dest == nil {
		return report, errors.New("worm: no export destination configured")
	}

	tenants, err := x.store.TenantIDs(ctx)
	if err != nil {
		return report, err
	}
	report.Tenants = len(tenants)

	var failures []error
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(failures, err)...)
		}
		before := report.Segments
		if err := x.exportTenant(ctx, tenantID, &report); err != nil {
			report.TenantsFailed++
			failures = append(failures, fmt.Errorf("tenant %s: %w", tenantID, err))
			logIfConfigured(ctx).Error("Audit export failed for a tenant",
				logger.String("tenantId", tenantID), logger.Error(err))
			continue
		}
		if report.Segments > before {
			report.TenantsExported++
		}
	}

	logIfConfigured(ctx).Info("Audit export pass complete",
		logger.Int("tenants", report.Tenants),
		logger.Int("tenantsExported", report.TenantsExported),
		logger.Int("tenantsFailed", report.TenantsFailed),
		logger.Int("segments", report.Segments),
		logger.Int("resumed", report.Resumed),
		logger.Int("entries", report.Entries),
		logger.Int("atBudget", report.AtBudget),
	)
	return report, errors.Join(failures...)
}

// exportTenant works one tenant until it is caught up or the per-run budget is
// spent.
func (x *Exporter) exportTenant(ctx context.Context, tenantID string, report *RunReport) error {
	for i := 0; i < x.settings.MaxSegmentsPerRun; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		more, err := x.step(ctx, tenantID, report)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
	report.AtBudget++
	logIfConfigured(ctx).Info("Audit export used its per-pass budget for a tenant; anything left is taken by the next pass",
		logger.String("tenantId", tenantID),
		logger.Int("segmentsPerRun", x.settings.MaxSegmentsPerRun))
	return nil
}

// step does one unit of work and says whether there may be more.
func (x *Exporter) step(ctx context.Context, tenantID string, report *RunReport) (bool, error) {
	pending, err := x.pendingOf(ctx, tenantID)
	if err != nil {
		return false, err
	}
	if pending != nil {
		if err := x.finish(ctx, tenantID, *pending, nil); err != nil {
			return false, err
		}
		report.Segments++
		report.Resumed++
		report.Entries += pending.Entries
		return true, nil
	}

	row, built, cut, err := x.cut(ctx, tenantID)
	if err != nil || !cut {
		return false, err
	}
	if err := x.finish(ctx, tenantID, row, &built); err != nil {
		return false, err
	}
	report.Segments++
	report.Entries += row.Entries
	return true, nil
}

// cut reserves the next range: one transaction that appends the audit entry,
// reads the entries (its own record included), builds and VERIFIES the
// document, and writes the pending ledger row. Nothing is uploaded here, so a
// crash anywhere in it leaves the tenant exactly as it was.
func (x *Exporter) cut(ctx context.Context, tenantID string) (Ledger, Built, bool, error) {
	var (
		row   Ledger
		built Built
		cut   bool
	)

	err := x.tx.WithinTransaction(x.tenantContext(ctx, tenantID), func(ctx context.Context) error {
		// Serialize against every other appender of this tenant's chain, so the
		// head measured here is still the head when the record is appended.
		if err := x.store.LockChain(ctx, tenantID); err != nil {
			return err
		}

		last, err := x.store.LastLanded(ctx, tenantID)
		if err != nil {
			return err
		}
		from, startPrev, segmentSeq := int64(1), audit.GenesisHash, int64(1)
		if last != nil {
			from, startPrev, segmentSeq = last.ToSeq+1, last.EndHash, last.SegmentSeq+1
		}

		head, err := x.store.ChainHead(ctx, tenantID)
		if err != nil {
			return err
		}
		switch {
		case head < from-1:
			// The chain is SHORTER than what has already been exported. In an
			// append-only ledger that cannot happen; refusing here is the whole
			// point of keeping the cursor.
			return fmt.Errorf("worm: chain head is seq %d but segments through seq %d have been exported (entries have been removed)",
				head, from-1)
		case head < from:
			return nil // nothing new — the ordinary quiet-tenant case
		}

		// The record this cut is about to append becomes the segment's last
		// entry, so the segment attests to its own creation and the next pass
		// finds nothing new. A range capped by MaxEntriesPerSegment stops short
		// of it instead, and a later segment carries it.
		to := head + 1
		if capped := from + int64(x.settings.MaxEntriesPerSegment) - 1; to > capped {
			to = capped
		}

		id := uuid.NewString()
		key := x.objectKey(tenantID, from, to)
		exportedAt := x.now().UTC().Truncate(audit.ChainResolution)
		previous := previousOf(last)
		previousDigest := ""
		if previous != nil {
			previousDigest = previous.Digest
		}

		// How far this range's unrecomputable prefix reaches, measured from the
		// rows themselves under the chain lock. Stating it inside the record is
		// what stops a later entry being relabelled into the cut-over.
		preCutoverThrough, err := x.store.PreCutoverThrough(ctx, tenantID, from, to)
		if err != nil {
			return err
		}

		// The header of the document about to be written, restated inside an
		// entry of the chain — the only place it cannot be edited afterwards.
		// The action and resource are spelled out as literals here on purpose
		// (see the constants above).
		if err := x.audit.Record(ctx, "audit.exported", "tenant", tenantID, RecordDetails{
			SegmentSeq:           segmentSeq,
			SegmentID:            id,
			FromSeq:              from,
			ToSeq:                to,
			Entries:              to - from + 1,
			ObjectKey:            key,
			ExportedAt:           FormatInstant(exportedAt),
			PreviousDigest:       previousDigest,
			PreCutoverThroughSeq: preCutoverThrough,
			FormatVersion:        FormatVersion,
		}.Map()); err != nil {
			return err
		}

		entries, err := x.store.LoadRange(ctx, tenantID, from, to)
		if err != nil {
			return err
		}
		built, err = Build(BuildInput{
			SegmentID:     id,
			SegmentSeq:    segmentSeq,
			TenantID:      tenantID,
			ExportedAt:    exportedAt,
			StartPrevHash: startPrev,
			Entries:       entries,
			Previous:      previous,
		})
		if err != nil {
			return err
		}

		row = Ledger{
			ID:            id,
			TenantID:      tenantID,
			SegmentSeq:    segmentSeq,
			FromSeq:       from,
			ToSeq:         to,
			Entries:       len(entries),
			StartPrevHash: startPrev,
			EndHash:       built.Document.Chain.EndHash,
			ObjectKey:     key,
			FormatVersion: FormatVersion,
			Digest:        built.Digest,
			SizeBytes:     int64(len(built.Bytes)),
			ExportedAt:    exportedAt,
			Status:        StatusPending,
		}
		cut = true
		return x.store.Claim(ctx, row)
	})
	if err != nil {
		return Ledger{}, Built{}, false, err
	}
	return row, built, cut, nil
}

// finish uploads a cut segment and confirms it. built is what cut produced;
// nil means the segment was cut by an earlier pass and must be rebuilt.
func (x *Exporter) finish(ctx context.Context, tenantID string, row Ledger, built *Built) error {
	if built == nil {
		rebuilt, err := x.rebuild(ctx, tenantID, row)
		if err != nil {
			return err
		}
		built = &rebuilt
	}

	if err := x.dest.Put(ctx, row.ObjectKey, bytes.NewReader(built.Bytes),
		int64(len(built.Bytes)), ContentType); err != nil {
		// The row stays PENDING: the range is still owed, and the next pass
		// will rebuild the same bytes and try the same key again. Recording the
		// attempt is best effort — losing the counter must not lose the error.
		if failErr := x.recordFailure(ctx, tenantID, row.ID, err); failErr != nil {
			logIfConfigured(ctx).Warn("Audit export could not record a failed upload",
				logger.String("tenantId", tenantID),
				logger.String("objectKey", row.ObjectKey),
				logger.Error(failErr))
		}
		return fmt.Errorf("worm: upload segment %s: %w", row.ObjectKey, err)
	}

	if err := x.confirm(ctx, tenantID, row.ID); err != nil {
		return err
	}
	logIfConfigured(ctx).Info("Audit chain segment exported",
		logger.String("tenantId", tenantID),
		logger.String("objectKey", row.ObjectKey),
		logger.Int64("fromSeq", row.FromSeq),
		logger.Int64("toSeq", row.ToSeq),
		logger.String("digest", built.Digest),
	)
	return nil
}

// rebuild reconstructs a segment cut by an earlier pass, from the same rows and
// the same stamped instant, and checks that it came out the same.
//
// A byte-identical rebuild is what makes a retry safe. It is also a check: the
// entries a pending segment covers are already fixed in the ledger, so if they
// no longer hash to the digest recorded at the cut, the rows behind them have
// changed — which an append-only ledger forbids. That is reported, not
// papered over by uploading the new bytes.
func (x *Exporter) rebuild(ctx context.Context, tenantID string, row Ledger) (Built, error) {
	var built Built
	err := x.tx.WithinTransaction(x.tenantContext(ctx, tenantID), func(ctx context.Context) error {
		entries, err := x.store.LoadRange(ctx, tenantID, row.FromSeq, row.ToSeq)
		if err != nil {
			return err
		}
		previous, err := x.store.Before(ctx, tenantID, row.SegmentSeq)
		if err != nil {
			return err
		}
		built, err = Build(BuildInput{
			SegmentID:     row.ID,
			SegmentSeq:    row.SegmentSeq,
			TenantID:      tenantID,
			ExportedAt:    row.ExportedAt,
			StartPrevHash: row.StartPrevHash,
			Entries:       entries,
			Previous:      previousOf(previous),
		})
		if err != nil {
			return err
		}

		if row.FormatVersion != FormatVersion {
			// The binary that retries writes a newer document shape than the
			// one that cut the range. The range it attests to is unchanged, so
			// the segment is rewritten and the ledger restated rather than the
			// tenant being stuck behind a file this build cannot produce.
			logIfConfigured(ctx).Warn("Audit export segment rebuilt under a newer document format",
				logger.String("tenantId", tenantID),
				logger.String("objectKey", row.ObjectKey),
				logger.Int("cutAs", row.FormatVersion),
				logger.Int("rebuiltAs", FormatVersion))
			return x.store.Restate(ctx, row.ID, FormatVersion, built.Digest, int64(len(built.Bytes)))
		}
		if built.Digest != row.Digest {
			return fmt.Errorf("worm: segment %s (seq %d..%d) rebuilds to digest %s but was cut as %s — the audit rows behind it have changed",
				row.ObjectKey, row.FromSeq, row.ToSeq, built.Digest, row.Digest)
		}
		return nil
	})
	return built, err
}

func (x *Exporter) pendingOf(ctx context.Context, tenantID string) (*Ledger, error) {
	var row *Ledger
	err := x.tx.WithinTransaction(x.tenantContext(ctx, tenantID), func(ctx context.Context) error {
		var err error
		row, err = x.store.Pending(ctx, tenantID)
		return err
	})
	return row, err
}

func (x *Exporter) confirm(ctx context.Context, tenantID, id string) error {
	return x.tx.WithinTransaction(x.tenantContext(ctx, tenantID), func(ctx context.Context) error {
		return x.store.Confirm(ctx, id)
	})
}

// recordFailure runs on a FRESH context: the upload's own context may already
// be cancelled (a shutdown, a timeout), and the attempt still has to be counted.
func (x *Exporter) recordFailure(ctx context.Context, tenantID, id string, reason error) error {
	failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failureRecordTimeout)
	defer cancel()
	return x.tx.WithinTransaction(x.tenantContext(failCtx, tenantID), func(ctx context.Context) error {
		return x.store.Fail(ctx, id, reason)
	})
}

const failureRecordTimeout = 5 * time.Second

// tenantContext binds the tenant (so RLS lets the reads, the append and the
// ledger write through) and the system actor (so the audit entry is attributed
// by id, per ADR-0008).
func (x *Exporter) tenantContext(ctx context.Context, tenantID string) context.Context {
	ctx = app.WithTenantID(ctx, tenantID)
	return app.WithRequester(ctx, &app.Requester{Kind: app.RequesterUser, ID: x.actor})
}

// objectKey is derived entirely from the tenant and the range, so a retry
// cannot land the same entries under a second key. Zero-padded, so a plain
// lexicographic listing of the prefix is the series in order.
func (x *Exporter) objectKey(tenantID string, from, to int64) string {
	return fmt.Sprintf("%s/%s/audit-segments/seg-%012d-%012d.json", x.settings.KeyPrefix, tenantID, from, to)
}

// previousOf renders a ledger row as the back-pointer the next segment carries.
func previousOf(row *Ledger) *Previous {
	if row == nil {
		return nil
	}
	return &Previous{
		SegmentID:  row.ID,
		SegmentSeq: row.SegmentSeq,
		ObjectKey:  row.ObjectKey,
		ToSeq:      row.ToSeq,
		EndHash:    row.EndHash,
		Digest:     digestPrefix + row.Digest,
	}
}

var loggerInit sync.Once

// logIfConfigured returns the process logger, initialising a default one if
// nothing has. A job must not panic on its first log line.
func logIfConfigured(ctx context.Context) logger.Logger {
	loggerInit.Do(func() {
		if logger.Log == nil {
			logger.InitBasic()
		}
	})
	return logger.Log.WithContext(ctx)
}
