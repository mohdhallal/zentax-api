package dto

import "github.com/mohamadhallal/zentax-api/modules/entities/domain"

// Fiscal calendar fields (ADR-0023): fiscalWeekEndDay / fiscalYearEndRule
// anchor the week-based patterns (defaults saturday / nearest); customPeriods
// is the ordered period list of the `custom` pattern (required for it, allowed
// for others). MM-DD strictness, code uniqueness and the custom-requires-
// periods rule are enforced by domain.ValidateFiscalConfig in the use case.
type CreateEntityBody struct {
	ParentEntityID        *string              `json:"parentEntityId"        validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	Name                  string               `json:"name"                  validate:"required,min=1,max=200" example:"Acme GmbH"`
	LegalName             *string              `json:"legalName"             validate:"omitempty,max=200" example:"Acme Gesellschaft mit beschränkter Haftung"`
	Country               string               `json:"country"               validate:"required,min=2,max=100" example:"Germany"`
	TaxResidency          *string              `json:"taxResidency"          validate:"omitempty,max=100" example:"Germany"`
	FiscalCalendarPattern string               `json:"fiscalCalendarPattern" validate:"omitempty,oneof=standard 445 454 544 13-period weekly custom" example:"standard"`
	FinancialYearEnd      *string              `json:"financialYearEnd"      validate:"omitempty,len=5" example:"12-31"`
	FiscalWeekEndDay      string               `json:"fiscalWeekEndDay"      validate:"omitempty,oneof=monday tuesday wednesday thursday friday saturday sunday" example:"saturday"`
	FiscalYearEndRule     string               `json:"fiscalYearEndRule"     validate:"omitempty,oneof=last nearest" example:"nearest"`
	CustomPeriods         domain.CustomPeriods `json:"customPeriods"         validate:"omitempty,max=60,dive"`
}

type UpdateEntityBody struct {
	ParentEntityID        *string              `json:"parentEntityId"        validate:"omitempty,uuid"`
	Name                  string               `json:"name"                  validate:"required,min=1,max=200"`
	LegalName             *string              `json:"legalName"             validate:"omitempty,max=200"`
	Country               string               `json:"country"               validate:"required,min=2,max=100"`
	TaxResidency          *string              `json:"taxResidency"          validate:"omitempty,max=100"`
	FiscalCalendarPattern string               `json:"fiscalCalendarPattern" validate:"omitempty,oneof=standard 445 454 544 13-period weekly custom"`
	FinancialYearEnd      *string              `json:"financialYearEnd"      validate:"omitempty,len=5"`
	FiscalWeekEndDay      string               `json:"fiscalWeekEndDay"      validate:"omitempty,oneof=monday tuesday wednesday thursday friday saturday sunday"`
	FiscalYearEndRule     string               `json:"fiscalYearEndRule"     validate:"omitempty,oneof=last nearest"`
	CustomPeriods         domain.CustomPeriods `json:"customPeriods"         validate:"omitempty,max=60,dive"`
	Status                string               `json:"status"                validate:"omitempty,oneof=active inactive archived"`
}

type EntityIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

// ListEntitiesQuery pages and filters the entity list. search is a literal,
// case-insensitive substring of name or legalName (% _ \ match themselves);
// blank means no search, and the total counts the same predicate as the page.
type ListEntitiesQuery struct {
	Limit   int      `json:"limit"   default:"20" validate:"min=1,max=100" example:"20"`
	Offset  int      `json:"offset"  default:"0"  validate:"min=0" example:"0"`
	Sort    []string `json:"sort"    validate:"omitempty,dive,oneof=createdAt:asc createdAt:desc name:asc name:desc" example:"createdAt:desc"`
	Country *string  `json:"country" filter:"country" validate:"omitempty,max=100" example:"Germany"`
	Status  *string  `json:"status"  filter:"status"  validate:"omitempty,oneof=active inactive archived" example:"active"`
	Search  string   `json:"search"  validate:"omitempty,max=200" example:"Acme"`
}

// PeriodsQuery selects the period list of GET /entities/{id}/periods:
// financialYear is the calendar year the fiscal year ENDS in.
type PeriodsQuery struct {
	Periodicity   string `json:"periodicity"   validate:"required,oneof=monthly quarterly bi-annual annual consolidated-annual weekly" example:"monthly"`
	FinancialYear int    `json:"financialYear" validate:"required,min=1900,max=2200" example:"2026"`
}
