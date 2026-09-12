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
//     adjustment, additional deadlines — a typed rule object of enums, small
//     integers and MM-DD dates. Recorded whole, both sides.
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
		"deadlineRule":       eo.DeadlineRule,
		"status":             eo.Status,
	}
}
