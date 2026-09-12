package dto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
)

func entry() *domain.Entry {
	return &domain.Entry{
		ID:           "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		Seq:          440,
		Action:       "member.role_changed",
		ResourceType: "user",
		ResourceID:   "9ae6d1df-af25-47e5-acb8-54e530351dff",
		ActorID:      "cdc3ee46-deeb-4764-a379-c12f48d9abd4",
		OccurredAt:   time.Date(2026, 9, 12, 20, 20, 37, 626_000_000, time.UTC),
		RequestID:    "4144d7de-dc1d-4e46-832e-9033bffb56a4",
		Hash:         "10c7ad604b937cf824440ad85597d8f20d7b885f6f9cfa478a50b137ed9c2386",
	}
}

// The three fields a reader ties a row to the record and to the chain with —
// the resource it describes, its place in the ledger, its own digest — are all
// on the wire, whole. The interface renders and exports them (zentax-ui
// client/src/pages/audit-trail.tsx).
func TestEntryToJSON_CarriesTheRecordAndTheChain(t *testing.T) {
	out := EntryToJSON(entry())

	if out["resourceId"] != "9ae6d1df-af25-47e5-acb8-54e530351dff" {
		t.Fatalf("the entry must name the record it describes, got %v", out["resourceId"])
	}
	if out["seq"] != int64(440) {
		t.Fatalf("an unbounded reader keeps the real chain sequence, got %v", out["seq"])
	}
	if h, _ := out["hash"].(string); len(h) != 64 {
		t.Fatalf("the hash goes out whole (a prefix cannot be matched against the chain), got %q", h)
	}
	if out["details"] == nil {
		t.Fatal("details must never be absent; an empty envelope is {}")
	}
}

// A narrowed reader's entry (repositories/pg zeroes its Seq) carries no `seq`
// KEY at all: sending 0 would put a number in the column that looks like a
// chain position and is not one.
func TestEntryToJSON_OmitsAWithheldSequence(t *testing.T) {
	e := entry()
	e.Seq = 0
	out := EntryToJSON(e)

	if _, present := out["seq"]; present {
		t.Fatalf("a withheld sequence must be an ABSENT key, got %v", out["seq"])
	}
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"seq"`) {
		t.Fatalf("the rendered body still mentions seq: %s", body)
	}
	// Everything else the reader is entitled to survives: neither the record id
	// nor the hash counts what was withheld.
	if out["resourceId"] == nil || out["hash"] == nil {
		t.Fatalf("only the sequence is withheld, got %v", out)
	}
}
