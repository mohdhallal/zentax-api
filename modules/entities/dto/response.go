package dto

import "github.com/mohamadhallal/zentax-api/modules/entities/domain"

// EntityToJSON renders an entity for the API. created_at/updated_at are system
// instants → UTC ISO-8601 (ADR-0003). financialYearEnd is a legal date-only
// value carried verbatim as its MM-DD string (ADR-0002) — never through a
// timezone.
func EntityToJSON(e *domain.Entity) map[string]any {
	return map[string]any{
		"id":                    e.ID,
		"parentEntityId":        e.ParentEntityID,
		"name":                  e.Name,
		"legalName":             e.LegalName,
		"country":               e.Country,
		"taxResidency":          e.TaxResidency,
		"fiscalCalendarPattern": e.FiscalCalendarPattern,
		"financialYearEnd":      e.FinancialYearEnd,
		"status":                e.Status,
		"createdAt":             e.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":             e.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}
