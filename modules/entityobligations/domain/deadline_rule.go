package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// DeadlineRule is the deadline-calculation config for an entity obligation,
// stored as a JSONB column (ADR-0017: rules are versioned data, not pinned to
// columns). It carries json tags (JSONB round-trip) AND validate tags (the API
// contract), and implements sql.Scanner / driver.Valuer for the JSONB column,
// so the one type serves the model, the DTO, and persistence.
//
// Two strategies coexist, mirroring the legacy deadline builder
// (client/src/lib/deadline-utils.ts):
//   - "fixed": one or two calendar dates per year (annual / bi-annual /
//     consolidated-annual) — FixedDates are the filing dates, PaymentFixedDates
//     the parallel payment dates.
//   - "period_offset": for periodicities with more than two occurrences a year
//     (weekly / monthly / quarterly) — a reporting-period start, a filing offset
//     after period end, an optional distinct payment offset (nil = payment is
//     due with the filing), weekend adjustment and extra deadlines.
//
// Reference / OffsetUnit / OffsetValue / OffsetDirection are the original
// single-offset form and stay valid for callers that only need one offset.
type DeadlineRule struct {
	// Type selects the strategy: "fixed" calendar dates, or a "period_offset"
	// computed from the period end / filing deadline.
	Type              string   `json:"type,omitempty"              validate:"omitempty,oneof=fixed period_offset"`
	Reference         string   `json:"reference,omitempty"         validate:"omitempty,oneof=period_end filing_deadline"`
	OffsetUnit        string   `json:"offsetUnit,omitempty"        validate:"omitempty,oneof=days weeks months"`
	OffsetValue       int      `json:"offsetValue,omitempty"       validate:"omitempty"`
	OffsetDirection   string   `json:"offsetDirection,omitempty"   validate:"omitempty,oneof=before after"`
	WeekendAdjustment string   `json:"weekendAdjustment,omitempty" validate:"omitempty,oneof=none next-business-day prev-business-day"`
	FixedDates        []string `json:"fixedDates,omitempty"        validate:"omitempty,dive,len=5"` // MM-DD filing dates, legal date-only (ADR-0002)
	// PaymentFixedDates parallels FixedDates (index i pays for filing i). MM-DD.
	PaymentFixedDates []string `json:"paymentFixedDates,omitempty" validate:"omitempty,dive,len=5"`
	// PeriodStart is the day/month the reporting period cycle begins (period_offset).
	PeriodStart *PeriodStart `json:"periodStart,omitempty"`
	// FilingOffset is the delay after period end until the filing is due.
	FilingOffset *MonthDayOffset `json:"filingOffset,omitempty"`
	// PaymentOffset is the delay after period end until payment is due; nil
	// means "payment is due with the filing".
	PaymentOffset *MonthDayOffset `json:"paymentOffset,omitempty"`
	// AdditionalDeadlines are extra per-period deadlines (e.g. an advance
	// payment) expressed as offsets after period end.
	AdditionalDeadlines []AdditionalDeadline `json:"additionalDeadlines,omitempty" validate:"omitempty,dive"`
}

// PeriodStart is a calendar day-of-year (no year): the start of the reporting
// period cycle. Month is 1-12 (January = 1).
type PeriodStart struct {
	Day   int `json:"day"   validate:"min=1,max=31"`
	Month int `json:"month" validate:"min=1,max=12"`
}

// MonthDayOffset is a "N months + M days after period end" delay.
type MonthDayOffset struct {
	Months int `json:"months" validate:"min=0,max=24"`
	Days   int `json:"days"   validate:"min=0,max=366"`
}

// AdditionalDeadline is an extra deadline of a named kind, offset from period end.
type AdditionalDeadline struct {
	Type   string `json:"type"   validate:"required,max=50"`
	Months int    `json:"months" validate:"min=0,max=24"`
	Days   int    `json:"days"   validate:"min=0,max=366"`
}

// Value serializes the rule to JSON for the JSONB column.
func (r DeadlineRule) Value() (driver.Value, error) {
	return json.Marshal(r)
}

// Scan deserializes a JSONB value into the rule.
func (r *DeadlineRule) Scan(src any) error {
	if src == nil {
		*r = DeadlineRule{}
		return nil
	}

	var data []byte
	switch v := src.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("DeadlineRule.Scan: unsupported source type %T", src)
	}

	if len(data) == 0 {
		*r = DeadlineRule{}
		return nil
	}
	if err := json.Unmarshal(data, r); err != nil {
		return fmt.Errorf("DeadlineRule.Scan: invalid JSON: %w", err)
	}
	return nil
}
