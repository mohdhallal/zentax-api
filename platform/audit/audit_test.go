package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func chainOf(n int) []Entry {
	prev := GenesisHash
	entries := make([]Entry, 0, n)
	at := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		e := Entry{
			EventID:      "evt-" + string(rune('a'+i)),
			TenantID:     "11111111-1111-1111-1111-111111111111",
			Seq:          int64(i),
			ActorID:      "22222222-2222-2222-2222-222222222222",
			Action:       "entity.created",
			ResourceType: "entity",
			ResourceID:   "33333333-3333-3333-3333-333333333333",
			OccurredAt:   at.Add(time.Duration(i) * time.Second),
			RequestID:    "req-1",
			Details:      json.RawMessage(`{}`),
			PrevHash:     prev,
		}
		e.Hash = ComputeHash(&e)
		prev = e.Hash
		entries = append(entries, e)
	}
	return entries
}

func TestVerifyChain_IntactChainPasses(t *testing.T) {
	if err := VerifyChain(chainOf(5)); err != nil {
		t.Fatalf("intact chain must verify: %v", err)
	}
	if err := VerifyChain(nil); err != nil {
		t.Fatalf("empty chain must verify: %v", err)
	}
}

func TestVerifyChain_DetectsModifiedEntry(t *testing.T) {
	entries := chainOf(5)
	entries[2].Action = "entity.deleted" // tamper without recomputing the hash
	err := VerifyChain(entries)
	if err == nil || !strings.Contains(err.Error(), "seq 3") {
		t.Fatalf("modification at seq 3 must be detected, got: %v", err)
	}
}

func TestVerifyChain_DetectsDeletedEntry(t *testing.T) {
	entries := chainOf(5)
	entries = append(entries[:1], entries[2:]...) // drop seq 2
	if err := VerifyChain(entries); err == nil {
		t.Fatal("a deleted entry must break the chain")
	}
}

func TestComputeHash_DetailsCanonicalization(t *testing.T) {
	e := Entry{
		EventID: "e", TenantID: "t", Seq: 1, ActorID: "a", Action: "x",
		ResourceType: "r", ResourceID: "i",
		OccurredAt: time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC),
		PrevHash:   GenesisHash,
	}
	// Same logical object, different formatting/key order (as jsonb might
	// return it) must hash identically.
	e.Details = json.RawMessage(`{"b": 2, "a": 1}`)
	h1 := ComputeHash(&e)
	e.Details = json.RawMessage(`{ "a":1,"b":2 }`)
	h2 := ComputeHash(&e)
	if h1 != h2 {
		t.Fatal("details canonicalization must make formatting/key order irrelevant")
	}
}

func TestNilRecorderIsNoOp(t *testing.T) {
	var r *Recorder
	if err := r.Record(t.Context(), "x", "y", "z", nil); err != nil {
		t.Fatalf("nil recorder must no-op: %v", err)
	}
}
