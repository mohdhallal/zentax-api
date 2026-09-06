package domain

import "github.com/mohamadhallal/zentax-api/shared/taxkeys"

// PredefinedTemplates returns the curated templates every tenant receives
// (seeded idempotently by name + category, immutable through the API). Names,
// labels, mandatory flags and numeric validation are the frontend fixtures'
// (client/src/mocks/fixtures/workflow-detail.ts: dt-vat, dt-cit, dt-wht); the
// field ids are the CANONICAL tax-data keys (shared/taxkeys) — the keys the
// tax-financial report, the raw export and the compliance report read — so an
// instance bound to a predefined template feeds the reports by construction.
// (Migration 20260906000021 renamed the fixture-era ids on existing tenants.)
func PredefinedTemplates() []CreateDataTemplateInput {
	return []CreateDataTemplateInput{
		{
			Name:         "VAT Return",
			TemplateType: taxkeys.TaxTypeVAT,
			Category:     CategoryPredefined,
			Description:  strPtr("Standard VAT/GST return figures"),
			Fields: Fields{
				currencyField(taxkeys.SalesTotal, "Total Sales (net)", true),
				currencyField(taxkeys.OutputVat, "Output VAT", true),
				currencyField(taxkeys.InputVat, "Input VAT", true),
				currencyField(taxkeys.NetVat, "Net VAT Payable", false),
			},
		},
		{
			Name:         "Corporate Income Tax",
			TemplateType: taxkeys.TaxTypeCIT,
			Category:     CategoryPredefined,
			Description:  strPtr("Corporate income tax computation"),
			Fields: Fields{
				currencyField(taxkeys.ProfitBeforeTax, "Profit Before Tax", true),
				currencyField(taxkeys.Adjustments, "Tax Adjustments", false),
				currencyField(taxkeys.TaxableIncome, "Taxable Profit", true),
				percentField(taxkeys.TaxRate, "Tax Rate (%)", true),
				currencyField(taxkeys.TaxLiability, "Tax Due", false),
			},
		},
		{
			Name:         "Withholding Tax",
			TemplateType: taxkeys.TaxTypeWHT,
			Category:     CategoryPredefined,
			Description:  strPtr("Withholding tax on outbound payments"),
			Fields: Fields{
				currencyField(taxkeys.WhtBase, "Payment Base", true),
				percentField(taxkeys.WhtRate, "Withholding Rate (%)", true),
				currencyField(taxkeys.WhtAmount, "Withheld Amount", true),
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
