package scale

import (
	"fmt"

	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// The date planner. It mirrors, rule for rule, what the API's generator does
// at POST /workflows/{id}/start and what the verify oracle recomputes
// (cmd/seed-demo/oracle.go planRecurring + oraclePaymentDeadline):
//
//	periodEnd = the entity calendar's period (standard pattern, the entity's
//	            financial year end, the calendar year the fiscal year ENDS in)
//	filing    = weekendAdjust(periodEnd ± workflow dueDateRule)
//	payment   = paymentOffset  → periodEnd + months (month-end clamped) + days,
//	                             weekend-adjusted with the obligation's own
//	                             adjustment (the workflow's when it has none);
//	            paymentFixedDates → first MM-DD on/after periodEnd, no adjustment;
//	            neither          → filing
//	due       = reference (periodEnd | filing | payment) ± template offset
//
// The dates are used here only to place every instance in a due-date band;
// the writer persists the oracle's own plan, and cmd/seed-demo's tripwire
// test asserts the two agree on every instance.

// currentFinancialYear is the calendar year the fiscal year running at asOf
// ends in: the year of asOf when the year end is still ahead (or today),
// the next one otherwise.
func currentFinancialYear(asOf dateonly.Date, fye string) int {
	mm, dd, err := deadline.ParseMonthDay(fye)
	if err != nil {
		return asOf.Year
	}
	end := dateonly.New(asOf.Year, mm, dd)
	if asOf.Time().After(end.Time()) {
		return asOf.Year + 1
	}
	return asOf.Year
}

// calendarFor resolves the entity's fiscal calendar through the same entry
// point the API's generator uses.
func calendarFor(e spec.Entity, fiscalYear int) (*deadline.Calendar, error) {
	fye := e.FinancialYearEnd
	entity := &entitiesdomain.Entity{FiscalCalendarPattern: e.FiscalCalendarPattern}
	if fye != "" {
		entity.FinancialYearEnd = &fye
	}
	return entitiesdomain.CalendarFor(entity, fiscalYear)
}

// paymentDeadline is ADR-0023 §5 as the oracle recomputes it.
func paymentDeadline(periodEnd, filing dateonly.Date, rule *spec.DeadlineRule, fallback string) (dateonly.Date, error) {
	if rule == nil {
		return filing, nil
	}
	switch {
	case rule.PaymentOffset != nil:
		adjustment := rule.WeekendAdjustment
		if adjustment == "" {
			adjustment = fallback
		}
		raw := deadline.ApplyMonthDayOffset(periodEnd, rule.PaymentOffset.Months, rule.PaymentOffset.Days)
		return deadline.ApplyWeekendAdjustment(raw, adjustment), nil
	case len(rule.PaymentFixedDates) > 0:
		return deadline.FirstFixedDateOnOrAfter(periodEnd, rule.PaymentFixedDates)
	default:
		return filing, nil
	}
}

// planWorkflow computes the instances start would create for one workflow:
// period-major (selectedPeriods order), then template orderIndex.
func planWorkflow(e spec.Entity, rule *spec.DeadlineRule, w spec.Workflow, fiscalYear int) ([]PlannedInstance, error) {
	if w.Periodicity == nil {
		return nil, fmt.Errorf("workflow %s: no periodicity", w.Key)
	}
	cal, err := calendarFor(e, fiscalYear)
	if err != nil {
		return nil, fmt.Errorf("workflow %s: %w", w.Key, err)
	}
	out := make([]PlannedInstance, 0, len(w.SelectedPeriods)*len(w.Templates))
	for _, code := range w.SelectedPeriods {
		period, err := cal.Period(code, *w.Periodicity)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", w.Key, err)
		}
		filing := deadline.ApplyWeekendAdjustment(
			deadline.ApplyOffset(period.End, w.DueDateRule.OffsetValue, w.DueDateRule.OffsetUnit, w.DueDateRule.OffsetDirection),
			w.DueDateRule.WeekendAdjustment,
		)
		payment, err := paymentDeadline(period.End, filing, rule, w.DueDateRule.WeekendAdjustment)
		if err != nil {
			return nil, fmt.Errorf("workflow %s period %s: %w", w.Key, code, err)
		}
		for _, tmpl := range w.Templates {
			ref := period.End
			switch tmpl.DueDateReference {
			case "filing_deadline":
				ref = filing
			case "payment_deadline":
				ref = payment
			}
			out = append(out, PlannedInstance{
				WorkflowKey:     w.Key,
				PeriodCode:      code,
				TemplateKey:     tmpl.Key,
				OrderIndex:      tmpl.OrderIndex,
				PeriodEnd:       period.End,
				FilingDeadline:  filing,
				PaymentDeadline: payment,
				DueDate: deadline.ApplyOffset(ref,
					tmpl.DueDateOffsetValue, tmpl.DueDateOffsetUnit, tmpl.DueDateOffsetDirection),
			})
		}
	}
	return out, nil
}

// fiscalYearBounds is the first and last civil day of a standard fiscal year
// ending in fiscalYear (the workflow's startDate / endDate).
func fiscalYearBounds(e spec.Entity, fiscalYear int) (dateonly.Date, dateonly.Date, error) {
	cal, err := calendarFor(e, fiscalYear)
	if err != nil {
		return dateonly.Date{}, dateonly.Date{}, err
	}
	periods, err := cal.Periods(deadline.PeriodicityAnnual)
	if err != nil || len(periods) == 0 {
		return dateonly.Date{}, dateonly.Date{}, fmt.Errorf("annual period of FY%d: %v", fiscalYear, err)
	}
	return periods[0].Start, periods[0].End, nil
}

// daysBetween is b − a in civil days.
func daysBetween(a, b dateonly.Date) int {
	return int(b.Time().Sub(a.Time()).Hours() / 24)
}

// addDays shifts a civil date.
func addDays(d dateonly.Date, days int) dateonly.Date {
	return dateonly.FromTime(d.Time().AddDate(0, 0, days))
}

// compareDates orders two civil dates (-1, 0, +1).
func compareDates(a, b dateonly.Date) int {
	switch {
	case a.Time().Before(b.Time()):
		return -1
	case a.Time().After(b.Time()):
		return 1
	default:
		return 0
	}
}

// endOfWeek is the Saturday of the week d falls in — the dashboard's "this
// week" boundary.
func endOfWeek(d dateonly.Date) dateonly.Date {
	return addDays(d, 6-int(d.Time().Weekday()))
}
