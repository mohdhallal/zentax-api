package dto

import "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"

func EntityObligationToJSON(eo *domain.EntityObligation) map[string]any {
	return map[string]any{
		"id":               eo.ID,
		"entityId":         eo.EntityID,
		"obligationTypeId": eo.ObligationTypeID,
		"jurisdiction":     eo.Jurisdiction,
		"periodicity":      eo.Periodicity,
		"deadlineRule":     eo.DeadlineRule,
		"status":           eo.Status,
		"createdBy":        eo.CreatedBy,
		"updatedBy":        eo.UpdatedBy,
		"createdAt":        eo.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":        eo.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}
