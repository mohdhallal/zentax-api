package audit

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// rule stands in for a domain JSONB rule object (a deadline rule, a due-date
// rule): a struct with omitempty tags whose zero value encodes as {}.
type rule struct {
	Reference   string `json:"reference,omitempty"`
	OffsetValue int    `json:"offsetValue,omitempty"`
}

// entryOf renders a payload the way Record does, so tests assert on the stored
// JSON rather than on Go maps.
func entryOf(t *testing.T, details map[string]any) map[string]any {
	t.Helper()
	raw, err := canonicalDetails(details)
	if err != nil {
		t.Fatalf("payload must encode: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("payload must decode: %v", err)
	}
	return out
}

func fieldsOf(t *testing.T, details map[string]any) map[string]any {
	t.Helper()
	stored := entryOf(t, details)
	f, ok := stored["fields"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no fields object: %v", stored)
	}
	return f
}

func TestChanges_UpdateRecordsOnlyWhatMoved(t *testing.T) {
	before := Values{
		"status":       "active",
		"periodicity":  "monthly",
		"deadlineRule": rule{Reference: "period_end", OffsetValue: 15},
	}
	after := Values{
		"status":       "inactive",
		"periodicity":  "monthly", // untouched
		"deadlineRule": rule{Reference: "period_end", OffsetValue: 20},
	}

	fields := fieldsOf(t, Changes(before, after))
	if _, ok := fields["periodicity"]; ok {
		t.Fatal("an unchanged field must not be recorded")
	}
	want := map[string]any{"from": "active", "to": "inactive"}
	if got := fields["status"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("status: got %v, want %v", got, want)
	}
	// The rule lands whole, both sides, so the superseded rule is recoverable.
	got, ok := fields["deadlineRule"].(map[string]any)
	if !ok {
		t.Fatalf("deadlineRule must be an object: %v", fields["deadlineRule"])
	}
	from, _ := got["from"].(map[string]any)
	to, _ := got["to"].(map[string]any)
	if from["offsetValue"] != float64(15) || to["offsetValue"] != float64(20) {
		t.Fatalf("deadlineRule must carry both sides: %v", got)
	}
}

func TestChanges_NothingMovedIsNil(t *testing.T) {
	v := Values{"status": "active", "rule": rule{}}
	if got := Changes(v, Values{"status": "active", "rule": rule{}}); got != nil {
		t.Fatalf("an identical rewrite must record no fields, got %v", got)
	}
}

func TestChanges_CreateRecordsToOnlyAndSkipsEmpties(t *testing.T) {
	var absent *string
	present := "Germany"
	fields := fieldsOf(t, Changes(nil, Values{
		"country":          present,
		"taxResidency":     absent, // nil pointer
		"jurisdiction":     "",     // empty string
		"deadlineRule":     rule{}, // zero rule object
		"selectedPeriods":  []string{},
		"approvalRequired": false, // a meaningful control value, not an absence
	}))

	for _, empty := range []string{"taxResidency", "jurisdiction", "deadlineRule", "selectedPeriods"} {
		if _, ok := fields[empty]; ok {
			t.Fatalf("%s was empty at creation and must not be recorded", empty)
		}
	}
	if got := fields["country"]; !reflect.DeepEqual(got, map[string]any{"to": "Germany"}) {
		t.Fatalf("country: got %v", got)
	}
	if got := fields["approvalRequired"]; !reflect.DeepEqual(got, map[string]any{"to": false}) {
		t.Fatalf("approvalRequired: got %v", got)
	}
}

func TestChanges_DeleteRecordsFromOnly(t *testing.T) {
	fields := fieldsOf(t, Changes(Values{
		"status":       "active",
		"deadlineRule": rule{Reference: "period_end", OffsetValue: 15},
	}, nil))

	if got := fields["status"]; !reflect.DeepEqual(got, map[string]any{"from": "active"}) {
		t.Fatalf("status: got %v", got)
	}
	from, ok := fields["deadlineRule"].(map[string]any)["from"].(map[string]any)
	if !ok || from["reference"] != "period_end" {
		t.Fatalf("the rule that is disappearing must be recorded: %v", fields["deadlineRule"])
	}
}

func TestChanges_RedactedRecordsPresenceNeverValue(t *testing.T) {
	oldRef := "DE123456789"
	newRef := "DE987654321"
	fields := fieldsOf(t, Changes(
		Values{"name": Redact("Acme GmbH"), "taxReferenceNumber": Redact(&oldRef)},
		Values{"name": Redact("Acme Holding GmbH"), "taxReferenceNumber": Redact(&newRef)},
	))

	want := map[string]any{"from": presenceSet, "to": presenceSet}
	for _, name := range []string{"name", "taxReferenceNumber"} {
		if got := fields[name]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Acme", "DE123456789", "DE987654321"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("redacted value %q leaked into the envelope: %s", secret, encoded)
		}
	}
}

func TestChanges_RedactedRecordsClearingAndSetting(t *testing.T) {
	ref := "DE123456789"
	var none *string

	cleared := fieldsOf(t, Changes(Values{"taxReferenceNumber": Redact(&ref)},
		Values{"taxReferenceNumber": Redact(none)}))
	if got := cleared["taxReferenceNumber"]; !reflect.DeepEqual(got,
		map[string]any{"from": presenceSet, "to": presenceEmpty}) {
		t.Fatalf("clearing: got %v", got)
	}

	set := fieldsOf(t, Changes(Values{"taxReferenceNumber": Redact(none)},
		Values{"taxReferenceNumber": Redact(&ref)}))
	if got := set["taxReferenceNumber"]; !reflect.DeepEqual(got,
		map[string]any{"from": presenceEmpty, "to": presenceSet}) {
		t.Fatalf("setting: got %v", got)
	}

	if got := Changes(Values{"name": Redact("Acme")}, Values{"name": Redact("Acme")}); got != nil {
		t.Fatalf("an unchanged redacted field must record nothing, got %v", got)
	}
}

// A redacted field that was empty on both sides of a create records nothing.
func TestChanges_RedactedEmptyOnCreateIsSkipped(t *testing.T) {
	var none *string
	if got := Changes(nil, Values{"legalName": Redact(none)}); got != nil {
		t.Fatalf("an empty redacted field must not be recorded on create, got %v", got)
	}
}

// The hash chain must survive the richer envelopes: a payload with nested
// objects hashes identically on the write side and after Postgres has
// normalized the jsonb (key order and whitespace differ on the way back).
func TestChanges_HashIsStableAcrossJSONBNormalization(t *testing.T) {
	details := Changes(
		Values{"status": "active", "deadlineRule": rule{Reference: "period_end", OffsetValue: 15}},
		Values{"status": "inactive", "deadlineRule": rule{Reference: "filing_deadline", OffsetValue: 20}},
	)
	written, err := canonicalDetails(details)
	if err != nil {
		t.Fatal(err)
	}

	e := Entry{
		EventID:      "11111111-1111-1111-1111-111111111111",
		TenantID:     "22222222-2222-2222-2222-222222222222",
		Seq:          1,
		ActorID:      "33333333-3333-3333-3333-333333333333",
		Action:       "entity_obligation.updated",
		ResourceType: "entity_obligation",
		ResourceID:   "44444444-4444-4444-4444-444444444444",
		OccurredAt:   time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
		RequestID:    "req-1",
		Details:      written,
		PrevHash:     GenesisHash,
	}
	e.Hash = ComputeHash(&e)

	// Same payload, re-serialized with reversed key order and whitespace —
	// what a jsonb round trip can hand back.
	stored := e
	stored.Details = json.RawMessage(`{ "fields": {
	    "status": {"to": "inactive", "from": "active"},
	    "deadlineRule": {"to":   {"offsetValue": 20, "reference": "filing_deadline"},
	                     "from": {"offsetValue": 15, "reference": "period_end"}}
	} }`)
	if got := ComputeHash(&stored); got != e.Hash {
		t.Fatalf("hash must not depend on jsonb formatting:\n write %s\n store %s", e.Hash, got)
	}
	if err := VerifyChain([]Entry{stored}); err != nil {
		t.Fatalf("chain must verify across a rich envelope: %v", err)
	}
}
