package domain

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	datatemplatesdomain "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Tax-data value limits per field type.
const (
	MaxTextValueLength = 10000
	MaxFileRefLength   = 500
)

// TaxDataStatus values: a draft may be partial; a final record carries every
// mandatory field.
const (
	TaxDataStatusDraft = "draft"
	TaxDataStatusFinal = "final"
)

// ValidateTaxData checks tax data against its template (ADR-0001: the server
// is the authority on what a valid record is) and returns the data to store:
// a copy with null-valued keys removed ("null clears"). Every key must be a
// template field id and every value must match its field type:
//
//   - numeric: a JSON number (never a string); a whole number when
//     allowDecimals is false; within min/max; at most decimalPlaces decimals
//   - date: YYYY-MM-DD (shared/dateonly)
//   - boolean: true/false
//   - text: a string of at most 10 000 characters
//   - file: a string reference of at most 500 characters (not resolved)
//
// A key that is not a template field is refused — unless the instance's
// stored data (carried) already holds it: a template edit may orphan a value
// that clients replay on every save, and that value is carried through
// untouched rather than making the instance un-editable.
//
// With requireMandatory (a final record, or a submission for approval) every
// mandatory field must be present and non-null; the error lists the missing
// field names. The returned error message is safe to surface as a 400.
func ValidateTaxData(tpl *datatemplatesdomain.DataTemplate, data, carried TaxData, requireMandatory bool) (TaxData, error) {
	if tpl == nil {
		return data, nil
	}

	cleaned := make(TaxData, len(data))
	// Deterministic error order: iterate keys sorted.
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := data[key]
		field := tpl.FieldByID(key)
		if field == nil {
			if _, orphaned := carried[key]; !orphaned {
				return nil, fmt.Errorf("tax data field %q is not in the template", key)
			}
			if value != nil {
				cleaned[key] = value // orphaned by a template edit: carried, not validated
			}
			continue
		}
		if value == nil || isBlank(field, value) {
			continue // null (or an emptied input) clears the field
		}
		if err := validateValue(field, value); err != nil {
			return nil, err
		}
		cleaned[key] = value
	}

	if requireMandatory {
		missing := make([]string, 0)
		for _, f := range tpl.Fields {
			if f.Mandatory && cleaned[f.ID] == nil {
				missing = append(missing, f.Name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("tax data is missing mandatory fields: %s", strings.Join(missing, ", "))
		}
	}

	return cleaned, nil
}

// TaxDataEqual reports whether two records carry the same values (nil and
// empty are the same record). Used to skip re-validation of data a client
// merely replays on a save that changes something else.
func TaxDataEqual(a, b TaxData) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// isBlank: a typed field whose input was emptied ("" from a cleared date or
// number box) means "no value", exactly like null. Text keeps "" as a value.
func isBlank(field *datatemplatesdomain.Field, value any) bool {
	s, ok := value.(string)
	if !ok || s != "" {
		return false
	}
	switch field.FieldType {
	case datatemplatesdomain.FieldTypeDate, datatemplatesdomain.FieldTypeNumeric,
		datatemplatesdomain.FieldTypeBoolean, datatemplatesdomain.FieldTypeFile:
		return true
	}
	return false
}

// MissingMandatoryFields lists the names of mandatory template fields that
// are absent or null in data (in template order).
func MissingMandatoryFields(tpl *datatemplatesdomain.DataTemplate, data TaxData) []string {
	missing := make([]string, 0)
	for _, f := range tpl.Fields {
		if f.Mandatory && (data == nil || data[f.ID] == nil) {
			missing = append(missing, f.Name)
		}
	}
	return missing
}

// ErrMissingMandatory formats the "final / submit" failure for a missing set.
func ErrMissingMandatory(missing []string) string {
	return "tax data is missing mandatory fields: " + strings.Join(missing, ", ")
}

func validateValue(field *datatemplatesdomain.Field, value any) error {
	switch field.FieldType {
	case datatemplatesdomain.FieldTypeNumeric:
		return validateNumeric(field, value)
	case datatemplatesdomain.FieldTypeDate:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("tax data field %q must be a YYYY-MM-DD date", field.ID)
		}
		if len(s) != 10 {
			return fmt.Errorf("tax data field %q must be a YYYY-MM-DD date", field.ID)
		}
		if _, err := dateonly.Parse(s); err != nil {
			return fmt.Errorf("tax data field %q must be a YYYY-MM-DD date", field.ID)
		}
	case datatemplatesdomain.FieldTypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("tax data field %q must be a boolean", field.ID)
		}
	case datatemplatesdomain.FieldTypeText:
		s, ok := value.(string)
		if !ok || len(s) > MaxTextValueLength {
			return fmt.Errorf("tax data field %q must be a string of at most %d characters", field.ID, MaxTextValueLength)
		}
	case datatemplatesdomain.FieldTypeFile:
		s, ok := value.(string)
		if !ok || len(s) > MaxFileRefLength {
			return fmt.Errorf("tax data field %q must be a file reference of at most %d characters", field.ID, MaxFileRefLength)
		}
	default:
		return fmt.Errorf("tax data field %q has an unsupported field type %q", field.ID, field.FieldType)
	}
	return nil
}

func validateNumeric(field *datatemplatesdomain.Field, value any) error {
	// JSON numbers decode to float64; anything else (notably a numeric
	// string) is rejected — the client must send a number.
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	default:
		return fmt.Errorf("tax data field %q must be a number", field.ID)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return fmt.Errorf("tax data field %q must be a finite number", field.ID)
	}
	nv := field.NumericValidation
	if nv == nil {
		return nil
	}
	if !nv.AllowDecimals && n != math.Trunc(n) {
		return fmt.Errorf("tax data field %q must be a whole number", field.ID)
	}
	if nv.Min != nil && n < *nv.Min {
		return fmt.Errorf("tax data field %q must be >= %s", field.ID, formatNumber(*nv.Min))
	}
	if nv.Max != nil && n > *nv.Max {
		return fmt.Errorf("tax data field %q must be <= %s", field.ID, formatNumber(*nv.Max))
	}
	if nv.AllowDecimals && nv.DecimalPlaces != nil && decimalPlaces(n) > *nv.DecimalPlaces {
		return fmt.Errorf("tax data field %q must have at most %d decimal places", field.ID, *nv.DecimalPlaces)
	}
	return nil
}

// decimalPlaces counts the fractional digits of the shortest decimal
// representation that round-trips the float (what the client typed).
func decimalPlaces(n float64) int {
	s := strconv.FormatFloat(n, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

func formatNumber(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}
