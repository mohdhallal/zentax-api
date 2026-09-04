package domain

import "time"

type DataTemplateID = string

// Template categories: predefined rows are seeded per tenant and immutable
// through the API; custom rows are the tenant's own.
const (
	CategoryPredefined = "predefined"
	CategoryCustom     = "custom"
)

// Field types a template may declare (the frontend's DataField.fieldType).
const (
	FieldTypeText    = "text"
	FieldTypeNumeric = "numeric"
	FieldTypeDate    = "date"
	FieldTypeBoolean = "boolean"
	FieldTypeFile    = "file"
)

// DataTemplate is a reusable set of typed fields a task instance's tax data is
// collected against. TenantID is infrastructure (RLS), absent from the model.
// Name is unique per tenant.
type DataTemplate struct {
	ID           DataTemplateID `json:"id"           db:"id"`
	Name         string         `json:"name"         db:"name"`
	TemplateType string         `json:"templateType" db:"template_type"`
	Category     string         `json:"category"     db:"category"`
	Description  *string        `json:"description"  db:"description"`
	Fields       Fields         `json:"fields"       db:"fields"`
	CreatedBy    *string        `json:"createdBy"    db:"created_by"`
	UpdatedBy    *string        `json:"updatedBy"    db:"updated_by"`
	CreatedAt    time.Time      `json:"createdAt"    db:"created_at"`
	UpdatedAt    time.Time      `json:"updatedAt"    db:"updated_at"`
}

// IsPredefined reports whether the template is a curated, immutable one.
func (t *DataTemplate) IsPredefined() bool { return t.Category == CategoryPredefined }

// FieldByID returns the field with the given id, or nil.
func (t *DataTemplate) FieldByID(id string) *Field {
	for i := range t.Fields {
		if t.Fields[i].ID == id {
			return &t.Fields[i]
		}
	}
	return nil
}

type CreateDataTemplateInput struct {
	Name         string
	TemplateType string
	Category     string
	Description  *string
	Fields       Fields
}

type UpdateDataTemplateInput struct {
	Name         string
	TemplateType string
	Description  *string
	Fields       Fields
}

// ListDataTemplatesArgs are the (optional) list filters; nil = no filter.
type ListDataTemplatesArgs struct {
	TemplateType *string
	Category     *string
}
