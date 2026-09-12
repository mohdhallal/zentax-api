package audit

import (
	"bytes"
	"encoding/json"
)

// Presence tokens: the only thing the trail says about a redacted field.
const (
	presenceSet   = "set"
	presenceEmpty = "empty"
)

// Values is one resource's whitelisted field values at a point in time — the
// per-resource answer to "what would an auditor ask about?", assembled by the
// use case that mutates the resource.
//
// It is deliberately NOT a row dump. A column reaches the trail only because
// someone decided an auditor should see it, which is what keeps the ADR-0008
// envelope free of personal data and free text as the schema grows: a new
// column is invisible here until a human adds it, and the reviewer of that
// line is the PII gate.
type Values map[string]any

// Redacted is a value whose CHANGE belongs in the trail but whose CONTENT does
// not: a company or person's name, a tax reference number (ADR-0006 names it
// the field to encrypt), any free text a user typed. Changes compares the
// wrapped value — so the change is still detected and dated — and records only
// whether each side was set. Build one with Redact.
type Redacted struct{ value any }

// Redact marks a whitelisted field as recordable but not quotable.
func Redact(v any) Redacted { return Redacted{value: v} }

// Changes is the details payload for a create, an update or a delete: the
// whitelisted fields that moved, each carrying its before and after value.
//
// It generalizes the shape the approval chain already writes — {"from": ...,
// "to": ...} for one status (see the taskinstances submit/approve/reject use
// cases) — to N fields:
//
//	{"fields": {"status":       {"from": "active", "to": "inactive"},
//	            "deadlineRule": {"from": {…},      "to": {…}}}}
//
// Pass before == nil for a create (each field carries "to" only) and after ==
// nil for a delete ("from" only); in those one-sided forms a field whose value
// is empty (null, "", {}, []) is left out rather than recorded as an absence.
// A redacted field records the presence tokens "set" / "empty" in place of its
// value.
//
// A field appears only when it actually changed, so the payload's key set IS
// the change set. Returns nil when nothing whitelisted moved, which Record
// stores as {} — the honest envelope for "the row was rewritten with the same
// values".
//
// Values must be JSON-encodable (they are the stored evidence); an encoding
// failure surfaces from Record and fails the enclosing transaction, so a
// mutation never commits with a payload that cannot be read back.
func Changes(before, after Values) map[string]any {
	names := make(map[string]struct{}, len(before)+len(after))
	for name := range before {
		names[name] = struct{}{}
	}
	for name := range after {
		names[name] = struct{}{}
	}

	fields := make(map[string]any, len(names))
	for name := range names {
		raw, inBefore := before[name]
		b, bRedacted := unwrap(raw)
		raw, inAfter := after[name]
		a, aRedacted := unwrap(raw)
		redacted := bRedacted || aRedacted

		bj, aj := canonical(b), canonical(a)
		switch {
		case inBefore && inAfter:
			if bytes.Equal(bj, aj) {
				continue // unchanged — not evidence of anything
			}
			fields[name] = map[string]any{
				"from": recorded(b, bj, redacted),
				"to":   recorded(a, aj, redacted),
			}
		case inAfter: // create
			if isEmptyJSON(aj) {
				continue
			}
			fields[name] = map[string]any{"to": recorded(a, aj, redacted)}
		case inBefore: // delete
			if isEmptyJSON(bj) {
				continue
			}
			fields[name] = map[string]any{"from": recorded(b, bj, redacted)}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return map[string]any{"fields": fields}
}

// unwrap yields the comparable value behind a whitelist entry and whether the
// call site marked it redacted.
func unwrap(v any) (any, bool) {
	if r, ok := v.(Redacted); ok {
		return r.value, true
	}
	return v, false
}

// recorded is what actually lands in the envelope: the value itself, or a bare
// presence token when the field is redacted.
func recorded(v any, encoded []byte, redacted bool) any {
	if !redacted {
		return v
	}
	if isEmptyJSON(encoded) {
		return presenceEmpty
	}
	return presenceSet
}

// canonical encodes a value for comparison only. Encoding is deterministic
// (encoding/json sorts map keys), so equal values compare equal regardless of
// pointer identity. A value that cannot be encoded compares as null here and
// fails loudly later, in Record.
func canonical(v any) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return out
}

// isEmptyJSON reports whether an encoded value carries nothing: a nil pointer,
// an empty string, or an empty object / array (every JSONB rule type in the
// domain encodes its zero value as {} or []).
func isEmptyJSON(encoded []byte) bool {
	switch string(encoded) {
	case "null", `""`, "{}", "[]":
		return true
	}
	return false
}
