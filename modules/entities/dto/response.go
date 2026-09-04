package dto

import (
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// EntityToJSON renders an entity for the API. created_at/updated_at are system
// instants → UTC ISO-8601 (ADR-0003). financialYearEnd is a legal date-only
// value carried verbatim as its MM-DD string (ADR-0002) — never through a
// timezone. customPeriods is always an array (empty for non-custom entities).
func EntityToJSON(e *domain.Entity) map[string]any {
	custom := e.CustomPeriods
	if custom == nil {
		custom = domain.CustomPeriods{}
	}
	return map[string]any{
		"id":                    e.ID,
		"parentEntityId":        e.ParentEntityID,
		"name":                  e.Name,
		"legalName":             e.LegalName,
		"country":               e.Country,
		"taxResidency":          e.TaxResidency,
		"fiscalCalendarPattern": e.FiscalCalendarPattern,
		"financialYearEnd":      e.FinancialYearEnd,
		"fiscalWeekEndDay":      e.FiscalWeekEndDay,
		"fiscalYearEndRule":     e.FiscalYearEndRule,
		"customPeriods":         custom,
		"status":                e.Status,
		"createdBy":             e.CreatedBy,
		"updatedBy":             e.UpdatedBy,
		"createdAt":             e.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":             e.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

// PeriodsToJSON renders GET /entities/{id}/periods rows: code, label and
// date-only start / end (ADR-0002).
func PeriodsToJSON(periods []deadline.Period) []map[string]any {
	out := make([]map[string]any, 0, len(periods))
	for _, p := range periods {
		out = append(out, map[string]any{
			"code":      p.Code,
			"label":     p.Label,
			"startDate": p.Start,
			"endDate":   p.End,
		})
	}
	return out
}
