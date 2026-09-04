package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dt "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
)

func f64(v float64) *float64 { return &v }
func intp(v int) *int        { return &v }

// vatTemplate: a currency (2 dp, ≥ 0), a whole-number count, a rate (0..100),
// a date, a flag, a note and a file — every field type once.
func vatTemplate() *dt.DataTemplate {
	return &dt.DataTemplate{
		ID: "tpl-1", Name: "VAT Return", TemplateType: "VAT", Category: dt.CategoryPredefined,
		Fields: dt.Fields{
			{ID: "sales", Name: "Total Sales (net)", FieldType: dt.FieldTypeNumeric, Mandatory: true,
				NumericValidation: &dt.NumericValidation{Min: f64(0), AllowDecimals: true, DecimalPlaces: intp(2), FormatAsCurrency: true}},
			{ID: "invoices", Name: "Invoice Count", FieldType: dt.FieldTypeNumeric, Mandatory: false,
				NumericValidation: &dt.NumericValidation{AllowDecimals: false}},
			{ID: "rate", Name: "Rate (%)", FieldType: dt.FieldTypeNumeric, Mandatory: true,
				NumericValidation: &dt.NumericValidation{Min: f64(0), Max: f64(100), AllowDecimals: true}},
			{ID: "plain", Name: "Plain number", FieldType: dt.FieldTypeNumeric}, // no rule
			{ID: "filed", Name: "Filing Date", FieldType: dt.FieldTypeDate},
			{ID: "nil", Name: "Nil Return", FieldType: dt.FieldTypeBoolean},
			{ID: "note", Name: "Note", FieldType: dt.FieldTypeText},
			{ID: "evidence", Name: "Evidence", FieldType: dt.FieldTypeFile},
		},
	}
}

func TestValidateTaxData_NilTemplatePassesThrough(t *testing.T) {
	t.Parallel()
	data := TaxData{"anything": "goes"}
	got, err := ValidateTaxData(nil, data, nil, true)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestValidateTaxData_UnknownKey(t *testing.T) {
	t.Parallel()
	_, err := ValidateTaxData(vatTemplate(), TaxData{"sales": 1.0, "bogus": 1.0}, nil, false)
	assert.EqualError(t, err, `tax data field "bogus" is not in the template`)
}

// A key the template no longer has (a template edit orphaned it) is carried
// through when the stored record already holds it, and never validated.
func TestValidateTaxData_OrphanedKeyCarriedFromStoredRecord(t *testing.T) {
	t.Parallel()
	carried := TaxData{"legacy": "whatever", "sales": 1.0}
	got, err := ValidateTaxData(vatTemplate(), TaxData{"sales": 2.0, "legacy": "whatever"}, carried, false)
	require.NoError(t, err)
	assert.Equal(t, TaxData{"sales": 2.0, "legacy": "whatever"}, got)

	// Clearing an orphan with null drops it like any other key.
	got, err = ValidateTaxData(vatTemplate(), TaxData{"sales": 2.0, "legacy": nil}, carried, false)
	require.NoError(t, err)
	assert.Equal(t, TaxData{"sales": 2.0}, got)
}

// An emptied typed input ("" from a cleared date / number box) clears the
// field like null; text keeps "" as a value.
func TestValidateTaxData_EmptyStringClearsTypedFields(t *testing.T) {
	t.Parallel()
	got, err := ValidateTaxData(vatTemplate(), TaxData{"sales": 1.0, "filed": "", "invoices": "", "note": ""}, nil, false)
	require.NoError(t, err)
	assert.Equal(t, TaxData{"sales": 1.0, "note": ""}, got)
}

func TestTaxDataEqual(t *testing.T) {
	t.Parallel()
	assert.True(t, TaxDataEqual(nil, TaxData{}))
	assert.True(t, TaxDataEqual(TaxData{"a": 1.0}, TaxData{"a": 1.0}))
	assert.False(t, TaxDataEqual(TaxData{"a": 1.0}, TaxData{"a": 2.0}))
	assert.False(t, TaxDataEqual(TaxData{"a": 1.0}, nil))
}

func TestValidateTaxData_NumericRules(t *testing.T) {
	t.Parallel()
	tpl := vatTemplate()
	cases := []struct {
		name string
		data TaxData
		want string
	}{
		{"string for numeric", TaxData{"sales": "5000"}, `tax data field "sales" must be a number`},
		{"bool for numeric", TaxData{"sales": true}, `tax data field "sales" must be a number`},
		{"decimals not allowed", TaxData{"invoices": 3.5}, `tax data field "invoices" must be a whole number`},
		{"below min", TaxData{"sales": -1.0}, `tax data field "sales" must be >= 0`},
		{"above max", TaxData{"rate": 100.5}, `tax data field "rate" must be <= 100`},
		{"too many decimals", TaxData{"sales": 10.005}, `tax data field "sales" must have at most 2 decimal places`},
	}
	for _, tc := range cases {
		_, err := ValidateTaxData(tpl, tc.data, nil, false)
		assert.EqualError(t, err, tc.want, tc.name)
	}

	// Accepted: JSON numbers (float64), integers via int, exact decimals, bounds inclusive.
	got, err := ValidateTaxData(tpl, TaxData{"sales": 12345.67, "invoices": float64(12), "rate": 100.0, "plain": 1.23456789}, nil, false)
	require.NoError(t, err)
	assert.Equal(t, TaxData{"sales": 12345.67, "invoices": float64(12), "rate": 100.0, "plain": 1.23456789}, got)
	_, err = ValidateTaxData(tpl, TaxData{"invoices": 7}, nil, false)
	require.NoError(t, err)
}

func TestValidateTaxData_DateBooleanTextFile(t *testing.T) {
	t.Parallel()
	tpl := vatTemplate()
	longText := make([]byte, MaxTextValueLength+1)
	longRef := make([]byte, MaxFileRefLength+1)
	for i := range longText {
		longText[i] = 'a'
	}
	for i := range longRef {
		longRef[i] = 'a'
	}
	cases := []struct {
		name string
		data TaxData
		want string
	}{
		{"date not string", TaxData{"filed": 20250131.0}, `tax data field "filed" must be a YYYY-MM-DD date`},
		{"date wrong format", TaxData{"filed": "31/01/2025"}, `tax data field "filed" must be a YYYY-MM-DD date`},
		{"date with time", TaxData{"filed": "2025-01-31T00:00:00Z"}, `tax data field "filed" must be a YYYY-MM-DD date`},
		{"date invalid day", TaxData{"filed": "2025-02-30"}, `tax data field "filed" must be a YYYY-MM-DD date`},
		{"boolean as string", TaxData{"nil": "true"}, `tax data field "nil" must be a boolean`},
		{"text as number", TaxData{"note": 1.0}, `tax data field "note" must be a string of at most 10000 characters`},
		{"text too long", TaxData{"note": string(longText)}, `tax data field "note" must be a string of at most 10000 characters`},
		{"file as object", TaxData{"evidence": map[string]any{"id": "x"}}, `tax data field "evidence" must be a file reference of at most 500 characters`},
		{"file too long", TaxData{"evidence": string(longRef)}, `tax data field "evidence" must be a file reference of at most 500 characters`},
	}
	for _, tc := range cases {
		_, err := ValidateTaxData(tpl, tc.data, nil, false)
		assert.EqualError(t, err, tc.want, tc.name)
	}

	got, err := ValidateTaxData(tpl, TaxData{"filed": "2025-01-31", "nil": false, "note": "ok", "evidence": "doc-123"}, nil, false)
	require.NoError(t, err)
	assert.Equal(t, TaxData{"filed": "2025-01-31", "nil": false, "note": "ok", "evidence": "doc-123"}, got)
}

func TestValidateTaxData_NullClearsAndFinalRequiresMandatory(t *testing.T) {
	t.Parallel()
	tpl := vatTemplate()

	// A null value is dropped from the stored record, whatever the type.
	got, err := ValidateTaxData(tpl, TaxData{"sales": nil, "rate": 5.0, "note": nil}, nil, false)
	require.NoError(t, err)
	assert.Equal(t, TaxData{"rate": 5.0}, got)

	// Draft: mandatory fields may be missing.
	_, err = ValidateTaxData(tpl, TaxData{}, nil, false)
	require.NoError(t, err)
	_, err = ValidateTaxData(tpl, nil, nil, false)
	require.NoError(t, err)

	// Final: every mandatory field present and non-null — names listed in template order.
	_, err = ValidateTaxData(tpl, TaxData{"sales": nil, "note": "x"}, nil, true)
	assert.EqualError(t, err, "tax data is missing mandatory fields: Total Sales (net), Rate (%)")
	_, err = ValidateTaxData(tpl, nil, nil, true)
	assert.EqualError(t, err, "tax data is missing mandatory fields: Total Sales (net), Rate (%)")
	_, err = ValidateTaxData(tpl, TaxData{"sales": 100.0}, nil, true)
	assert.EqualError(t, err, "tax data is missing mandatory fields: Rate (%)")

	got, err = ValidateTaxData(tpl, TaxData{"sales": 100.0, "rate": 20.0}, nil, true)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestMissingMandatoryFields(t *testing.T) {
	t.Parallel()
	tpl := vatTemplate()
	assert.Equal(t, []string{"Total Sales (net)", "Rate (%)"}, MissingMandatoryFields(tpl, nil))
	assert.Equal(t, []string{"Rate (%)"}, MissingMandatoryFields(tpl, TaxData{"sales": 1.0, "rate": nil}))
	assert.Empty(t, MissingMandatoryFields(tpl, TaxData{"sales": 1.0, "rate": 1.0}))
	assert.Equal(t, "tax data is missing mandatory fields: A, B", ErrMissingMandatory([]string{"A", "B"}))
}
