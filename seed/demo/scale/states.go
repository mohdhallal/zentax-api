package scale

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	"github.com/mohamadhallal/zentax-api/shared/taxkeys"
)

// The state of every task instance is drawn from the seeded PRNG by the
// instance's due-date band relative to AsOf, so that on the generation day —
// and on any run day for as long as the fixture's fiscal years straddle it —
// every dashboard tile and every compliance classification is non-empty:
//
//	band          due − asOf     completed  late¹  in_progress  pending²  in_review  blocked  not_started
//	far past      < −60 days        90%      20%       3%          —          1%        2%         4%
//	recent past   −60 … −1          60%      25%      15%          8%         5%        3%         9%
//	today         0                 20%       —       40%         10%         5%        —         25%
//	near future   1 … 30            10%       —       25%          5%         3%        —         57%
//	far future    > 30               —        —        3%          —          —         —         97%
//
// ¹ share of the completed that finish 1–20 days after the due date (never
// after AsOf); the rest finish 0–5 days before it. A completion in the today
// or near-future band is on time by construction (dated AsOf or earlier).
// ² pending_approval needs a template with approvalRequired; on any other
// template the draw becomes in_review. Completed approval templates finish
// via approve (submitted the day before), everything else via put.
//
// Assignees: every non-default instance carries one (the spec requires it);
// a plain not_started instance is assigned with probability 0.6 and then
// listed as a deviation. Round-robin over the assignable users.

// Band names.
const (
	BandFarPast    = "far_past"
	BandRecentPast = "recent_past"
	BandToday      = "today"
	BandNearFuture = "near_future"
	BandFarFuture  = "far_future"
)

// Compliance classifications (ADR-0021 rule 5), as Stats reports them.
const (
	ClassOnTime = "on_time"
	ClassLate   = "late"
	ClassMissed = "missed"
	ClassNotDue = "not_due"
)

const (
	statusNotStarted      = "not_started"
	statusInProgress      = "in_progress"
	statusInReview        = "in_review"
	statusPendingApproval = "pending_approval"
	statusCompleted       = "completed"
	statusBlocked         = "blocked"
)

// bandOf places a due date relative to asOf.
func bandOf(due, asOf dateonly.Date) string {
	days := daysBetween(asOf, due)
	switch {
	case days < -60:
		return BandFarPast
	case days < 0:
		return BandRecentPast
	case days == 0:
		return BandToday
	case days <= 30:
		return BandNearFuture
	default:
		return BandFarFuture
	}
}

// statusMix is one band's cumulative status distribution (percent) and the
// share of its completions that are late.
type statusMix struct {
	completed, inProgress, pending, inReview, blocked int // percent, in draw order
	lateShare                                         float64
}

var bandMixes = map[string]statusMix{
	BandFarPast:    {completed: 90, inProgress: 3, pending: 0, inReview: 1, blocked: 2, lateShare: 0.20},
	BandRecentPast: {completed: 60, inProgress: 15, pending: 8, inReview: 5, blocked: 3, lateShare: 0.25},
	BandToday:      {completed: 20, inProgress: 40, pending: 10, inReview: 5, blocked: 0},
	BandNearFuture: {completed: 10, inProgress: 25, pending: 5, inReview: 3, blocked: 0},
	BandFarFuture:  {completed: 0, inProgress: 3, pending: 0, inReview: 0, blocked: 0},
}

// drawStatus picks a status for the band (not_started is the remainder).
func (g *generator) drawStatus(band string, approvalTemplate bool) string {
	mix := bandMixes[band]
	roll := g.rng.IntN(100)
	limit := mix.completed
	if roll < limit {
		return statusCompleted
	}
	limit += mix.inProgress
	if roll < limit {
		return statusInProgress
	}
	limit += mix.pending
	if roll < limit {
		if approvalTemplate {
			return statusPendingApproval
		}
		return statusInReview
	}
	limit += mix.inReview
	if roll < limit {
		return statusInReview
	}
	limit += mix.blocked
	if roll < limit {
		return statusBlocked
	}
	return statusNotStarted
}

// instanceState draws the state of one planned instance. It returns the
// deviation to list on the workflow and whether there is one (a not_started,
// unassigned, data-less instance is the default and is not listed).
//
// forceCompleted is the workflow-level override (a `completed` workflow has
// no open instance).
func (g *generator) instanceState(
	p PlannedInstance, ot obligationTypeDef, td templateDef, e entity, forceCompleted bool,
) (spec.Instance, bool) {
	asOf := g.cfg.AsOf
	band := bandOf(p.DueDate, asOf)
	status := g.drawStatus(band, td.approval)
	if forceCompleted {
		status = statusCompleted
	}

	inst := spec.Instance{
		Period: p.PeriodCode,
		Task:   p.TemplateKey,
		Status: status,
		Via:    spec.ViaPut,
	}

	switch status {
	case statusCompleted:
		inst.CompletedOn = g.completionDate(band, p.DueDate)
		if td.approval {
			inst.Via = spec.ViaApprove
			inst.SubmittedOn = addDays(inst.CompletedOn, -1)
		}
		// Prepare tasks: 70 % of the completed ones carry tax data.
		if td.key == "prepare" && g.chance(0.70) {
			inst.TaxData = g.taxData(ot, e)
			inst.TaxDataStatus = "final"
		}
	case statusPendingApproval:
		inst.Via = spec.ViaSubmit
		// Submitted recently: the day before AsOf, or the day after the
		// instance became due when that is earlier.
		inst.SubmittedOn = addDays(asOf, -1)
		if compareDates(p.DueDate, asOf) < 0 && g.chance(0.5) {
			inst.SubmittedOn = addDays(p.DueDate, 1)
		}
	}

	// Notes on 5 % of payment tasks.
	if td.taskType == "payment" && g.chance(0.05) {
		g.noteSeq++
		inst.Notes = fmt.Sprintf("Paid by bank transfer, reference %s-%s-%06d.",
			ot.code, e.country.taxPrefix, g.noteSeq)
	}

	listed := status != statusNotStarted || inst.Notes != ""
	if !listed && g.chance(0.60) {
		listed = true
	}
	if listed {
		inst.Assignee = g.nextAssignee()
	}
	return inst, listed
}

// completionDate dates a completion for its band: late ones 1–20 days after
// the due date (capped at AsOf), on-time ones 0–5 days before it (today and
// future bands: at or up to three days before AsOf).
func (g *generator) completionDate(band string, due dateonly.Date) dateonly.Date {
	asOf := g.cfg.AsOf
	switch band {
	case BandToday, BandNearFuture, BandFarFuture:
		return addDays(asOf, -g.between(0, 3))
	}
	if g.chance(bandMixes[band].lateShare) {
		late := addDays(due, g.between(1, 20))
		if compareDates(late, asOf) > 0 {
			late = asOf
		}
		return late
	}
	return addDays(due, -g.between(0, 5))
}

// taxData draws plausible figures under the canonical keys (shared/taxkeys)
// for the obligation's tax type — the keys the predefined data templates
// carry and the tax-financial report reads.
func (g *generator) taxData(ot obligationTypeDef, e entity) map[string]any {
	num := func(v int) json.Number { return json.Number(strconv.Itoa(v)) }
	switch ot.template {
	case taxkeys.TaxTypeVAT:
		sales := g.between(50, 2000) * 1000
		output := sales * e.country.vatRate / 100
		input := output * g.between(40, 90) / 100
		return map[string]any{
			taxkeys.SalesTotal: num(sales),
			taxkeys.OutputVat:  num(output),
			taxkeys.InputVat:   num(input),
			taxkeys.NetVat:     num(output - input),
		}
	case taxkeys.TaxTypeWHT:
		base := g.between(20, 800) * 1000
		rates := []int{5, 10, 15, 20}
		rate := rates[g.rng.IntN(len(rates))]
		return map[string]any{
			taxkeys.WhtBase:   num(base),
			taxkeys.WhtRate:   num(rate),
			taxkeys.WhtAmount: num(base * rate / 100),
		}
	case taxkeys.TaxTypeCIT:
		profit := g.between(100, 5000) * 1000
		adjustments := (g.between(-10, 10) * profit) / 100
		rates := []int{15, 19, 21, 25, 30}
		rate := rates[g.rng.IntN(len(rates))]
		taxable := profit + adjustments
		return map[string]any{
			taxkeys.ProfitBeforeTax: num(profit),
			taxkeys.Adjustments:     num(adjustments),
			taxkeys.TaxableIncome:   num(taxable),
			taxkeys.TaxRate:         num(rate),
			taxkeys.TaxLiability:    num(taxable * rate / 100),
		}
	case "TP":
		return map[string]any{
			taxkeys.Amount:         num(g.between(5, 60) * 1000),
			taxkeys.EngagementCost: num(g.between(8, 40) * 1000),
		}
	default: // Custom (ENV)
		return map[string]any{
			taxkeys.Amount: num(g.between(2, 90) * 500),
		}
	}
}

// classify is ADR-0021 rule 5 on the due date at AsOf: on_time / late for a
// completion, missed / not_due otherwise.
func classify(inst spec.Instance, due, asOf dateonly.Date) string {
	if inst.Status == statusCompleted && !inst.CompletedOn.IsZero() {
		if compareDates(inst.CompletedOn, due) <= 0 {
			return ClassOnTime
		}
		return ClassLate
	}
	if compareDates(due, asOf) < 0 {
		return ClassMissed
	}
	return ClassNotDue
}
