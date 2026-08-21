package dto

import "github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"

func ObligationTypeToJSON(ot *domain.ObligationType) map[string]any {
	return map[string]any{
		"id":          ot.ID,
		"name":        ot.Name,
		"code":        ot.Code,
		"category":    ot.Category,
		"template":    ot.Template,
		"status":      ot.Status,
		"description": ot.Description,
		"createdBy":   ot.CreatedBy,
		"updatedBy":   ot.UpdatedBy,
		"createdAt":   ot.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":   ot.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}
