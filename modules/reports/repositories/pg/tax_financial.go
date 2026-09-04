package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
)

// figuresCTE is the ONE filtered set every part of the tax-financial report
// derives from: participating instances that carry tax data (the legacy
// hasData rule — an instance is included when any figure is non-zero OR its
// tax_data has at least one key; the first implies the second, so the
// predicate is simply "tax_data is a non-empty object"). Figures come from a
// FIXED key set per tax type with the legacy alias chains, cast safely
// (ADR-0021 rule 6). $4 is the groupBy, resolved here into group_key /
// group_label so the aggregate statement can GROUP BY them.
var figuresCTE = `
WITH raw AS (
    SELECT ti.id,
           COALESCE(e.name, 'Unknown Entity') AS entity_name,
           COALESCE(e.id::text, '') AS entity_id,
           COALESCE(e.country, '') AS country,
           COALESCE(ot.template, '') AS tax_type,
           COALESCE(ot.name, 'Unknown') AS obligation_name,
           COALESCE(ot.code, '') AS obligation_code,
           COALESCE(ot.id::text, '') AS obligation_type_id,
           ti.period_code AS period,
           COALESCE(w.financial_year, '') AS financial_year,
           CASE WHEN ot.template = 'VAT' THEN ` + firstNonZero("outputVat", "outputTax", "salesVat") + ` ELSE 0 END AS output_vat,
           CASE WHEN ot.template = 'VAT' THEN ` + firstNonZero("inputVat", "inputTax", "purchaseVat") + ` ELSE 0 END AS input_vat,
           CASE WHEN ot.template = 'VAT' THEN COALESCE(NULLIF(` + jsonNum("netVat") + `, 0), NULLIF(` + jsonNum("vatPayable") + `, 0)) END AS net_vat_given,
           CASE WHEN ot.template = 'CIT' THEN ` + firstNonZero("taxableIncome", "taxableProfit") + ` ELSE 0 END AS taxable_income,
           CASE WHEN ot.template = 'CIT' THEN ` + firstNonZero("taxLiability", "taxPayable", "corporateTax") + ` ELSE 0 END AS tax_liability,
           CASE WHEN ot.template = 'WHT' THEN ` + firstNonZero("whtAmount", "withholdingTax", "taxWithheld") + ` ELSE 0 END AS wht_amount,
           CASE WHEN COALESCE(ot.template, '') NOT IN ('VAT', 'CIT', 'WHT') THEN ` + firstNonZero("amount", "totalAmount", "taxAmount") + ` ELSE 0 END AS other_amount,
           ` + firstNonZero("engagementCost", "cost", "filingCost") + ` AS engagement_cost
    FROM task_instances ti
    JOIN workflows w ON w.id = ti.workflow_id
    LEFT JOIN entities e ON e.id = w.entity_id
    LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id
    WHERE ` + participatingWorkflows + reportFiltersWhere + `
      AND ti.tax_data IS NOT NULL AND ti.tax_data <> '{}'::jsonb -- planner-estimable (null_frac / MCV); the API only ever stores objects
),
figures AS (
    SELECT raw.*,
           COALESCE(net_vat_given, CASE WHEN tax_type = 'VAT' THEN output_vat - input_vat ELSE 0 END) AS net_vat,
           CASE tax_type
               WHEN 'VAT' THEN COALESCE(net_vat_given, output_vat - input_vat)
               WHEN 'CIT' THEN tax_liability
               WHEN 'WHT' THEN wht_amount
               ELSE other_amount
           END AS total_amount,
           CASE $4::text
               WHEN '` + domain.GroupByCountry + `'    THEN COALESCE(NULLIF(country, ''), 'unknown')
               WHEN '` + domain.GroupByTaxType + `'    THEN COALESCE(NULLIF(tax_type, ''), 'unknown')
               WHEN '` + domain.GroupByPeriod + `'     THEN period
               WHEN '` + domain.GroupByObligation + `' THEN COALESCE(NULLIF(obligation_type_id, ''), 'unknown')
               ELSE COALESCE(NULLIF(entity_id, ''), 'unknown')
           END AS group_key,
           CASE $4::text
               WHEN '` + domain.GroupByCountry + `'    THEN COALESCE(NULLIF(country, ''), 'Unknown')
               WHEN '` + domain.GroupByTaxType + `'    THEN COALESCE(NULLIF(tax_type, ''), 'Unknown')
               WHEN '` + domain.GroupByPeriod + `'     THEN period
               WHEN '` + domain.GroupByObligation + `' THEN obligation_name
               ELSE entity_name
           END AS group_label
    FROM raw
)
`

// Page of rows (numeric → float8 so the JSON carries numbers, never strings).
var financialRowsSQL = figuresCTE + `
SELECT entity_name, entity_id, country, tax_type, obligation_name, obligation_code, period, financial_year,
       output_vat::float8, input_vat::float8, net_vat::float8, taxable_income::float8,
       tax_liability::float8, wht_amount::float8, engagement_cost::float8, total_amount::float8
FROM figures
ORDER BY entity_name, obligation_name, period, id
LIMIT $5 OFFSET $6`

// One aggregate statement yields the three exact roll-ups via GROUPING SETS:
// grp 1 = per group_key (aggregated), grp 2 = per period (chartData),
// grp 3 = grand total (summary + totalCount). Ordered so Go can split them.
var financialAggregateSQL = figuresCTE + `
SELECT GROUPING(group_key, period)::int AS grp,
       COALESCE(group_key, '') AS group_key,
       COALESCE(group_label, '') AS group_label,
       COALESCE(period, '') AS period,
       COALESCE(SUM(output_vat), 0)::float8 AS output_vat,
       COALESCE(SUM(input_vat), 0)::float8 AS input_vat,
       COALESCE(SUM(net_vat), 0)::float8 AS net_vat,
       COALESCE(SUM(taxable_income), 0)::float8 AS taxable_income,
       COALESCE(SUM(tax_liability), 0)::float8 AS tax_liability,
       COALESCE(SUM(wht_amount), 0)::float8 AS wht_amount,
       COALESCE(SUM(engagement_cost), 0)::float8 AS engagement_cost,
       COALESCE(SUM(total_amount), 0)::float8 AS total_amount,
       COUNT(*)::int AS cnt
FROM figures
GROUP BY GROUPING SETS ((group_key, group_label), (period), ())
ORDER BY grp, group_label, group_key, period`

type financialAggRow struct {
	Grp    int    `db:"grp"`
	Key    string `db:"group_key"`
	Label  string `db:"group_label"`
	Period string `db:"period"`
	domain.Figures
	Count int `db:"cnt"`
}

const (
	grpByGroup  = 1 // GROUPING(group_key, period) = 0b01: period rolled up
	grpByPeriod = 2 // 0b10: group_key rolled up
	grpTotal    = 3 // 0b11: everything rolled up
)

func (r *ReportsRepo) TaxFinancial(ctx context.Context, args domain.TaxFinancialArgs) (*domain.TaxFinancialResult, error) {
	params := append(filterArgs(args.ReportFilters), args.GroupBy)
	res := &domain.TaxFinancialResult{
		Rows:       []domain.FinancialRow{},
		Aggregated: []domain.FinancialGroup{},
		ChartData:  []domain.FinancialPeriodPoint{},
	}
	if err := r.db.SelectContext(ctx, &res.Rows, financialRowsSQL,
		append(params, args.Limit, args.Offset)...); err != nil {
		return nil, err
	}

	aggs := []financialAggRow{}
	if err := r.db.SelectContext(ctx, &aggs, financialAggregateSQL, params...); err != nil {
		return nil, err
	}
	for i := range aggs {
		a := &aggs[i]
		switch a.Grp {
		case grpByGroup:
			res.Aggregated = append(res.Aggregated, domain.FinancialGroup{
				Key: a.Key, Label: a.Label, Figures: a.Figures, Count: a.Count,
			})
		case grpByPeriod:
			res.ChartData = append(res.ChartData, domain.FinancialPeriodPoint{Period: a.Period, Figures: a.Figures})
		case grpTotal:
			res.Summary = domain.FinancialSummary{Figures: a.Figures, RecordCount: a.Count}
			res.TotalCount = a.Count
		}
	}
	return res, nil
}
