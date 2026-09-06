package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/shared/taxkeys"
)

func f64(v float64) *float64 { return &v }

func validField(id string) Field {
	return Field{ID: id, Name: "Field " + id, FieldType: FieldTypeText}
}

func TestValidateFields_RequiresAtLeastOne(t *testing.T) {
	t.Parallel()
	assert.EqualError(t, ValidateFields(nil), "fields must contain at least one field")
	assert.EqualError(t, ValidateFields(Fields{}), "fields must contain at least one field")
}

func TestValidateFields_DuplicateID(t *testing.T) {
	t.Parallel()
	err := ValidateFields(Fields{validField("a"), validField("a")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `fields[1].id "a" is duplicated`)
}

func TestValidateFields_IDAndNameLengths(t *testing.T) {
	t.Parallel()
	long := make([]byte, MaxFieldIDLength+1)
	for i := range long {
		long[i] = 'x'
	}
	assert.Error(t, ValidateFields(Fields{{ID: "", Name: "n", FieldType: FieldTypeText}}))
	assert.Error(t, ValidateFields(Fields{{ID: string(long), Name: "n", FieldType: FieldTypeText}}))
	assert.Error(t, ValidateFields(Fields{{ID: "a", Name: "", FieldType: FieldTypeText}}))
}

func TestValidateFields_UnknownType(t *testing.T) {
	t.Parallel()
	err := ValidateFields(Fields{{ID: "a", Name: "A", FieldType: "money"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fields[0].fieldType")
}

func TestValidateFields_NumericValidationOnlyOnNumeric(t *testing.T) {
	t.Parallel()
	err := ValidateFields(Fields{{ID: "a", Name: "A", FieldType: FieldTypeText, NumericValidation: &NumericValidation{AllowDecimals: true}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "numericValidation is only allowed on numeric fields")

	// The same rule on a numeric field is fine.
	require.NoError(t, ValidateFields(Fields{{ID: "a", Name: "A", FieldType: FieldTypeNumeric, NumericValidation: &NumericValidation{AllowDecimals: true}}}))
}

func TestValidateFields_MinMaxAndDecimalPlaces(t *testing.T) {
	t.Parallel()
	err := ValidateFields(Fields{{ID: "a", Name: "A", FieldType: FieldTypeNumeric,
		NumericValidation: &NumericValidation{Min: f64(10), Max: f64(5), AllowDecimals: true}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "min must be <= max")

	// min == max is allowed (a fixed value).
	require.NoError(t, ValidateFields(Fields{{ID: "a", Name: "A", FieldType: FieldTypeNumeric,
		NumericValidation: &NumericValidation{Min: f64(5), Max: f64(5)}}}))

	eleven := 11
	err = ValidateFields(Fields{{ID: "a", Name: "A", FieldType: FieldTypeNumeric,
		NumericValidation: &NumericValidation{AllowDecimals: true, DecimalPlaces: &eleven}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decimalPlaces must be 0..10")
}

func TestPredefinedTemplates_AreValidAndStable(t *testing.T) {
	t.Parallel()
	tpls := PredefinedTemplates()
	require.Len(t, tpls, 3)

	names := map[string]string{}
	for _, tpl := range tpls {
		require.NoError(t, ValidateFields(tpl.Fields), tpl.Name)
		assert.Equal(t, CategoryPredefined, tpl.Category)
		names[tpl.Name] = tpl.TemplateType
	}
	assert.Equal(t, map[string]string{"VAT Return": "VAT", "Corporate Income Tax": "CIT", "Withholding Tax": "WHT"}, names)

	// Field ids are the canonical tax-data keys (stable across environments).
	vat := tpls[0]
	assert.Equal(t, []string{"salesTotal", "outputVat", "inputVat", "netVat"}, fieldIDs(vat.Fields))
	assert.True(t, vat.Fields[0].Mandatory)
	assert.False(t, vat.Fields[3].Mandatory)
	assert.True(t, vat.Fields[0].NumericValidation.FormatAsCurrency)
	assert.Equal(t, 2, *vat.Fields[0].NumericValidation.DecimalPlaces)

	cit := tpls[1]
	assert.Equal(t, []string{"profitBeforeTax", "adjustments", "taxableIncome", "taxRate", "taxLiability"}, fieldIDs(cit.Fields))
	assert.False(t, cit.Fields[3].NumericValidation.FormatAsCurrency, "a rate is not a currency")

	wht := tpls[2]
	assert.Equal(t, []string{"whtBase", "whtRate", "whtAmount"}, fieldIDs(wht.Fields))
}

// Every figure the tax-financial report derives for a tax type (VAT: output /
// input / net VAT; CIT: taxable income / liability; WHT: withheld amount) is a
// NUMERIC field of that type's predefined template, under the canonical key —
// so a template-bound instance can feed the report (the fixture-era ids never
// could).
func TestPredefinedTemplates_FeedTheFinancialReport(t *testing.T) {
	t.Parallel()
	byType := map[string]CreateDataTemplateInput{}
	for _, tpl := range PredefinedTemplates() {
		byType[tpl.TemplateType] = tpl
	}
	for _, taxType := range []string{taxkeys.TaxTypeVAT, taxkeys.TaxTypeCIT, taxkeys.TaxTypeWHT} {
		tpl, ok := byType[taxType]
		require.True(t, ok, "no predefined template for %s", taxType)
		numeric := map[string]bool{}
		for _, f := range tpl.Fields {
			if f.FieldType == FieldTypeNumeric {
				numeric[f.ID] = true
			}
		}
		keys := taxkeys.FigureKeys(taxType)
		require.NotEmpty(t, keys, taxType)
		for _, key := range keys {
			assert.True(t, numeric[key], "%s template must carry numeric field %q", taxType, key)
		}
	}
}

func TestFields_ValueScanRoundTrip(t *testing.T) {
	t.Parallel()
	two := 2
	original := Fields{
		{ID: "a", Name: "A", FieldType: FieldTypeNumeric, Mandatory: true,
			NumericValidation: &NumericValidation{Min: f64(0), AllowDecimals: true, DecimalPlaces: &two, FormatAsCurrency: true}},
		{ID: "b", Name: "B", FieldType: FieldTypeText},
	}
	v, err := original.Value()
	require.NoError(t, err)

	var scanned Fields
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)

	// numericValidation / description are omitted when absent.
	assert.NotContains(t, string(v.([]byte)), `"b","name":"B","fieldType":"text","mandatory":false,"numericValidation"`)
	assert.NotContains(t, string(v.([]byte)), `"description"`)

	var nilFields Fields
	v, err = nilFields.Value()
	require.NoError(t, err)
	assert.Equal(t, "[]", string(v.([]byte)))
	assert.Error(t, scanned.Scan(3.14))
}

func fieldIDs(fields Fields) []string {
	ids := make([]string, 0, len(fields))
	for _, f := range fields {
		ids = append(ids, f.ID)
	}
	return ids
}
