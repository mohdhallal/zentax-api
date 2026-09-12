package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/app"
)

// stubExecer is the minimum database seam Record touches: the advisory lock,
// the chain-head read, and the append. It keeps the appended rows as the
// arguments that were actually sent to Postgres, which is what the assertions
// below are about — the in-memory Entry is not evidence of anything, the
// INSERT's parameters are.
type stubExecer struct {
	appended [][]any
	head     *Entry
}

func (s *stubExecer) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT INTO audit_log") {
		s.appended = append(s.appended, args)
	}
	return nil, nil
}

func (s *stubExecer) GetContext(_ context.Context, dest any, _ string, _ ...any) error {
	if s.head == nil {
		return sql.ErrNoRows
	}
	// Record reads into an anonymous struct, so reach the fields by name.
	v := reflect.ValueOf(dest).Elem()
	v.FieldByName("Seq").SetInt(s.head.Seq)
	v.FieldByName("Hash").SetString(s.head.Hash)
	return nil
}

func (s *stubExecer) SelectContext(context.Context, any, string, ...any) error { panic("unused") }
func (s *stubExecer) QueryRowxContext(context.Context, string, ...any) *sqlx.Row {
	panic("unused")
}
func (s *stubExecer) QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error) {
	panic("unused")
}
func (s *stubExecer) Rebind(q string) string { return q }

// appendedEntry rebuilds the entry Postgres was asked to store, in the column
// order of Record's INSERT.
func appendedEntry(t *testing.T, args []any) Entry {
	t.Helper()
	if len(args) != 13 {
		t.Fatalf("INSERT carries %d parameters, want 13 — the column list changed", len(args))
	}
	return Entry{
		EventID:      args[0].(string),
		TenantID:     args[1].(string),
		Seq:          args[2].(int64),
		ActorID:      args[3].(string),
		Action:       args[4].(string),
		ResourceType: args[5].(string),
		ResourceID:   args[6].(string),
		OccurredAt:   args[7].(time.Time),
		RequestID:    args[8].(string),
		Details:      json.RawMessage(args[9].(string)),
		PrevHash:     args[10].(string),
		Hash:         args[11].(string),
		HashVersion:  args[12].(int16),
	}
}

func recordingContext() context.Context {
	ctx := app.WithTenantID(context.Background(), "11111111-1111-1111-1111-111111111111")
	ctx = app.WithRequester(ctx, &app.Requester{Kind: app.RequesterUser, ID: "22222222-2222-2222-2222-222222222222"})
	return app.WithRequestId(ctx, "req-chain")
}

// nanoClock returns a clock whose instants carry nanoseconds the column cannot
// store — i.e. a Linux clock, which is where the product actually runs. On a
// microsecond-granular host (macOS) the real clock hides this class of bug
// entirely, which is why it survived until now.
func nanoClock(base time.Time) func() time.Time {
	calls := 0
	return func() time.Time {
		calls++
		return base.Add(time.Duration(calls)*time.Millisecond + time.Duration(100*calls+7)*time.Nanosecond)
	}
}

// The load-bearing unit test: what Record sends to Postgres must be an instant
// Postgres can keep, and its hash must survive the column's rounding.
func TestRecordStampsAtChainResolutionAndSurvivesStorage(t *testing.T) {
	db := &stubExecer{}
	rec := NewRecorderWithClock(db, nanoClock(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)))
	ctx := recordingContext()

	var chain []Entry
	for i := 0; i < 3; i++ {
		if err := rec.Record(ctx, "entity.updated", "entity",
			"33333333-3333-3333-3333-333333333333", map[string]any{"n": i}); err != nil {
			t.Fatalf("record: %v", err)
		}
		e := appendedEntry(t, db.appended[i])
		db.head = &e // the next append links to this one, as the real read does

		if e.HashVersion != CurrentHashVersion {
			t.Fatalf("seq %d written as hash format v%d, want v%d", e.Seq, e.HashVersion, CurrentHashVersion)
		}
		if rem := e.OccurredAt.Sub(e.OccurredAt.Truncate(ChainResolution)); rem != 0 {
			t.Fatalf("seq %d stamped %s — %v finer than the column stores, so the hash covers digits that will be lost",
				e.Seq, e.OccurredAt.Format(time.RFC3339Nano), rem)
		}

		// What Postgres gives back: TIMESTAMPTZ keeps microseconds and drops
		// the rest. The hash must recompute from THAT row.
		stored := e
		stored.OccurredAt = e.OccurredAt.Truncate(time.Microsecond)
		chain = append(chain, stored)
	}

	rep, err := Verify(chain)
	if err != nil {
		t.Fatalf("a chain read back at the column's resolution must verify: %v", err)
	}
	if rep.FirstVerifiableSeq != 1 || rep.LastVerifiedSeq != 3 || rep.PreCutoverEntries != 0 {
		t.Fatalf("unexpected report: entries=%d preCutover=%d first=%d last=%d",
			rep.Entries, rep.PreCutoverEntries, rep.FirstVerifiableSeq, rep.LastVerifiedSeq)
	}
}

// Defence in depth: even if some future writer stamps a finer clock, the
// canonical form must not depend on digits the column cannot store.
func TestComputeHashIgnoresSubMicrosecondDigits(t *testing.T) {
	base := Entry{
		EventID: "e", TenantID: "t", Seq: 1, ActorID: "a", Action: "entity.created",
		ResourceType: "entity", ResourceID: "i", RequestID: "r",
		Details: json.RawMessage(`{}`), PrevHash: GenesisHash, HashVersion: CurrentHashVersion,
		OccurredAt: time.Date(2026, 9, 12, 10, 0, 0, 123456000, time.UTC),
	}
	withNanos := base
	withNanos.OccurredAt = time.Date(2026, 9, 12, 10, 0, 0, 123456789, time.UTC)

	if ComputeHash(&base) != ComputeHash(&withNanos) {
		t.Fatal("the hash must not cover sub-microsecond digits: audit_log.occurred_at cannot store them")
	}

	// It must still cover the microseconds it does store.
	nextMicro := base
	nextMicro.OccurredAt = time.Date(2026, 9, 12, 10, 0, 0, 123457000, time.UTC)
	if ComputeHash(&base) == ComputeHash(&nextMicro) {
		t.Fatal("the hash must cover the instant at the column's resolution")
	}
}

// chainFromSeq builds a linked run of entries starting at the given prev_hash,
// each hashed under the version it declares. A v1 entry is built the way the
// pre-cut-over writer built it: hashed over an instant carrying nanoseconds,
// stored truncated — so its hash cannot be recomputed from the stored row.
func chainFromSeq(startSeq int64, prev string, versions ...int16) []Entry {
	entries := make([]Entry, 0, len(versions))
	at := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	for i, version := range versions {
		e := Entry{
			EventID:      "evt-" + strings.Repeat("x", i+1),
			TenantID:     "11111111-1111-1111-1111-111111111111",
			Seq:          startSeq + int64(i),
			ActorID:      "22222222-2222-2222-2222-222222222222",
			Action:       "entity.updated",
			ResourceType: "entity",
			ResourceID:   "33333333-3333-3333-3333-333333333333",
			OccurredAt:   at.Add(time.Duration(i) * time.Second),
			RequestID:    "req-1",
			Details:      json.RawMessage(`{}`),
			PrevHash:     prev,
			HashVersion:  version,
		}
		if version > 0 && version < CurrentHashVersion {
			nano := e
			nano.OccurredAt = e.OccurredAt.Add(217 * time.Nanosecond)
			e.Hash = legacyHash(&nano)
		} else {
			e.Hash = ComputeHash(&e)
		}
		prev = e.Hash
		entries = append(entries, e)
	}
	return entries
}

// legacyHash is the pre-cut-over ComputeHash, verbatim: the instant in
// RFC3339Nano, so hashing an instant that carries nanoseconds produces a hash
// nothing can recompute from the microsecond value Postgres kept. Kept here,
// in a test, as the record of what the rows before the cut-over look like.
func legacyHash(e *Entry) string {
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

func TestVerifyReportsTheFirstVerifiableSeqAcrossTheCutover(t *testing.T) {
	chain := chainFromSeq(1, GenesisHash, 1, 1, CurrentHashVersion, CurrentHashVersion)

	rep, err := Verify(chain)
	if err != nil {
		t.Fatalf("a pre-cut-over prefix must not read as tampering: %v", err)
	}
	if rep.PreCutoverEntries != 2 || rep.FirstVerifiableSeq != 3 || rep.LastVerifiedSeq != 4 {
		t.Fatalf("unexpected report: entries=%d preCutover=%d first=%d last=%d",
			rep.Entries, rep.PreCutoverEntries, rep.FirstVerifiableSeq, rep.LastVerifiedSeq)
	}

	// The strict form still refuses the range, naming where verification starts.
	err = VerifyChain(chain)
	if err == nil || !strings.Contains(err.Error(), "only from seq 3") {
		t.Fatalf("VerifyChain must refuse a partially verifiable chain, got: %v", err)
	}

	// An all-legacy chain: links intact, nothing recomputable.
	rep, err = Verify(chainFromSeq(1, GenesisHash, 1, 1))
	if err != nil {
		t.Fatalf("all-legacy chain: %v", err)
	}
	if rep.FirstVerifiableSeq != 0 || rep.PreCutoverEntries != 2 {
		t.Fatalf("unexpected report: entries=%d preCutover=%d first=%d last=%d",
			rep.Entries, rep.PreCutoverEntries, rep.FirstVerifiableSeq, rep.LastVerifiedSeq)
	}
	if err := VerifyChain(chainFromSeq(1, GenesisHash, 1, 1)); err == nil ||
		!strings.Contains(err.Error(), "not verifiable") {
		t.Fatalf("VerifyChain must refuse an all-legacy chain, got: %v", err)
	}
}

// The marker may excuse a prefix, never a row inside a live chain — otherwise
// relabelling one row would exempt it from recomputation.
func TestVerifyRefusesAPreCutoverEntryAfterAVerifiableOne(t *testing.T) {
	chain := chainFromSeq(1, GenesisHash, CurrentHashVersion, 1, CurrentHashVersion)
	_, err := Verify(chain)
	if err == nil || !strings.Contains(err.Error(), "seq 2") {
		t.Fatalf("a pre-cut-over entry after a verifiable one must break the chain, got: %v", err)
	}
}

func TestVerifyRefusesAHashFormatItCannotCheck(t *testing.T) {
	chain := chainFromSeq(1, GenesisHash, CurrentHashVersion+1)
	_, err := Verify(chain)
	if err == nil || !strings.Contains(err.Error(), "newer than this verifier") {
		t.Fatalf("a newer hash format must be reported as unverifiable, not verified, got: %v", err)
	}
}

// An entry loaded without hash_version (version 0) must be recomputed, not
// waved through: a verifier that forgets the column must fail loudly.
func TestVerifyTreatsAnUnversionedEntryStrictly(t *testing.T) {
	chain := chainFromSeq(1, GenesisHash, CurrentHashVersion, CurrentHashVersion)
	chain[1].HashVersion = 0
	chain[1].Action = "entity.deleted" // tamper without recomputing
	_, err := Verify(chain)
	if err == nil || !strings.Contains(err.Error(), "seq 2") {
		t.Fatalf("an unversioned entry must be verified strictly, got: %v", err)
	}
}
