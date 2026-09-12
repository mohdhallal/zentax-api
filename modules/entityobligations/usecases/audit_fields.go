package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for an entity obligation — the row
// that says "this entity owes this tax, on this cycle, due on these dates". It
// feeds audit.Changes, which records the before and after of whatever actually
// moved.
//
// This is the sharpest case in the domain for a before-value. deadlineRule is
// the rule that computed a statutory filing date; nothing versions it, so
// until now one PUT replaced the only copy in existence and the trail said
// only "an obligation was updated". With the rule recorded on both sides, a
// date that looks wrong in hindsight can be re-derived from the rule that was
// actually in force when the period was generated.
//
// A value is quoted verbatim ONLY when the request contract constrains its
// shape, checked against modules/entityobligations/dto/request.go:
//
//   - deadlineRule: filing and payment offsets or fixed dates, weekend
//     adjustment, additional deadlines. Every member of the rule is a `oneof`
//     enum, an integer, or a `len=5` MM-DD date — except one, and that
//     exception is the reason the rule is projected rather than quoted whole:
//     see auditDeadlineRule.
//   - periodicity: `oneof weekly monthly … consolidated-annual`.
//   - currency: `len=3,uppercase` — ISO 4217, the unit the figures are filed in.
//   - status: `oneof active inactive`.
//   - entityId / obligationTypeId: `uuid`. Who owes what (fixed at creation).
//
// Redacted — the change is dated, the value withheld:
//
//   - taxReferenceNumber: the tax identifier ADR-0006 singles out for
//     field-level encryption; it must never be duplicated into an append-only,
//     crypto-shredding-exempt log, so the trail records only that it was set,
//     cleared or replaced.
//   - jurisdiction / jurisdictionState: whose rules apply. Both are `max=100`
//     free text with no code list behind them — nothing stops a caller putting
//     a filing agent's name, phone number or email in either — so they get a
//     name's treatment, not reference data's, until the input is a code list.
//     (Same hole, same remedy, as country / taxResidency on the entity.)
//
// Left out by design: the actor / timestamp columns (the envelope carries
// those) and the id (resource_id).
func auditValues(eo *domain.EntityObligation) audit.Values {
	if eo == nil {
		return nil
	}
	return audit.Values{
		"taxReferenceNumber": audit.Redact(eo.TaxReferenceNumber),
		"jurisdiction":       audit.Redact(eo.Jurisdiction),
		"jurisdictionState":  audit.Redact(eo.JurisdictionState),
		"entityId":           eo.EntityID,
		"obligationTypeId":   eo.ObligationTypeID,
		"currency":           eo.Currency,
		"periodicity":        eo.Periodicity,
		"deadlineRule":       auditDeadlineRule(eo.DeadlineRule),
		"status":             eo.Status,
	}
}

// auditDeadlineRule records the rule that computes a statutory filing date,
// with its one unbounded member gated.
//
// The rule is the sharpest thing in the domain to keep, and everything in it is
// reference data — offsets, a weekend adjustment, MM-DD calendar dates — with a
// single exception: AdditionalDeadline.Type is `required,max=50` free text. No
// oneof, no code list, no CHECK behind the JSONB column. It is documented as
// "an extra deadline of a named kind", which is precisely where someone types
// "chase Jane Doe, jane.doe@example.com" — and quoting the rule whole put that
// string into two append-only rows that ADR-0007 places outside the erasure
// boundary, beside a jurisdiction the same whitelist withholds two lines above
// for being `max=100` free text.
//
// So the type goes through the shape gate and the rest of the rule is
// untouched: the offsets, the weekend adjustment and the MM-DD dates that let a
// date be re-derived are all still quoted, on both sides, and each additional
// deadline keeps its months and days.
//
// The consequence is deliberate and worth stating: re-wording one additional
// deadline's type from one free-text phrase to another leaves no trace here
// (both sides gate to "redacted" and compare equal), while adding one, removing
// one or moving its offset does. That is the same trade
// workflowtasks/usecases/audit_fields.go:auditDocumentRequirements makes for
// requirement names.
//
// The returned rule is a copy: the entries are gated in a fresh slice so the
// caller's domain object — which is on its way to the database on an update —
// is never mutated.
//
// One thing to know before adding a field to domain.DeadlineRule: everything
// else in the object passes through here verbatim, so a new free-text member
// would reach the trail unredacted. Gate it here, or the whitelist comment
// above stops being true.
func auditDeadlineRule(r domain.DeadlineRule) domain.DeadlineRule {
	if len(r.AdditionalDeadlines) == 0 {
		return r
	}
	gated := make([]domain.AdditionalDeadline, len(r.AdditionalDeadlines))
	copy(gated, r.AdditionalDeadlines)
	for i := range gated {
		gated[i].Type = auditIdentifier(gated[i].Type)
	}
	r.AdditionalDeadlines = gated
	return r
}

// maxDeadlineTypeLen is domain.AdditionalDeadline.Type's own `max=50`.
const maxDeadlineTypeLen = 50

// redactedIdentifier stands in for a string that should have been a name but is
// not shaped like one.
const redactedIdentifier = "redacted"

// auditIdentifier is the gate on the one user-supplied string this envelope
// quotes. What is guaranteed here is SHAPE, not meaning: a value is quoted only
// if it is built from letters, digits, '_', '-' and '.', which passes every
// type the codebase itself produces ("filing", "payment", "advance_payment")
// and redacts anything carrying a space or an '@'.
//
// This is a deliberate per-module copy of one rule — the same gate guards
// tax-data keys (taskinstances), data-template field ids (datatemplates) and
// custom fiscal period codes (entities). Each module owns its own whitelist and
// its own length bound; lifting the character rule into platform/audit is the
// obvious consolidation once a caller wants it parameterized.
func auditIdentifier(s string) string {
	if s == "" || len(s) > maxDeadlineTypeLen {
		return redactedIdentifier
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '-', c == '.':
		default:
			return redactedIdentifier
		}
	}
	return s
}
