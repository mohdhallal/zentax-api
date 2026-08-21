package domain

import "time"

type EntityID = string

// Entity is a tax-paying organization. Entities are hierarchical via
// ParentEntityID. TenantID is intentionally absent from the model: it is
// infrastructure enforced by Postgres RLS at the Tx seam (ADR-0004), not a
// domain attribute the application reads or writes.
type Entity struct {
	ID                    EntityID  `json:"id"                    db:"id"`
	ParentEntityID        *string   `json:"parentEntityId"        db:"parent_entity_id"`
	Name                  string    `json:"name"                  db:"name"`
	LegalName             *string   `json:"legalName"             db:"legal_name"`
	Country               string    `json:"country"               db:"country"`
	TaxResidency          *string   `json:"taxResidency"          db:"tax_residency"`
	FiscalCalendarPattern string    `json:"fiscalCalendarPattern" db:"fiscal_calendar_pattern"`
	FinancialYearEnd      *string   `json:"financialYearEnd"      db:"financial_year_end"`
	Status                string    `json:"status"                db:"status"`
	CreatedBy             *string   `json:"createdBy" db:"created_by"`
	UpdatedBy             *string   `json:"updatedBy" db:"updated_by"`
	CreatedAt             time.Time `json:"createdAt"             db:"created_at"`
	UpdatedAt             time.Time `json:"updatedAt"             db:"updated_at"`
}

type CreateEntityInput struct {
	ParentEntityID        *string
	Name                  string
	LegalName             *string
	Country               string
	TaxResidency          *string
	FiscalCalendarPattern string
	FinancialYearEnd      *string
}

type UpdateEntityInput struct {
	ParentEntityID        *string
	Name                  string
	LegalName             *string
	Country               string
	TaxResidency          *string
	FiscalCalendarPattern string
	FinancialYearEnd      *string
	Status                string
}
