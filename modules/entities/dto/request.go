package dto

type CreateEntityBody struct {
	ParentEntityID        *string `json:"parentEntityId"        validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	Name                  string  `json:"name"                  validate:"required,min=1,max=200" example:"Acme GmbH"`
	LegalName             *string `json:"legalName"             validate:"omitempty,max=200" example:"Acme Gesellschaft mit beschränkter Haftung"`
	Country               string  `json:"country"               validate:"required,min=2,max=100" example:"Germany"`
	TaxResidency          *string `json:"taxResidency"          validate:"omitempty,max=100" example:"Germany"`
	FiscalCalendarPattern string  `json:"fiscalCalendarPattern" validate:"omitempty,oneof=standard 445 454 544 13-period weekly custom" example:"standard"`
	FinancialYearEnd      *string `json:"financialYearEnd"      validate:"omitempty,len=5" example:"12-31"`
}

type UpdateEntityBody struct {
	ParentEntityID        *string `json:"parentEntityId"        validate:"omitempty,uuid"`
	Name                  string  `json:"name"                  validate:"required,min=1,max=200"`
	LegalName             *string `json:"legalName"             validate:"omitempty,max=200"`
	Country               string  `json:"country"               validate:"required,min=2,max=100"`
	TaxResidency          *string `json:"taxResidency"          validate:"omitempty,max=100"`
	FiscalCalendarPattern string  `json:"fiscalCalendarPattern" validate:"omitempty,oneof=standard 445 454 544 13-period weekly custom"`
	FinancialYearEnd      *string `json:"financialYearEnd"      validate:"omitempty,len=5"`
	Status                string  `json:"status"                validate:"omitempty,oneof=active inactive archived"`
}

type EntityIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

type ListEntitiesQuery struct {
	Limit   int      `json:"limit"   default:"20" validate:"min=1,max=100" example:"20"`
	Offset  int      `json:"offset"  default:"0"  validate:"min=0" example:"0"`
	Sort    []string `json:"sort"    validate:"omitempty,dive,oneof=createdAt:asc createdAt:desc name:asc name:desc" example:"createdAt:desc"`
	Country *string  `json:"country" filter:"country" validate:"omitempty,max=100" example:"Germany"`
	Status  *string  `json:"status"  filter:"status"  validate:"omitempty,oneof=active inactive archived" example:"active"`
}
