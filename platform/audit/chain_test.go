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
	if len(args) != 15 {
		t.Fatalf("INSERT carries %d parameters, want 15 — the column list changed", len(args))
	}
	return Entry{
		EventID:        args[0].(string),
		TenantID:       args[1].(string),
		Seq:            args[2].(int64),
		ActorID:        args[3].(string),
		Action:         args[4].(string),
		ResourceType:   args[5].(string),
		ResourceID:     args[6].(string),
		OccurredAt:     args[7].(time.Time),
		RequestID:      args[8].(string),
		CredentialID:   args[9].(string),
		CredentialKind: args[10].(string),
		Details:        json.RawMessage(args[11].(string)),
		PrevHash:       args[12].(string),
		Hash:           args[13].(string),
		HashVersion:    args[14].(int16),
	}
}

const (
	testSessionID = "44444444-4444-4444-4444-444444444444"
	testRequestID = "55555555-5555-5555-5555-555555555555"
)

func recordingContext() context.Context {
	ctx := app.WithTenantID(context.Background(), "11111111-1111-1111-1111-111111111111")
	ctx = app.WithRequester(ctx, &app.Requester{
		Kind: app.RequesterUser, ID: "22222222-2222-2222-2222-222222222222",
		CredentialID: testSessionID, CredentialKind: app.CredentialSession,
	})
	return app.WithRequestId(ctx, testRequestID)
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
		if IsPreCutover(version) {
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

// THE CUT-OVER RULE THAT MATTERS, and the one a version bump gets wrong: a new
// hashing contract must not turn the rows written under the last one into
// "link-checked only". Version 2 entries are recomputed — under the version 2
// envelope — inside a chain whose newer entries are version 3.
func TestAVersionBumpDoesNotStripOlderEntriesOfTheirVerifiability(t *testing.T) {
	chain := chainFromSeq(1, GenesisHash,
		HashVersionMicrosecond, HashVersionMicrosecond, HashVersionCredential)
	chain[2].CredentialID = testSessionID
	chain[2].CredentialKind = app.CredentialSession
	chain[2].Hash = ComputeHash(&chain[2])

	rep, err := Verify(chain)
	if err != nil {
		t.Fatalf("a chain spanning the credential cut-over must verify on both sides: %v", err)
	}
	if rep.PreCutoverEntries != 0 || rep.FirstVerifiableSeq != 1 || rep.LastVerifiedSeq != 3 {
		t.Fatalf("every entry must be recomputed: entries=%d preCutover=%d first=%d last=%d",
			rep.Entries, rep.PreCutoverEntries, rep.FirstVerifiableSeq, rep.LastVerifiedSeq)
	}
	if err := VerifyChain(chain); err != nil {
		t.Fatalf("the strict form must accept a chain in which every entry recomputes: %v", err)
	}

	// And tampering with a v2 entry is still caught: it is recomputed, not
	// excused for being old.
	chain[1].Action = "entity.deleted"
	if _, err := Verify(chain); err == nil || !strings.Contains(err.Error(), "seq 2") {
		t.Fatalf("a modified version-2 entry must still read as tampering, got: %v", err)
	}
}

// The credential is INSIDE the hash, which is the whole point of putting it on
// the envelope rather than beside it: an operator who rewrites which session
// made a change breaks the chain from that entry onward.
func TestTheCredentialIsCoveredByTheHash(t *testing.T) {
	entry := chainFromSeq(1, GenesisHash, HashVersionCredential)[0]
	entry.CredentialID = testSessionID
	entry.CredentialKind = app.CredentialSession
	entry.Hash = ComputeHash(&entry)

	reattributed := entry
	reattributed.CredentialID = "66666666-6666-6666-6666-666666666666"
	if ComputeHash(&reattributed) == entry.Hash {
		t.Fatal("moving an entry to another session must break its hash")
	}

	rekinded := entry
	rekinded.CredentialKind = app.CredentialAPIToken
	if ComputeHash(&rekinded) == entry.Hash {
		t.Fatal("changing which store the credential belongs to must break its hash")
	}

	// Versions EXTEND: the same entry hashed under version 2 is the version 2
	// canonical form, credential and all left out of it.
	asV2 := entry
	asV2.HashVersion = HashVersionMicrosecond
	bare := asV2
	bare.CredentialID, bare.CredentialKind = "", ""
	if ComputeHash(&asV2) != ComputeHash(&bare) {
		t.Fatal("version 2 must hash exactly what it hashed before the credential existed")
	}
}

// The writer's two gates, asserted on the INSERT parameters rather than on the
// in-memory entry: a request id that is not canonical is not stored (it would
// be caller text inside an append-only, hash-covered, WORM-exported store), and
// half a credential is no credential (a join key pointing at no table).
func TestRecordRefusesCallerTextAndHalfCredentials(t *testing.T) {
	cases := []struct {
		name             string
		requestID        string
		credID, credKind string
		wantRequestID    string
		wantCredID       string
		wantCredKind     string
	}{
		{"canonical", testRequestID, testSessionID, app.CredentialSession, testRequestID, testSessionID, app.CredentialSession},
		{"uppercase request id is canonicalized", strings.ToUpper(testRequestID), "", "", testRequestID, "", ""},
		{"caller text", "'; DROP TABLE audit_log; --", "", "", "", "", ""},
		{"overlong", strings.Repeat("a", 4096), "", "", "", "", ""},
		{"id without kind", testRequestID, testSessionID, "", testRequestID, "", ""},
		{"kind without id", testRequestID, "", app.CredentialSession, testRequestID, "", ""},
		{"unknown kind", testRequestID, testSessionID, "cookie", testRequestID, "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &stubExecer{}
			rec := NewRecorderWithClock(db, nanoClock(time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)))
			ctx := app.WithTenantID(context.Background(), "11111111-1111-1111-1111-111111111111")
			ctx = app.WithRequester(ctx, &app.Requester{
				Kind: app.RequesterUser, ID: "22222222-2222-2222-2222-222222222222",
				CredentialID: tc.credID, CredentialKind: tc.credKind,
			})
			ctx = app.WithRequestId(ctx, tc.requestID)

			if err := rec.Record(ctx, "entity.updated", "entity",
				"33333333-3333-3333-3333-333333333333", nil); err != nil {
				t.Fatalf("record: %v", err)
			}
			e := appendedEntry(t, db.appended[0])
			if e.RequestID != tc.wantRequestID {
				t.Fatalf("stored request id %q, want %q", e.RequestID, tc.wantRequestID)
			}
			if e.CredentialID != tc.wantCredID || e.CredentialKind != tc.wantCredKind {
				t.Fatalf("stored credential %q/%q, want %q/%q",
					e.CredentialKind, e.CredentialID, tc.wantCredKind, tc.wantCredID)
			}
			if got := ComputeHash(&e); got != e.Hash {
				t.Fatal("the stored row must hash to the stored hash")
			}
		})
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
