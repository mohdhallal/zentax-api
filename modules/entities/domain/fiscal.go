package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/mohamadhallal/zentax-api/shared/deadline"
)

// Fiscal calendar defaults (ADR-0023): the NRF convention — weeks end on a
// Saturday and the fiscal year ends on the Saturday nearest the anchor.
const (
	DefaultFiscalCalendarPattern = deadline.PatternStandard
	DefaultFiscalWeekEndDay      = "saturday"
	DefaultFiscalYearEndRule     = deadline.YearEndRuleNearest
)

// CustomPeriod is one period of a `custom` fiscal calendar: a stable code
// (the period code workflows select), a display name and MM-DD start / end
// dates placed into the fiscal year by the engine. json + validate tags let
// the one type serve the model, the DTO and JSONB persistence (ADR-0017).
type CustomPeriod struct {
	Code      string `json:"code"      validate:"required,min=1,max=16"  example:"P1"`
	Name      string `json:"name"      validate:"required,min=1,max=100" example:"Period 1"`
	StartDate string `json:"startDate" validate:"required,len=5"         example:"01-01"`
	EndDate   string `json:"endDate"   validate:"required,len=5"         example:"01-28"`
}

// CustomPeriods is the JSONB-stored, ordered period list of a custom calendar.
// nil stores as SQL NULL (the column is nullable — most entities have none).
type CustomPeriods []CustomPeriod

func (p CustomPeriods) Value() (driver.Value, error) {
	if len(p) == 0 {
		return nil, nil
	}
	return json.Marshal([]CustomPeriod(p))
}

func (p *CustomPeriods) Scan(src any) error {
	var data []byte
	switch v := src.(type) {
	case nil:
		*p = nil
		return nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("CustomPeriods.Scan: unsupported source type %T", src)
	}
	if len(data) == 0 {
		*p = nil
		return nil
	}
	var out []CustomPeriod
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("CustomPeriods.Scan: invalid JSON: %w", err)
	}
	*p = out
	return nil
}

// FiscalConfig is the calendar-relevant slice of an entity, shared by create
// and update validation.
type FiscalConfig struct {
	FiscalCalendarPattern string
	FinancialYearEnd      *string
	FiscalWeekEndDay      string
	FiscalYearEndRule     string
	CustomPeriods         CustomPeriods
}

// ValidateFiscalConfig enforces the cross-field rules the tag validator cannot
// express: financialYearEnd and every custom period date are strict MM-DD,
// custom period codes are unique within the entity, and the `custom` pattern
// carries at least one period (other patterns may keep a list). Returns a
// human-readable message (empty when valid).
func ValidateFiscalConfig(c FiscalConfig) string {
	if c.FinancialYearEnd != nil && *c.FinancialYearEnd != "" {
		if _, _, err := deadline.ParseMonthDay(*c.FinancialYearEnd); err != nil {
			return "financialYearEnd must be a valid MM-DD date"
		}
	}
	if c.FiscalWeekEndDay != "" && !deadline.IsWeekEndDay(c.FiscalWeekEndDay) {
		return "fiscalWeekEndDay must be one of monday … sunday"
	}
	if c.FiscalYearEndRule != "" && c.FiscalYearEndRule != deadline.YearEndRuleLast && c.FiscalYearEndRule != deadline.YearEndRuleNearest {
		return "fiscalYearEndRule must be 'last' or 'nearest'"
	}
	if c.FiscalCalendarPattern == deadline.PatternCustom && len(c.CustomPeriods) == 0 {
		return "a custom fiscal calendar requires at least one custom period"
	}
	seen := make(map[string]bool, len(c.CustomPeriods))
	for i, p := range c.CustomPeriods {
		if p.Code == "" || len(p.Code) > 16 {
			return fmt.Sprintf("customPeriods[%d].code must be 1..16 characters", i)
		}
		if p.Name == "" || len(p.Name) > 100 {
			return fmt.Sprintf("customPeriods[%d].name must be 1..100 characters", i)
		}
		if seen[p.Code] {
			return fmt.Sprintf("customPeriods[%d].code %q is not unique within the entity", i, p.Code)
		}
		seen[p.Code] = true
		if _, _, err := deadline.ParseMonthDay(p.StartDate); err != nil {
			return fmt.Sprintf("customPeriods[%d].startDate must be a valid MM-DD date", i)
		}
		if _, _, err := deadline.ParseMonthDay(p.EndDate); err != nil {
			return fmt.Sprintf("customPeriods[%d].endDate must be a valid MM-DD date", i)
		}
	}
	return ""
}

// CalendarFor builds the deadline engine's Calendar for an entity and a
// fiscal year (the calendar year it ends in). It is THE way the generator and
// GET /entities/{id}/periods obtain periods, so both agree (ADR-0001/0023).
func CalendarFor(e *Entity, fiscalYear int) (*deadline.Calendar, error) {
	fyEnd := ""
	if e.FinancialYearEnd != nil {
		fyEnd = *e.FinancialYearEnd
	}
	custom := make([]deadline.CustomPeriod, 0, len(e.CustomPeriods))
	for _, p := range e.CustomPeriods {
		custom = append(custom, deadline.CustomPeriod{Code: p.Code, Name: p.Name, StartDate: p.StartDate, EndDate: p.EndDate})
	}
	return deadline.NewCalendar(e.FiscalCalendarPattern, fyEnd, e.FiscalWeekEndDay, e.FiscalYearEndRule, custom, fiscalYear)
}
