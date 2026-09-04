package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
)

// NumericValidation constrains a numeric field. Only numeric fields may carry
// one. AllowDecimals defaults to true, FormatAsCurrency to false; DecimalPlaces
// (0..10) caps the fractional digits a value may carry.
type NumericValidation struct {
	Min              *float64 `json:"min,omitempty"`
	Max              *float64 `json:"max,omitempty"`
	AllowDecimals    bool     `json:"allowDecimals"`
	DecimalPlaces    *int     `json:"decimalPlaces,omitempty"`
	FormatAsCurrency bool     `json:"formatAsCurrency"`
}

// Field is one typed field of a template — the frontend's DataField, exactly.
// ID is the key the value is stored under in a task instance's tax data and
// is unique within the template; Name is the human label.
type Field struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	FieldType         string             `json:"fieldType"`
	Mandatory         bool               `json:"mandatory"`
	Description       *string            `json:"description,omitempty"`
	NumericValidation *NumericValidation `json:"numericValidation,omitempty"`
}

// Fields is the ordered field list, stored as a JSONB array (order preserved).
type Fields []Field

// FieldsCompatible reports whether next keeps every field of current with the
// same id AND type — the edit is additive (new fields, renames, descriptions,
// mandatory flags, numeric rules) and cannot orphan or invalidate values
// already recorded under current's ids. Used to gate edits of in-use templates.
func FieldsCompatible(current, next Fields) bool {
	types := make(map[string]string, len(next))
	for _, f := range next {
		types[f.ID] = f.FieldType
	}
	for _, f := range current {
		if t, ok := types[f.ID]; !ok || t != f.FieldType {
			return false
		}
	}
	return true
}

func (f Fields) Value() (driver.Value, error) {
	if f == nil {
		return json.Marshal([]Field{})
	}
	return json.Marshal([]Field(f))
}

func (f *Fields) Scan(src any) error {
	var data []byte
	switch v := src.(type) {
	case nil:
		*f = nil
		return nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("Fields.Scan: unsupported source type %T", src)
	}
	if len(data) == 0 {
		*f = nil
		return nil
	}
	var out []Field
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("Fields.Scan: invalid JSON: %w", err)
	}
	*f = out
	return nil
}

// Limits on a field definition.
const (
	MaxFieldIDLength   = 64
	MaxFieldNameLength = 200
	MaxDecimalPlaces   = 10
)

var fieldTypes = map[string]bool{
	FieldTypeText: true, FieldTypeNumeric: true, FieldTypeDate: true, FieldTypeBoolean: true, FieldTypeFile: true,
}

// IsFieldType reports whether t is a known field type.
func IsFieldType(t string) bool { return fieldTypes[t] }

// ValidateFields applies the strict cross-field rules on a template's field
// list — the ones a struct validator cannot express: at least one field,
// ids unique within the template, numericValidation only on numeric fields,
// min <= max, decimalPlaces within range. Per-field shape (lengths, enums) is
// checked here too so every entry point (API, seeding) shares one rule set.
// The returned error message is safe to surface as a 400.
func ValidateFields(fields Fields) error {
	if len(fields) == 0 {
		return fmt.Errorf("fields must contain at least one field")
	}
	seen := make(map[string]bool, len(fields))
	for i, f := range fields {
		pos := "fields[" + strconv.Itoa(i) + "]"
		if l := len(f.ID); l < 1 || l > MaxFieldIDLength {
			return fmt.Errorf("%s.id must be 1..%d characters", pos, MaxFieldIDLength)
		}
		if seen[f.ID] {
			return fmt.Errorf("%s.id %q is duplicated within the template", pos, f.ID)
		}
		seen[f.ID] = true
		if l := len(f.Name); l < 1 || l > MaxFieldNameLength {
			return fmt.Errorf("%s.name must be 1..%d characters", pos, MaxFieldNameLength)
		}
		if !IsFieldType(f.FieldType) {
			return fmt.Errorf("%s.fieldType %q is not one of text, numeric, date, boolean, file", pos, f.FieldType)
		}
		if f.NumericValidation == nil {
			continue
		}
		if f.FieldType != FieldTypeNumeric {
			return fmt.Errorf("%s.numericValidation is only allowed on numeric fields", pos)
		}
		nv := f.NumericValidation
		if nv.Min != nil && nv.Max != nil && *nv.Min > *nv.Max {
			return fmt.Errorf("%s.numericValidation.min must be <= max", pos)
		}
		if nv.DecimalPlaces != nil && (*nv.DecimalPlaces < 0 || *nv.DecimalPlaces > MaxDecimalPlaces) {
			return fmt.Errorf("%s.numericValidation.decimalPlaces must be 0..%d", pos, MaxDecimalPlaces)
		}
	}
	return nil
}
