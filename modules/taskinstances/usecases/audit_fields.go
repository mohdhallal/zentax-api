package usecases

import (
	"encoding/json"
	"sort"

	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for a task instance — the object that
// carries the actual work: who is doing it, by when, and what figures it holds.
// It feeds audit.Changes, which records the before and after of whatever moved.
//
// The whitelist is deliberately the EDITABLE subset (domain.UpdateTaskInstance-
// Input) and no more. periodEndDate, filingDeadline and paymentDeadline are the
// legal dates an auditor asks about, but no request can move them — they are
// computed once by the generator — so listing them here would only imply a
// guard that is really a schema fact. dueDate, by contrast, IS overridable per
// instance, which is why it is the one date recorded.
//
// Quoted verbatim (enums, a legal date-only, uuids):
//
//   - status: not_started → … → completed / blocked.
//   - dueDate: `YYYY-MM-DD`, the per-instance override of the computed date.
//   - assigneeId / dataTemplateId: `uuid`. Who owns the step, and which typed
//     schema its figures are validated against (ADR-0001).
//   - taxDataStatus: `oneof draft final` — whether the figures are declared
//     complete. Moving to final is the assertion an auditor tests.
//
// Redacted — the change is dated, the value withheld: notes. Free text on the
// object closest to the filing, so the likeliest place in the whole schema for
// a taxpayer's name or an adviser's phone number to be typed.
//
// taxData is handled separately, as a shape rather than values — see
// withTaxDataChange. The figures themselves must never reach an append-only log
// that ADR-0007 places outside the erasure boundary.
func auditValues(t *domain.TaskInstance) audit.Values {
	if t == nil {
		return nil
	}
	return audit.Values{
		"notes":          audit.Redact(t.Notes),
		"status":         t.Status,
		"dueDate":        t.DueDate,
		"assigneeId":     t.AssigneeID,
		"dataTemplateId": t.DataTemplateID,
		"taxDataStatus":  t.TaxDataStatus,
	}
}

// withTaxDataChange adds the tax record's change to an audit.Changes payload as
// a SHAPE: how many figures the record held on each side, and which keys moved.
// Never an amount.
//
// It cannot go through audit.Changes itself, and that is the whole point of the
// gap this closes. A per-side shape would compare equal whenever a figure is
// edited without adding or removing a key — the common case — and the change
// would vanish, which is exactly how a rewritten tax figure used to be
// indistinguishable from a status nudge. So the diff is computed here, from the
// two records the use case already has in hand.
//
// It is recorded BESIDE the whitelist envelope, not inside it:
//
//	{"fields":  {"dueDate": {"from": "2025-02-10", "to": "2025-11-30"}},
//	 "taxData": {"figuresBefore": 2, "figuresAfter": 3,
//	             "changedKeys": ["carryForward", "vatDue"]}}
//
// `fields` is a map of field name to exactly one {from, to} pair — that is the
// contract readers parse against — and a shape summary is not a before/after
// value, so it would break it. Recording it as its own key also states what it
// is: not the figures, but the count on each side (which distinguishes a figure
// added from one removed) and the sorted names of every key added, removed or
// re-valued.
//
// changedKeys is the criterion too: the summary appears only when some key's
// recorded value actually differs, so replaying the same figures adds nothing.
func withTaxDataChange(details map[string]any, before, after domain.TaxData) map[string]any {
	changed := changedTaxDataKeys(before, after)
	if len(changed) == 0 {
		return details // the two records read identically — nothing to record
	}
	if details == nil {
		details = map[string]any{}
	}
	details["taxData"] = map[string]any{
		"figuresBefore": len(before),
		"figuresAfter":  len(after),
		"changedKeys":   changed,
	}
	return details
}

// changedTaxDataKeys lists the keys whose value differs between the two records
// — added, removed or re-valued — sorted, and each passed through auditTaxDataKey.
func changedTaxDataKeys(before, after domain.TaxData) []string {
	keys := make([]string, 0, len(before)+len(after))
	for key, b := range before {
		a, ok := after[key]
		if !ok || !sameTaxValue(a, b) {
			keys = append(keys, auditTaxDataKey(key))
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			keys = append(keys, auditTaxDataKey(key))
		}
	}
	sort.Strings(keys)
	return keys
}

// sameTaxValue compares two figures without recording either: the JSON encoding
// is deterministic (map keys sorted), so equal values compare equal whatever
// their Go representation. A value that cannot be encoded compares unequal,
// which errs towards reporting a change.
func sameTaxValue(a, b any) bool {
	aj, aerr := json.Marshal(a)
	bj, berr := json.Marshal(b)
	if aerr != nil || berr != nil {
		return false
	}
	return string(aj) == string(bj)
}

// redactedKey stands in for a tax-data key that is not identifier-shaped.
const redactedKey = "redacted"

// maxTaxDataKeyLen is dto.FieldBody.ID's own `max=64`.
const maxTaxDataKeyLen = 64

// auditTaxDataKey is the gate on the one user-supplied string this envelope
// quotes. A tax-data key is a data-template field id — a schema name, not a
// datum (dto.FieldBody.ID, `min=1,max=64`, example "salesTotal") — but the
// contract constrains only its length, so nothing stops a caller defining a
// field whose id is a sentence. What is guaranteed here is SHAPE, not meaning:
// a key is quoted only if it is built from letters, digits, '_', '-' and '.',
// and anything else is recorded as "redacted" instead. The list keeps one entry
// per changed key either way, and the from / to counts are untouched, so the
// trail still says how many figures moved.
func auditTaxDataKey(key string) string {
	if key == "" || len(key) > maxTaxDataKeyLen {
		return redactedKey
	}
	for i := 0; i < len(key); i++ {
		switch c := key[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '-', c == '.':
		default:
			return redactedKey
		}
	}
	return key
}
