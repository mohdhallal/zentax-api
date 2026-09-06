// Package taxkeys is the single source of the canonical tax-data figure keys:
// the JSON keys under which a task instance's tax_data carries the amounts the
// reports read (tax-financial, raw export, compliance penalty/interest), and
// the legacy alias chains those reports still honour.
//
// The predefined data templates (modules/datatemplates) store their numeric
// fields under exactly these ids, so an instance bound to a predefined
// template feeds the financial report by construction; a custom template may
// use the same ids to do the same. Aliases exist only for free-form tax data
// recorded before the keys were pinned — new writers use the canonical key.
package taxkeys

// Canonical keys — one per figure. Every alias chain below starts with its
// canonical key.
const (
	// VAT / GST return figures.
	SalesTotal = "salesTotal"
	OutputVat  = "outputVat"
	InputVat   = "inputVat"
	NetVat     = "netVat"

	// Corporate income tax computation.
	ProfitBeforeTax = "profitBeforeTax"
	Adjustments     = "adjustments"
	TaxableIncome   = "taxableIncome"
	TaxRate         = "taxRate"
	TaxLiability    = "taxLiability"

	// Withholding tax.
	WhtBase   = "whtBase"
	WhtRate   = "whtRate"
	WhtAmount = "whtAmount"

	// Any other tax type (TP, Custom, no obligation type): the one amount.
	Amount = "amount"

	// Every tax type.
	EngagementCost = "engagementCost"
	PenaltyAmount  = "penaltyAmount"
	InterestAmount = "interestAmount"
)

// Tax types as obligation_types.template / data_templates.template_type name them.
const (
	TaxTypeVAT = "VAT"
	TaxTypeCIT = "CIT"
	TaxTypeWHT = "WHT"
)

// Alias chains as the tax-financial report reads them: the first key whose
// value is a non-zero number wins (the legacy `num(a) || num(b) || num(c)`).
// Canonical key first, then the legacy spellings, in the historical order.
var (
	OutputVatAliases      = []string{OutputVat, "outputTax", "salesVat"}
	InputVatAliases       = []string{InputVat, "inputTax", "purchaseVat"}
	NetVatAliases         = []string{NetVat, "vatPayable"}
	TaxableIncomeAliases  = []string{TaxableIncome, "taxableProfit"}
	TaxLiabilityAliases   = []string{TaxLiability, "taxPayable", "corporateTax"}
	WhtAmountAliases      = []string{WhtAmount, "withholdingTax", "taxWithheld"}
	OtherAmountAliases    = []string{Amount, "totalAmount", "taxAmount"}
	EngagementCostAliases = []string{EngagementCost, "cost", "filingCost"}
	PenaltyAliases        = []string{PenaltyAmount, "penalty"}
	InterestAliases       = []string{InterestAmount, "interest"}
)

// Export pairs: the raw tax-data export reads each figure as the canonical
// camelCase key, then its legacy snake_case twin — no tax-type gating, no
// derivation (the legacy export contract).
var (
	ExportOutputVat      = []string{OutputVat, "output_vat"}
	ExportInputVat       = []string{InputVat, "input_vat"}
	ExportNetVat         = []string{NetVat, "net_vat"}
	ExportTaxableIncome  = []string{TaxableIncome, "taxable_income"}
	ExportTaxLiability   = []string{TaxLiability, "tax_liability"}
	ExportWhtAmount      = []string{WhtAmount, "wht_amount"}
	ExportPenaltyAmount  = []string{PenaltyAmount, "penalty"}
	ExportInterestAmount = []string{InterestAmount, "interest"}
	ExportEngagementCost = []string{EngagementCost, "engagement_cost"}
)

// FigureKeys returns the canonical keys the tax-financial report derives a
// tax type's amounts from — the keys a data template of that type must carry
// as numeric fields for its instances to feed the report. nil for tax types
// without dedicated figures (their amount comes from OtherAmountAliases).
func FigureKeys(taxType string) []string {
	switch taxType {
	case TaxTypeVAT:
		return []string{OutputVat, InputVat, NetVat}
	case TaxTypeCIT:
		return []string{TaxableIncome, TaxLiability}
	case TaxTypeWHT:
		return []string{WhtAmount}
	}
	return nil
}
