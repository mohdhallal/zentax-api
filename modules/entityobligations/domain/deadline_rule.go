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
type DeadlineRule struct {
	// Type selects the strategy: "fixed" calendar dates, or a "period_offset"
	// computed from the period end / filing deadline.
	Type              string   `json:"type,omitempty"              validate:"omitempty,oneof=fixed period_offset"`
	Reference         string   `json:"reference,omitempty"         validate:"omitempty,oneof=period_end filing_deadline"`
	OffsetUnit        string   `json:"offsetUnit,omitempty"        validate:"omitempty,oneof=days weeks months"`
	OffsetValue       int      `json:"offsetValue,omitempty"       validate:"omitempty"`
	OffsetDirection   string   `json:"offsetDirection,omitempty"   validate:"omitempty,oneof=before after"`
	WeekendAdjustment string   `json:"weekendAdjustment,omitempty" validate:"omitempty,oneof=none next-business-day prev-business-day"`
	FixedDates        []string `json:"fixedDates,omitempty"        validate:"omitempty,dive,len=5"` // MM-DD, legal date-only (ADR-0002)
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
