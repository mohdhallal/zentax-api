package dto

import "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"

// DataTemplateToJSON renders the DataTemplateView: fields keep their order and
// omit numericValidation / description when absent; timestamps are UTC
// instants (ADR-0003).
func DataTemplateToJSON(t *domain.DataTemplate) map[string]any {
	fields := t.Fields
	if fields == nil {
		fields = domain.Fields{}
	}
	return map[string]any{
		"id":           t.ID,
		"name":         t.Name,
		"templateType": t.TemplateType,
		"category":     t.Category,
		"description":  t.Description,
		"fields":       fields,
		"createdAt":    t.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":    t.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

// DataTemplatesToJSON renders a list (never null).
func DataTemplatesToJSON(items []domain.DataTemplate) []map[string]any {
	data := make([]map[string]any, 0, len(items))
	for i := range items {
		data = append(data, DataTemplateToJSON(&items[i]))
	}
	return data
}
