package dto

import "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"

// NumericValidationBody is the numeric rule of a field. allowDecimals defaults
// to true when omitted (pointer so "omitted" is distinguishable from false).
type NumericValidationBody struct {
	Min              *float64 `json:"min"`
	Max              *float64 `json:"max"`
	AllowDecimals    *bool    `json:"allowDecimals"`
	DecimalPlaces    *int     `json:"decimalPlaces"    validate:"omitempty,min=0,max=10"`
	FormatAsCurrency bool     `json:"formatAsCurrency"`
}

// FieldBody is one field definition — the frontend's DataField, exactly.
// Cross-field rules (unique ids, numericValidation only on numeric fields,
// min <= max) are enforced by domain.ValidateFields in the use case.
type FieldBody struct {
	ID                string                 `json:"id"                validate:"required,min=1,max=64" example:"f-vat-sales"`
	Name              string                 `json:"name"              validate:"required,min=1,max=200" example:"Total Sales (net)"`
	FieldType         string                 `json:"fieldType"         validate:"required,oneof=text numeric date boolean file" example:"numeric"`
	Mandatory         bool                   `json:"mandatory"`
	Description       *string                `json:"description"       validate:"omitempty,max=2000"`
	NumericValidation *NumericValidationBody `json:"numericValidation" validate:"omitempty"`
}

// CreateDataTemplateBody creates a CUSTOM template; category is not accepted
// (an unknown field → 400).
type CreateDataTemplateBody struct {
	Name         string      `json:"name"         validate:"required,min=1,max=200" example:"Quarterly VAT figures"`
	TemplateType string      `json:"templateType" validate:"required,oneof=VAT CIT TP WHT Custom" example:"VAT"`
	Description  *string     `json:"description"  validate:"omitempty,max=2000"`
	Fields       []FieldBody `json:"fields"       validate:"required,min=1,dive"`
}

// UpdateDataTemplateBody replaces name / templateType / description / fields.
type UpdateDataTemplateBody struct {
	Name         string      `json:"name"         validate:"required,min=1,max=200"`
	TemplateType string      `json:"templateType" validate:"required,oneof=VAT CIT TP WHT Custom"`
	Description  *string     `json:"description"  validate:"omitempty,max=2000"`
	Fields       []FieldBody `json:"fields"       validate:"required,min=1,dive"`
}

type DataTemplateIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

// ListDataTemplatesQuery filters the (unpaginated, name-sorted) list.
type ListDataTemplatesQuery struct {
	TemplateType *string `json:"templateType" validate:"omitempty,oneof=VAT CIT TP WHT Custom" example:"VAT"`
	Category     *string `json:"category"     validate:"omitempty,oneof=predefined custom" example:"custom"`
}

// FieldsToDomain maps the request fields onto the domain shape, applying the
// defaults the JSON contract promises (allowDecimals = true when omitted).
func FieldsToDomain(fields []FieldBody) domain.Fields {
	out := make(domain.Fields, 0, len(fields))
	for _, f := range fields {
		df := domain.Field{
			ID:          f.ID,
			Name:        f.Name,
			FieldType:   f.FieldType,
			Mandatory:   f.Mandatory,
			Description: f.Description,
		}
		if f.NumericValidation != nil {
			nv := &domain.NumericValidation{
				Min:              f.NumericValidation.Min,
				Max:              f.NumericValidation.Max,
				AllowDecimals:    true,
				DecimalPlaces:    f.NumericValidation.DecimalPlaces,
				FormatAsCurrency: f.NumericValidation.FormatAsCurrency,
			}
			if f.NumericValidation.AllowDecimals != nil {
				nv.AllowDecimals = *f.NumericValidation.AllowDecimals
			}
			df.NumericValidation = nv
		}
		out = append(out, df)
	}
	return out
}
