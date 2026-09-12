package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for a data template — the schema that
// decides which tax figures the system accepts, and in what shape. It feeds
// audit.Changes, which records the before and after of whatever actually moved.
//
// This is the one resource whose change is invisible in the data it governs. A
// task instance records its figures (and taskinstances/usecases/audit_fields.go
// records that they moved), but nothing anywhere records the rules those
// figures were validated against: there is no version column and no history
// table, and domain.FieldsCompatible freezes only a field's id and type once a
// template is in use — mandatory, the bounds, the decimal places and the
// currency flag stay freely editable on a template already attached to a live
// workflow step. Recording a field COUNT made that edit byte-identical to no
// edit at all; the structure below is what makes it legible.
//
// Recorded verbatim (an enum, plus structure that is schema, never a figure):
//
//   - templateType: `oneof VAT CIT TP WHT Custom`.
//   - fields: the field list projected onto its structure — see auditFields.
//
// Redacted — the change is dated, the value withheld: name and description.
// Both are free text a user typed (`max=200` and `max=2000` with no shape rule),
// and a template name is as likely to carry a client's name as an entity's is.
//
// Left out by design: category (the server sets it — Create forces custom,
// Update cannot move it, Delete refuses a predefined row — so it is a constant
// on every envelope this whitelist feeds), the actor / timestamp columns (the
// envelope carries those) and the id (that is resource_id).
func auditValues(t *domain.DataTemplate) audit.Values {
	if t == nil {
		return nil
	}
	return audit.Values{
		"name":         audit.Redact(t.Name),
		"description":  audit.Redact(t.Description),
		"templateType": t.TemplateType,
		"fields":       auditFields(t.Fields),
	}
}

// auditFields projects the field list onto its STRUCTURE: which fields exist,
// what type each one is, whether it must be filled, and the numeric rule that
// decides which values pass. None of that is a tax figure — a bound is the
// schema's own limit, not an amount anybody filed — and none of it is free
// text, with the single exception of the field id, which goes through
// auditIdentifier.
//
// Order is preserved because domain.Fields is ordered and the order is what the
// form renders, so a reordering shows up as a change.
//
// The labels and descriptions stay out, and the consequence is deliberate and
// worth stating: re-wording a field's label leaves no trace here, while adding
// a field, removing one, retyping one, making one mandatory or widening the
// range of figures it accepts all do.
func auditFields(fields domain.Fields) []map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(fields))
	for _, f := range fields {
		entry := map[string]any{
			"id":        auditIdentifier(f.ID),
			"fieldType": f.FieldType,
			"mandatory": f.Mandatory,
		}
		if nv := f.NumericValidation; nv != nil {
			rule := map[string]any{
				"allowDecimals":    nv.AllowDecimals,
				"formatAsCurrency": nv.FormatAsCurrency,
			}
			if nv.Min != nil {
				rule["min"] = *nv.Min
			}
			if nv.Max != nil {
				rule["max"] = *nv.Max
			}
			if nv.DecimalPlaces != nil {
				rule["decimalPlaces"] = *nv.DecimalPlaces
			}
			entry["numericValidation"] = rule
		}
		out = append(out, entry)
	}
	return out
}

// redactedIdentifier stands in for a string that should have been a name but
// is not shaped like one.
const redactedIdentifier = "redacted"

// auditIdentifier is the gate on the one user-supplied string this envelope
// quotes. A field id is a schema name, not a datum (dto.FieldBody.ID,
// `min=1,max=64`, example "salesTotal") — it is the key a task instance's
// figures are stored under, so it has to be quoted for the change to mean
// anything — but the contract constrains only its length, so nothing stops a
// caller defining a field whose id is a sentence or an email address. What is
// guaranteed here is SHAPE, not meaning: a value is quoted only if it is built
// from letters, digits, '_', '-' and '.', and anything else is recorded as
// "redacted" instead.
//
// This is a deliberate per-module copy of one rule — the same gate guards tax-
// data keys (taskinstances/usecases/audit_fields.go:auditTaxDataKey), custom
// fiscal period codes (entities) and additional-deadline types
// (entityobligations). Each module owns its own whitelist and its own vocabulary
// of what "identifier-shaped" means for its contract; lifting it into
// platform/audit is the obvious consolidation once a fourth caller wants
// exactly these characters.
func auditIdentifier(s string) string {
	if s == "" || len(s) > domain.MaxFieldIDLength {
		return redactedIdentifier
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '-', c == '.':
		default:
			return redactedIdentifier
		}
	}
	return s
}
