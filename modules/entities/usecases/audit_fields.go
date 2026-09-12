package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for an entity: what an auditor asks
// about when a filing made under this entity is questioned, and nothing else.
// It feeds audit.Changes, which records the before and after of whatever
// actually moved.
//
// A value is quoted verbatim ONLY when the request contract constrains its
// shape — a `oneof` enum, a fixed-length legal date, a uuid. That is the rule
// that keeps the envelope free of personal data as the schema grows, and it is
// checked against modules/entities/dto/request.go, not assumed:
//
//   - the fiscal calendar (pattern, financial year end, week-end day, year-end
//     rule, custom periods): the configuration that decides what a reporting
//     period IS, and therefore which period a filing belongs to and when it was
//     due (ADR-0023). Changing it retroactively re-cuts the year, so an auditor
//     needs the version that was in force. pattern / weekEndDay / yearEndRule
//     are `oneof` enums, financialYearEnd is `len=5` (MM-DD), and a custom
//     period reaches the trail as code + boundaries only (auditPeriods).
//   - parentEntityId: `uuid`. The position in the group, which drives
//     consolidation and decides who can see the entity at all (ADR-0012).
//   - status: `oneof active inactive archived`.
//
// Redacted — the change is dated, the value withheld:
//
//   - name / legalName: the entity's identity in the outside world, i.e.
//     customer free text of exactly the kind ADR-0008 keeps out of the
//     envelope.
//   - country / taxResidency: whose tax law applies — the one thing here an
//     auditor would most like quoted, and the one the contract cannot promise.
//     Both are `max=100` free text with no code list behind them (there is no
//     ISO 3166 validation, and the UI's country picker is a client-side
//     convenience, not server-side authority), so a caller may put a person, a
//     street or an email address in either. Until the input is a code list they
//     are treated exactly like a name: recorded as having moved, never quoted.
//
// For every redacted field the current value is on the row; the trail says when
// it last moved and who moved it.
//
// Left out by design: createdBy / updatedBy / createdAt / updatedAt (the
// envelope's own actor_id and occurred_at say it better) and the id (that is
// resource_id).
func auditValues(e *domain.Entity) audit.Values {
	if e == nil {
		return nil
	}
	return audit.Values{
		"name":                  audit.Redact(e.Name),
		"legalName":             audit.Redact(e.LegalName),
		"country":               audit.Redact(e.Country),
		"taxResidency":          audit.Redact(e.TaxResidency),
		"parentEntityId":        e.ParentEntityID,
		"fiscalCalendarPattern": e.FiscalCalendarPattern,
		"financialYearEnd":      e.FinancialYearEnd,
		"fiscalWeekEndDay":      e.FiscalWeekEndDay,
		"fiscalYearEndRule":     e.FiscalYearEndRule,
		"customPeriods":         auditPeriods(e.CustomPeriods),
		"status":                e.Status,
	}
}

// auditPeriods projects a custom fiscal calendar onto the part that computes
// dates — each period's code and its MM-DD boundaries — and drops the display
// name, which is free text the tenant typed.
func auditPeriods(periods domain.CustomPeriods) []map[string]any {
	if len(periods) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(periods))
	for _, p := range periods {
		out = append(out, map[string]any{
			"code":      p.Code,
			"startDate": p.StartDate,
			"endDate":   p.EndDate,
		})
	}
	return out
}
