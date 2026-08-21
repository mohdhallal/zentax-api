package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// DueDateRule is a workflow's structured filing due-date rule (an offset from the
// period end / filing deadline), stored as JSONB. json + validate tags let the
// one type serve the model, the DTO, and JSONB persistence (ADR-0017).
type DueDateRule struct {
	Reference         string `json:"reference,omitempty"         validate:"omitempty,oneof=period_end filing_deadline"`
	OffsetUnit        string `json:"offsetUnit,omitempty"        validate:"omitempty,oneof=days weeks months"`
	OffsetValue       int    `json:"offsetValue,omitempty"       validate:"omitempty"`
	OffsetDirection   string `json:"offsetDirection,omitempty"   validate:"omitempty,oneof=before after"`
	WeekendAdjustment string `json:"weekendAdjustment,omitempty" validate:"omitempty,oneof=none next-business-day prev-business-day"`
}

func (r DueDateRule) Value() (driver.Value, error) {
	return json.Marshal(r)
}

func (r *DueDateRule) Scan(src any) error {
	data, err := jsonBytes(src)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		*r = DueDateRule{}
		return nil
	}
	if err := json.Unmarshal(data, r); err != nil {
		return fmt.Errorf("DueDateRule.Scan: invalid JSON: %w", err)
	}
	return nil
}

// Periods is a JSONB-stored list of period codes (e.g. ["M1","Q1"]).
type Periods []string

func (p Periods) Value() (driver.Value, error) {
	if p == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(p))
}

func (p *Periods) Scan(src any) error {
	data, err := jsonBytes(src)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		*p = Periods{}
		return nil
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("Periods.Scan: invalid JSON: %w", err)
	}
	*p = out
	return nil
}

func jsonBytes(src any) ([]byte, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("unsupported JSONB source type %T", src)
	}
}
