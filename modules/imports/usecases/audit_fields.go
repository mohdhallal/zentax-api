package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// The ADR-0008 whitelists for the records an import writes.
//
// They are DELIBERATE COPIES of modules/entities/usecases/audit_fields.go and
// modules/entityobligations/usecases/audit_fields.go, field for field and
// redaction for redaction, because the trail must not be able to tell how a
// change arrived. If an entity's country is redacted when someone edits it by
// hand, it must be redacted when a file edits it; if the deadline rule is
// quoted verbatim on a PUT, it is quoted here, or an auditor reconstructing the
// rule that dated a statutory filing would find the evidence depends on which
// door the change came through.
//
// A copy rather than an import because modules here are vertical slices: the
// entities module owns its whitelist and may narrow it, and this file is the
// place that has to notice. The acceptance suite compares the two envelopes for
// the same change, which is what keeps the copy honest.
//
// The rule both files apply: a value is quoted verbatim ONLY when the request
// contract constrains its shape — a `oneof` enum, a fixed-length legal date, a
// uuid. Anything a customer types freely is recorded as having moved and never
// quoted.

// auditEntityValues mirrors the entity whitelist: the fiscal calendar and the
// position in the group are quoted (they decide what a reporting period IS and
// who may see the entity); the names, the country and the tax residency are
// recorded as having moved and never quoted.
func auditEntityValues(e *domain.ExistingEntity) audit.Values {
	if e == nil {
		return nil
	}
	return audit.Values{
		"name":                  audit.Redact(e.Name),
		"legalName":             audit.Redact(e.LegalName),
		"country":               audit.Redact(e.Country),
		"taxResidency":          audit.Redact(e.TaxResidency),
		"parentEntityId":        e.ParentID,
		"fiscalCalendarPattern": e.Pattern,
		"financialYearEnd":      e.YearEnd,
		"fiscalWeekEndDay":      e.WeekEndDay,
		"fiscalYearEndRule":     e.YearEndRule,
		"status":                e.Status,
	}
}

// auditObligationValues mirrors the entity-obligation whitelist. The deadline
// rule is quoted whole: it is what computed a statutory filing date, nothing
// versions it, and a full-replace update destroys the only copy — which is the
// exact case ADR-0008's before-state rule exists for. The tax reference number
// is redacted (ADR-0006 names it a field to encrypt).
func auditObligationValues(o *domain.ExistingObligation) audit.Values {
	if o == nil {
		return nil
	}
	var rule any
	if len(o.DeadlineRule) > 0 {
		rule = o.DeadlineRule
	}
	return audit.Values{
		"entityId":           o.EntityID,
		"obligationTypeId":   o.ObligationTypeID,
		"taxReferenceNumber": audit.Redact(o.TaxReferenceNumber),
		"jurisdiction":       audit.Redact(o.Jurisdiction),
		"jurisdictionState":  audit.Redact(o.JurisdictionState),
		"currency":           o.Currency,
		"periodicity":        o.Periodicity,
		"deadlineRule":       rule,
		"status":             o.Status,
	}
}

// auditBatchValues is the import OPERATION's own envelope.
//
// Everything on it is server-generated or drawn from a closed vocabulary, which
// is the stricter rule ADR-0008 applies to the envelope itself: the kind is one
// of two words, the counts are integers this code computed, and the checksum is
// a sha256 the server calculated over bytes it received. The FILE NAME is the
// one thing the customer chose, and it is a free-text string that routinely
// carries a company name or a person's — so it is recorded as present and never
// quoted. The checksum identifies the file for anyone who still has it.
func auditBatchValues(b *domain.Batch) audit.Values {
	if b == nil {
		return nil
	}
	return audit.Values{
		"kind":           string(b.Kind),
		"status":         string(b.Status),
		"fileName":       audit.Redact(b.FileName),
		"checksum":       b.Checksum,
		"byteSize":       b.ByteSize,
		"rowCount":       b.RowCount,
		"createCount":    b.CreateCount,
		"updateCount":    b.UpdateCount,
		"unchangedCount": b.UnchangedCount,
		"invalidCount":   b.InvalidCount,
	}
}
