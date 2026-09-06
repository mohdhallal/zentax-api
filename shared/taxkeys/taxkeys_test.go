package taxkeys

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every alias chain starts with its canonical key, carries no duplicates, and
// the export pairs lead with the same canonical key the report reads — the
// invariants the SQL builders rely on.
func TestAliasChains_LeadWithTheCanonicalKey(t *testing.T) {
	t.Parallel()
	chains := map[string][]string{
		OutputVat:      OutputVatAliases,
		InputVat:       InputVatAliases,
		NetVat:         NetVatAliases,
		TaxableIncome:  TaxableIncomeAliases,
		TaxLiability:   TaxLiabilityAliases,
		WhtAmount:      WhtAmountAliases,
		Amount:         OtherAmountAliases,
		EngagementCost: EngagementCostAliases,
		PenaltyAmount:  PenaltyAliases,
		InterestAmount: InterestAliases,
	}
	for canonical, chain := range chains {
		require.NotEmpty(t, chain, canonical)
		assert.Equal(t, canonical, chain[0], "chain must lead with its canonical key")
		seen := map[string]bool{}
		for _, k := range chain {
			assert.False(t, seen[k], "duplicate alias %q in %s", k, canonical)
			seen[k] = true
		}
	}

	pairs := map[string][]string{
		OutputVat: ExportOutputVat, InputVat: ExportInputVat, NetVat: ExportNetVat,
		TaxableIncome: ExportTaxableIncome, TaxLiability: ExportTaxLiability, WhtAmount: ExportWhtAmount,
		PenaltyAmount: ExportPenaltyAmount, InterestAmount: ExportInterestAmount, EngagementCost: ExportEngagementCost,
	}
	for canonical, pair := range pairs {
		require.Len(t, pair, 2, canonical)
		assert.Equal(t, canonical, pair[0])
	}
}

// The figures a tax type reports are the heads of that type's alias chains.
func TestFigureKeys_ArePerTaxType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{OutputVat, InputVat, NetVat}, FigureKeys(TaxTypeVAT))
	assert.Equal(t, []string{TaxableIncome, TaxLiability}, FigureKeys(TaxTypeCIT))
	assert.Equal(t, []string{WhtAmount}, FigureKeys(TaxTypeWHT))
	assert.Nil(t, FigureKeys("TP"))
	assert.Nil(t, FigureKeys("Custom"))
	assert.Nil(t, FigureKeys(""))
}
