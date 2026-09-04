package domain

// PredefinedTemplates returns the curated templates every tenant receives
// (seeded idempotently by name + category, immutable through the API). Ported
// verbatim from the frontend fixtures (client/src/mocks/fixtures/
// workflow-detail.ts: dt-vat, dt-cit, dt-wht) — same names, types, field ids,
// labels and validation, so a tenant's tax data keys are stable across
// environments.
func PredefinedTemplates() []CreateDataTemplateInput {
	return []CreateDataTemplateInput{
		{
			Name:         "VAT Return",
			TemplateType: "VAT",
			Category:     CategoryPredefined,
			Description:  strPtr("Standard VAT/GST return figures"),
			Fields: Fields{
				currencyField("f-vat-sales", "Total Sales (net)", true),
				currencyField("f-vat-output", "Output VAT", true),
				currencyField("f-vat-input", "Input VAT", true),
				currencyField("f-vat-net", "Net VAT Payable", false),
			},
		},
		{
			Name:         "Corporate Income Tax",
			TemplateType: "CIT",
			Category:     CategoryPredefined,
			Description:  strPtr("Corporate income tax computation"),
			Fields: Fields{
				currencyField("f-cit-pbt", "Profit Before Tax", true),
				currencyField("f-cit-adj", "Tax Adjustments", false),
				currencyField("f-cit-taxable", "Taxable Profit", true),
				percentField("f-cit-rate", "Tax Rate (%)", true),
				currencyField("f-cit-due", "Tax Due", false),
			},
		},
		{
			Name:         "Withholding Tax",
			TemplateType: "WHT",
			Category:     CategoryPredefined,
			Description:  strPtr("Withholding tax on outbound payments"),
			Fields: Fields{
				currencyField("f-wht-base", "Payment Base", true),
				percentField("f-wht-rate", "Withholding Rate (%)", true),
				currencyField("f-wht-amount", "Withheld Amount", true),
			},
		},
	}
}

// currencyField mirrors the fixture's currency() helper: numeric, 2 decimals,
// formatted as currency.
func currencyField(id, name string, mandatory bool) Field {
	return Field{
		ID: id, Name: name, FieldType: FieldTypeNumeric, Mandatory: mandatory,
		NumericValidation: &NumericValidation{AllowDecimals: true, DecimalPlaces: intPtr(2), FormatAsCurrency: true},
	}
}

// percentField is the fixture's inline rate field: numeric, 2 decimals, not a
// currency.
func percentField(id, name string, mandatory bool) Field {
	return Field{
		ID: id, Name: name, FieldType: FieldTypeNumeric, Mandatory: mandatory,
		NumericValidation: &NumericValidation{AllowDecimals: true, DecimalPlaces: intPtr(2), FormatAsCurrency: false},
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
