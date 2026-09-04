package domain

import "time"

type EntityID = string

// Entity is a tax-paying organization. Entities are hierarchical via
// ParentEntityID. TenantID is intentionally absent from the model: it is
// infrastructure enforced by Postgres RLS at the Tx seam (ADR-0004), not a
// domain attribute the application reads or writes.
//
// The fiscal calendar (ADR-0023): FiscalCalendarPattern + FinancialYearEnd
// (MM-DD anchor) for every pattern; FiscalWeekEndDay + FiscalYearEndRule
// anchor the week-based patterns; CustomPeriods is the explicit period list
// of the `custom` pattern.
type Entity struct {
	ID                    EntityID      `json:"id"                    db:"id"`
	ParentEntityID        *string       `json:"parentEntityId"        db:"parent_entity_id"`
	Name                  string        `json:"name"                  db:"name"`
	LegalName             *string       `json:"legalName"             db:"legal_name"`
	Country               string        `json:"country"               db:"country"`
	TaxResidency          *string       `json:"taxResidency"          db:"tax_residency"`
	FiscalCalendarPattern string        `json:"fiscalCalendarPattern" db:"fiscal_calendar_pattern"`
	FinancialYearEnd      *string       `json:"financialYearEnd"      db:"financial_year_end"`
	FiscalWeekEndDay      string        `json:"fiscalWeekEndDay"      db:"fiscal_week_end_day"`
	FiscalYearEndRule     string        `json:"fiscalYearEndRule"     db:"fiscal_year_end_rule"`
	CustomPeriods         CustomPeriods `json:"customPeriods"         db:"custom_periods"`
	Status                string        `json:"status"                db:"status"`
	CreatedBy             *string       `json:"createdBy" db:"created_by"`
	UpdatedBy             *string       `json:"updatedBy" db:"updated_by"`
	CreatedAt             time.Time     `json:"createdAt"             db:"created_at"`
	UpdatedAt             time.Time     `json:"updatedAt"             db:"updated_at"`
}

type CreateEntityInput struct {
	ParentEntityID        *string
	Name                  string
	LegalName             *string
	Country               string
	TaxResidency          *string
	FiscalCalendarPattern string
	FinancialYearEnd      *string
	FiscalWeekEndDay      string
	FiscalYearEndRule     string
	CustomPeriods         CustomPeriods
}

type UpdateEntityInput struct {
	ParentEntityID        *string
	Name                  string
	LegalName             *string
	Country               string
	TaxResidency          *string
	FiscalCalendarPattern string
	FinancialYearEnd      *string
	FiscalWeekEndDay      string
	FiscalYearEndRule     string
	CustomPeriods         CustomPeriods
	Status                string
}
