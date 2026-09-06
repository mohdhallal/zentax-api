package main

// Tax-data figure extraction, recomputed from ADR-0021 rule 6 and the legacy
// alias chains in shared/taxkeys — the numbers the tax-financial report and
// the raw tax-data export must produce from an instance's tax_data.
//
// The report does this in SQL over jsonb; this file does it over the decoded
// JSON object, mirroring what Postgres would see:
//
//   - `tax_data->>'key'` renders the value as text (a JSON number keeps its
//     literal, a string is itself, a boolean is "true"/"false", an absent or
//     null key is SQL NULL);
//   - a value counts as a number only when that text matches the report's
//     numeric regex, else it counts as 0 (the legacy parseFloat → NaN → 0);
//   - an alias chain yields the first key whose numeric value is non-zero.
//
// Money is exact: sums are big.Rat, so nothing rounds on the way to a
// comparison. Only the final comparison against the API's float64 goes
// through a float, which is exactly what Postgres does (exact numeric sum,
// one ::float8 cast at the end).

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/shared/taxkeys"
)

// oracleNumericRegex is the report's own numeric test: a plain decimal, no
// scientific notation, no blanks.
var oracleNumericRegex = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// oracleFigures is the fixed set of amounts a row contributes.
//
// The big.Rat fields are accumulators: copying an oracleFigures by value
// copies slice headers, so figures are passed and stored as *oracleFigures
// and only ever added into a value that nothing else points at.
type oracleFigures struct {
	OutputVat      big.Rat
	InputVat       big.Rat
	NetVat         big.Rat
	TaxableIncome  big.Rat
	TaxLiability   big.Rat
	WhtAmount      big.Rat
	EngagementCost big.Rat
	TotalAmount    big.Rat
}

// add accumulates another row's figures.
func (f *oracleFigures) add(o *oracleFigures) {
	f.OutputVat.Add(&f.OutputVat, &o.OutputVat)
	f.InputVat.Add(&f.InputVat, &o.InputVat)
	f.NetVat.Add(&f.NetVat, &o.NetVat)
	f.TaxableIncome.Add(&f.TaxableIncome, &o.TaxableIncome)
	f.TaxLiability.Add(&f.TaxLiability, &o.TaxLiability)
	f.WhtAmount.Add(&f.WhtAmount, &o.WhtAmount)
	f.EngagementCost.Add(&f.EngagementCost, &o.EngagementCost)
	f.TotalAmount.Add(&f.TotalAmount, &o.TotalAmount)
}

// field returns one figure by its JSON name (the diff walks them by name).
func (f *oracleFigures) field(name string) *big.Rat {
	switch name {
	case "outputVat":
		return &f.OutputVat
	case "inputVat":
		return &f.InputVat
	case "netVat":
		return &f.NetVat
	case "taxableIncome":
		return &f.TaxableIncome
	case "taxLiability":
		return &f.TaxLiability
	case "whtAmount":
		return &f.WhtAmount
	case "engagementCost":
		return &f.EngagementCost
	case "totalAmount":
		return &f.TotalAmount
	}
	return nil
}

// oracleFigureFields is the order figures are diffed in.
var oracleFigureFields = []string{
	"outputVat", "inputVat", "netVat", "taxableIncome",
	"taxLiability", "whtAmount", "engagementCost", "totalAmount",
}

// oracleTaxFinancialFigures extracts the figures the tax-financial report
// derives from one instance: gated by the obligation's tax type, with netVat
// falling back to output − input when the return does not state it, and
// totalAmount picked per tax type.
func oracleTaxFinancialFigures(taxType string, data map[string]any) *oracleFigures {
	f := &oracleFigures{}
	isVAT := taxType == taxkeys.TaxTypeVAT
	isCIT := taxType == taxkeys.TaxTypeCIT
	isWHT := taxType == taxkeys.TaxTypeWHT

	if isVAT {
		f.OutputVat.Set(oracleFirstNonZero(data, taxkeys.OutputVatAliases))
		f.InputVat.Set(oracleFirstNonZero(data, taxkeys.InputVatAliases))
	}
	if isCIT {
		f.TaxableIncome.Set(oracleFirstNonZero(data, taxkeys.TaxableIncomeAliases))
		f.TaxLiability.Set(oracleFirstNonZero(data, taxkeys.TaxLiabilityAliases))
	}
	if isWHT {
		f.WhtAmount.Set(oracleFirstNonZero(data, taxkeys.WhtAmountAliases))
	}
	// Every tax type carries the engagement cost.
	f.EngagementCost.Set(oracleFirstNonZero(data, taxkeys.EngagementCostAliases))

	// netVatGiven is NULL unless the return states a non-zero net figure.
	var netVatGiven *big.Rat
	if isVAT {
		if given := oracleFirstNonZero(data, taxkeys.NetVatAliases); given.Sign() != 0 {
			netVatGiven = given
		}
	}
	var derivedNet big.Rat
	derivedNet.Sub(&f.OutputVat, &f.InputVat)

	switch {
	case netVatGiven != nil:
		f.NetVat.Set(netVatGiven)
	case isVAT:
		f.NetVat.Set(&derivedNet)
	default:
		f.NetVat.SetInt64(0)
	}

	switch {
	case isVAT:
		f.TotalAmount.Set(&f.NetVat) // COALESCE(net_vat_given, output − input)
	case isCIT:
		f.TotalAmount.Set(&f.TaxLiability)
	case isWHT:
		f.TotalAmount.Set(&f.WhtAmount)
	default:
		// TP / Custom / no obligation type: the single "amount" chain.
		f.TotalAmount.Set(oracleFirstNonZero(data, taxkeys.OtherAmountAliases))
	}
	return f
}

// oracleExportTaxDataFigures extracts the RAW export's figures: the canonical
// key then its snake_case twin, no tax-type gating and no derivation.
type oracleExportFigures struct {
	OutputVat      big.Rat
	InputVat       big.Rat
	NetVat         big.Rat
	TaxableIncome  big.Rat
	TaxLiability   big.Rat
	WhtAmount      big.Rat
	PenaltyAmount  big.Rat
	InterestAmount big.Rat
	EngagementCost big.Rat
}

func oracleExportTaxDataFigures(data map[string]any) *oracleExportFigures {
	f := &oracleExportFigures{}
	f.OutputVat.Set(oracleFirstNonZero(data, taxkeys.ExportOutputVat))
	f.InputVat.Set(oracleFirstNonZero(data, taxkeys.ExportInputVat))
	f.NetVat.Set(oracleFirstNonZero(data, taxkeys.ExportNetVat))
	f.TaxableIncome.Set(oracleFirstNonZero(data, taxkeys.ExportTaxableIncome))
	f.TaxLiability.Set(oracleFirstNonZero(data, taxkeys.ExportTaxLiability))
	f.WhtAmount.Set(oracleFirstNonZero(data, taxkeys.ExportWhtAmount))
	f.PenaltyAmount.Set(oracleFirstNonZero(data, taxkeys.ExportPenaltyAmount))
	f.InterestAmount.Set(oracleFirstNonZero(data, taxkeys.ExportInterestAmount))
	f.EngagementCost.Set(oracleFirstNonZero(data, taxkeys.ExportEngagementCost))
	return f
}

// oraclePenaltyInterest is the compliance report's penaltyInterest column:
// the tax-data penalty / interest figures when either is JS-truthy, else a
// payment task's notes, else "".
func oraclePenaltyInterest(inst *oracleInstance) string {
	penalty, hasPenalty := oracleFirstTruthy(inst.TaxData, taxkeys.PenaltyAliases)
	interest, hasInterest := oracleFirstTruthy(inst.TaxData, taxkeys.InterestAliases)
	if hasPenalty || hasInterest {
		parts := make([]string, 0, 2)
		if hasPenalty {
			parts = append(parts, "Penalty: "+penalty)
		}
		if hasInterest {
			parts = append(parts, "Interest: "+interest)
		}
		return strings.Join(parts, ", ") // concat_ws skips the absent half
	}
	if inst.TaskType == "payment" && inst.Notes != "" {
		return inst.Notes
	}
	return ""
}

// oracleFirstNonZero mirrors the SQL alias chain: the first key whose numeric
// value is non-zero, else zero. The returned value is never nil and must not
// be mutated by the caller.
func oracleFirstNonZero(data map[string]any, keys []string) *big.Rat {
	for _, k := range keys {
		if v := oracleNum(data, k); v.Sign() != 0 {
			return v
		}
	}
	return new(big.Rat)
}

// oracleNum is `CASE WHEN text ~ numericRegex THEN text::numeric ELSE 0 END`.
func oracleNum(data map[string]any, key string) *big.Rat {
	text, ok := oracleJSONText(data, key)
	if !ok || !oracleNumericRegex.MatchString(text) {
		return new(big.Rat)
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return new(big.Rat)
	}
	return r
}

// oracleFirstTruthy is the alias-chain form of the JS-truthy presence test.
func oracleFirstTruthy(data map[string]any, keys []string) (string, bool) {
	for _, k := range keys {
		if v, ok := oracleTruthy(data, k); ok {
			return v, true
		}
	}
	return "", false
}

// oracleTruthy mirrors jsonTruthy: a number that is not 0, a boolean that is
// not false, or a non-empty string — as text. Anything else (absent, null,
// object, array) is absent.
func oracleTruthy(data map[string]any, key string) (string, bool) {
	raw, present := data[key]
	if !present || raw == nil {
		return "", false
	}
	text, ok := oracleJSONText(data, key)
	if !ok {
		return "", false
	}
	switch oracleJSONType(raw) {
	case "number":
		if text == "0" || text == "0.0" {
			return "", false
		}
		return text, true
	case "boolean":
		if text == "false" {
			return "", false
		}
		return text, true
	case "string":
		if text == "" {
			return "", false
		}
		return text, true
	default:
		// object / array: jsonb_typeof falls through to NULL.
		return "", false
	}
}

// oracleJSONType is jsonb_typeof over a decoded value.
func oracleJSONType(raw any) string {
	switch raw.(type) {
	case json.Number, float64, float32, int, int64:
		return "number"
	case bool:
		return "boolean"
	case string:
		return "string"
	case nil:
		return "null"
	default:
		return "other"
	}
}

// oracleJSONText renders one key the way `tax_data->>'key'` does. ok is false
// when the key is absent or JSON null (SQL NULL).
func oracleJSONText(data map[string]any, key string) (string, bool) {
	raw, present := data[key]
	if !present || raw == nil {
		return "", false
	}
	switch v := raw.(type) {
	case json.Number:
		return v.String(), true
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	default:
		// Objects and arrays render as their JSON text; nothing numeric or
		// truthy ever matches, which is what the SQL does too.
		b, err := json.Marshal(raw)
		if err != nil {
			return fmt.Sprint(raw), true
		}
		return string(b), true
	}
}

// oracleRatFloat converts an exact amount to the float64 the API serializes
// (Postgres sums exactly, then casts once — this is that one cast).
func oracleRatFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

// oracleRatString renders an amount for a difference line.
func oracleRatString(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return strconv.FormatFloat(oracleRatFloat(r), 'f', -1, 64)
}

// oracleParseRat reads an exact decimal (a spec expectation) as a rational.
func oracleParseRat(n json.Number) (*big.Rat, error) {
	if n == "" {
		return new(big.Rat), nil
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return nil, fmt.Errorf("not a number: %q", n.String())
	}
	return r, nil
}
