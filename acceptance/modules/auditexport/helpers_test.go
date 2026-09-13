package auditexport_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/audit/worm"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/storage"
	storagefs "github.com/mohamadhallal/zentax-api/platform/storage/fs"
)

// AuditExportSuite proves the WORM export against live Postgres and the real
// filesystem storage adapter — the two things a unit test cannot stand in for.
//
// The claims under test are the ones an auditor would press on:
//
//   - a segment written to the store verifies OFFLINE, from its bytes alone,
//     with no database and no session;
//   - one altered byte in that file is caught;
//   - the ordinary operational accidents — the first export, a quiet tenant, a
//     crash between the range being cut and the object landing, an upload that
//     fails outright — never lose an entry, never duplicate one, and never
//     leave the next pass believing it already ran.
//
// Everything reads audit_log and audit_export_segments through the tenant
// policy (the suite connects as the non-BYPASSRLS app role), exactly as the
// application does.
type AuditExportSuite struct {
	acceptance.Suite

	// exportRoot is this test's evidence store: a temp directory the
	// filesystem adapter is rooted at, removed after the test.
	exportRoot string
	dest       *storagefs.Storage
}

func TestAuditExportSuite(t *testing.T) {
	suite.Run(t, new(AuditExportSuite))
}

func (s *AuditExportSuite) SetupTest() {
	s.Suite.SetupTest()
	root, err := os.MkdirTemp(os.TempDir(), "zentax-acceptance-worm-*")
	s.Require().NoError(err)
	s.exportRoot = root
	s.dest, err = storagefs.New(root)
	s.Require().NoError(err)
}

func (s *AuditExportSuite) TearDownTest() {
	if s.exportRoot != "" {
		_ = os.RemoveAll(s.exportRoot)
	}
	s.exportRoot = ""
	s.dest = nil
	s.Suite.TearDownTest()
}

// exportInstant is when every export below is stamped. Fixed, because the
// instant is inside the hashed document and the rebuild-must-be-identical
// claim is about determinism, not about when the suite happened to run.
var exportInstant = time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)

// exporter builds the job exactly as bootstrap does, on the suite's pool,
// against the given destination.
func (s *AuditExportSuite) exporter(dest storage.Storage, settings worm.Settings) *worm.Exporter {
	db := database.NewExec(s.DB)
	return worm.NewExporter(db, worm.NewStore(db), dest, audit.NewRecorder(db), settings).
		WithClock(func() time.Time { return exportInstant })
}

// run executes one pass against the suite's filesystem destination.
func (s *AuditExportSuite) run(settings worm.Settings) worm.RunReport {
	report, err := s.exporter(s.dest, settings).Run(context.Background())
	s.Require().NoError(err)
	return report
}

// seedActor inserts a human member to attribute the seeded entries to; the
// trail records actors by id, never by name.
func (s *AuditExportSuite) seedActor(tenantID string) string {
	var id string
	s.Require().NoError(s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status, kind)
		 VALUES ($1, $2, 'Export Actor', 'active', 'human') RETURNING id`,
		tenantID, "worm-"+uuid.NewString()+"@test.local").Scan(&id))
	return id
}

// appendEntries writes n ordinary domain audit entries for a tenant, through
// the real recorder on real transactions — so the chain the export reads is a
// chain the application wrote.
func (s *AuditExportSuite) appendEntries(tenantID, actorID string, n int) {
	exec := database.NewExec(s.DB)
	recorder := audit.NewRecorder(exec)
	for i := 0; i < n; i++ {
		ctx := app.WithTenantID(context.Background(), tenantID)
		ctx = app.WithRequester(ctx, &app.Requester{Kind: app.RequesterUser, ID: actorID})
		// A canonical request id: the envelope stores only that shape, so an
		// ad-hoc string would be dropped and hash as empty.
		ctx = app.WithRequestId(ctx, uuid.NewString())
		resource := uuid.NewString()
		s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
			return recorder.Record(ctx, "entity.updated", "entity", resource, map[string]any{
				"fields": map[string]any{"name": map[string]any{"from": "set", "to": "set"}},
			})
		}))
	}
}

// chainHead is the tenant's highest audit_log.seq.
func (s *AuditExportSuite) chainHead(tenantID string) int64 {
	var head int64
	s.withTenant(tenantID, func(ctx context.Context, exec *database.Exec) {
		s.Require().NoError(exec.GetContext(ctx, &head,
			`SELECT COALESCE(MAX(seq), 0) FROM audit_log WHERE tenant_id = $1`, tenantID))
	})
	return head
}

// exportEntries returns the tenant's audit.exported records, oldest first.
func (s *AuditExportSuite) exportEntries(tenantID string) []audit.Entry {
	var entries []audit.Entry
	s.withTenant(tenantID, func(ctx context.Context, exec *database.Exec) {
		s.Require().NoError(exec.SelectContext(ctx, &entries, `
			SELECT `+audit.ChainColumns+`
			  FROM audit_log
			 WHERE tenant_id = $1 AND action = $2
			 ORDER BY seq ASC`, tenantID, worm.AuditAction))
	})
	return entries
}

// ledgerRow is one audit_export_segments row as these tests read it.
type ledgerRow struct {
	SegmentSeq int64  `db:"segment_seq"`
	FromSeq    int64  `db:"from_seq"`
	ToSeq      int64  `db:"to_seq"`
	Entries    int    `db:"entries"`
	StartPrev  string `db:"start_prev_hash"`
	EndHash    string `db:"end_hash"`
	ObjectKey  string `db:"object_key"`
	Digest     string `db:"digest"`
	SizeBytes  int64  `db:"size_bytes"`
	Status     string `db:"status"`
	Attempts   int    `db:"attempts"`
}

// ledger returns a tenant's export ledger, oldest segment first.
func (s *AuditExportSuite) ledger(tenantID string) []ledgerRow {
	var rows []ledgerRow
	s.withTenant(tenantID, func(ctx context.Context, exec *database.Exec) {
		s.Require().NoError(exec.SelectContext(ctx, &rows, `
			SELECT segment_seq, from_seq, to_seq, entries, start_prev_hash, end_hash,
			       object_key, digest, size_bytes, status, attempts
			  FROM audit_export_segments
			 WHERE tenant_id = $1
			 ORDER BY segment_seq ASC`, tenantID))
	})
	return rows
}

// withTenant runs fn on a transaction bound to the tenant, mirroring the
// application's Tx seam: RLS reads app.tenant_id from a SET LOCAL, so without
// one the policies fail closed and these reads would see nothing.
func (s *AuditExportSuite) withTenant(tenantID string, fn func(ctx context.Context, exec *database.Exec)) {
	exec := database.NewExec(s.DB)
	ctx := app.WithTenantID(context.Background(), tenantID)
	s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
		fn(ctx, exec)
		return nil
	}))
}

// segmentPath is where the filesystem adapter put an object. Reading the file
// straight off the disk — rather than through the adapter — is the point: an
// auditor has the bytes, nothing else.
func (s *AuditExportSuite) segmentPath(objectKey string) string {
	return filepath.Join(s.exportRoot, filepath.FromSlash(objectKey))
}

// readSegment reads a stored segment's bytes.
func (s *AuditExportSuite) readSegment(objectKey string) []byte {
	data, err := os.ReadFile(s.segmentPath(objectKey))
	s.Require().NoError(err)
	return data
}

// verifyOffline is what cmd/verify-audit-segment does to one file. The report
// carries the document AND the digest of the bytes it was read from, which is
// what worm.VerifySeries needs: a segment names the digest of the file before
// it, and a document lifted out of its file no longer knows what those bytes
// hashed to.
func (s *AuditExportSuite) verifyOffline(objectKey string) (worm.Report, worm.Document) {
	data := s.readSegment(objectKey)
	report, err := worm.VerifyFile(data)
	s.Require().NoError(err, "a segment this product wrote must verify offline")
	return report, report.Document
}

// reseal reads a stored segment, edits its document, and writes the file back
// out with its digest recomputed — an attacker who has read the published
// verification block and knows exactly how the file is checked. It returns the
// bytes; the object in the store is untouched.
func (s *AuditExportSuite) reseal(objectKey string, edit func(doc *worm.Document)) []byte {
	doc := s.documentOf(s.readSegment(objectKey))
	edit(&doc)
	body, err := json.Marshal(doc)
	s.Require().NoError(err)
	sum := sha256.Sum256(body)
	out, err := json.Marshal(worm.File{Segment: body, Digest: "sha256:" + hex.EncodeToString(sum[:])})
	s.Require().NoError(err)
	return out
}

// digestOf is the digest a reader computes over a file's segment member.
func (s *AuditExportSuite) digestOf(data []byte) string {
	var f worm.File
	s.Require().NoError(json.Unmarshal(data, &f))
	sum := sha256.Sum256(f.Segment)
	return hex.EncodeToString(sum[:])
}

func (s *AuditExportSuite) documentOf(data []byte) worm.Document {
	var f worm.File
	s.Require().NoError(json.Unmarshal(data, &f))
	var doc worm.Document
	s.Require().NoError(json.Unmarshal(f.Segment, &doc))
	return doc
}

// detailsOf decodes an audit entry's payload.
func (s *AuditExportSuite) detailsOf(e audit.Entry) map[string]any {
	var out map[string]any
	s.Require().NoError(json.Unmarshal(e.Details, &out))
	return out
}

// --- destinations that misbehave -------------------------------------------

var errStoreDown = errors.New("object store is unreachable")

// refusingStore is an object store that accepts nothing: the upload-failure
// case.
type refusingStore struct{}

func (refusingStore) Put(context.Context, string, io.Reader, int64, string) error {
	return errStoreDown
}

func (refusingStore) Get(context.Context, string) (io.ReadCloser, storage.ObjectInfo, error) {
	return nil, storage.ObjectInfo{}, storage.ErrNotFound
}

func (refusingStore) Delete(context.Context, string) error { return nil }

// crashingStore writes the object and THEN fails, which is the crash the
// ledger cannot distinguish from an upload that never happened: the evidence is
// in the store, and nothing in the database knows it.
type crashingStore struct {
	inner storage.Storage
}

func (c crashingStore) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := c.inner.Put(ctx, key, r, size, contentType); err != nil {
		return err
	}
	return errStoreDown
}

func (c crashingStore) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	return c.inner.Get(ctx, key)
}

func (c crashingStore) Delete(ctx context.Context, key string) error { return c.inner.Delete(ctx, key) }
