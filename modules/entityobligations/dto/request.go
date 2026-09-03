package dto

import "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"

// Periodicity vocabulary: `weekly` is accepted so the obligation can be
// recorded; the deadline engine still fails closed on weekly at workflow
// start (shared/deadline) — recording is not the same as scheduling.
// Currency is ISO 4217 alpha-3 (validated len=3 + uppercase).
type CreateEntityObligationBody struct {
	EntityID           string              `json:"entityId"           validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	ObligationTypeID   string              `json:"obligationTypeId"   validate:"required,uuid" example:"6ba7b811-9dad-11d1-80b4-00c04fd430c8"`
	TaxReferenceNumber *string             `json:"taxReferenceNumber" validate:"omitempty,max=100" example:"DE123456789"`
	Jurisdiction       *string             `json:"jurisdiction"       validate:"omitempty,max=100" example:"Germany"`
	JurisdictionState  *string             `json:"jurisdictionState"  validate:"omitempty,max=100" example:"Bavaria"`
	Currency           *string             `json:"currency"           validate:"omitempty,len=3,uppercase" example:"EUR"`
	Periodicity        string              `json:"periodicity"        validate:"required,oneof=weekly monthly quarterly bi-annual annual consolidated-annual" example:"monthly"`
	DeadlineRule       domain.DeadlineRule `json:"deadlineRule"       validate:"omitempty"`
}

type UpdateEntityObligationBody struct {
	TaxReferenceNumber *string             `json:"taxReferenceNumber" validate:"omitempty,max=100"`
	Jurisdiction       *string             `json:"jurisdiction"       validate:"omitempty,max=100"`
	JurisdictionState  *string             `json:"jurisdictionState"  validate:"omitempty,max=100"`
	Currency           *string             `json:"currency"           validate:"omitempty,len=3,uppercase"`
	Periodicity        string              `json:"periodicity"        validate:"required,oneof=weekly monthly quarterly bi-annual annual consolidated-annual"`
	DeadlineRule       domain.DeadlineRule `json:"deadlineRule"       validate:"omitempty"`
	Status             string              `json:"status"             validate:"omitempty,oneof=active inactive"`
}

type EntityObligationIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

type ListEntityObligationsQuery struct {
	Limit            int      `json:"limit"            default:"20" validate:"min=1,max=100" example:"20"`
	Offset           int      `json:"offset"           default:"0"  validate:"min=0" example:"0"`
	Sort             []string `json:"sort"             validate:"omitempty,dive,oneof=createdAt:asc createdAt:desc periodicity:asc periodicity:desc" example:"createdAt:desc"`
	EntityID         *string  `json:"entityId"         filter:"entity_id" validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	ObligationTypeID *string  `json:"obligationTypeId" filter:"obligation_type_id" validate:"omitempty,uuid" example:"6ba7b811-9dad-11d1-80b4-00c04fd430c8"`
	Status           *string  `json:"status"           filter:"status" validate:"omitempty,oneof=active inactive" example:"active"`
}
