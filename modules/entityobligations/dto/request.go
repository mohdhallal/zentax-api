package dto

import "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"

type CreateEntityObligationBody struct {
	EntityID         string              `json:"entityId"         validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	ObligationTypeID string              `json:"obligationTypeId" validate:"required,uuid" example:"6ba7b811-9dad-11d1-80b4-00c04fd430c8"`
	Jurisdiction     *string             `json:"jurisdiction"     validate:"omitempty,max=100" example:"DE"`
	Periodicity      string              `json:"periodicity"      validate:"required,oneof=monthly quarterly bi-annual annual consolidated-annual" example:"monthly"`
	DeadlineRule     domain.DeadlineRule `json:"deadlineRule"     validate:"omitempty"`
}

type UpdateEntityObligationBody struct {
	Jurisdiction *string             `json:"jurisdiction" validate:"omitempty,max=100"`
	Periodicity  string              `json:"periodicity"  validate:"required,oneof=monthly quarterly bi-annual annual consolidated-annual"`
	DeadlineRule domain.DeadlineRule `json:"deadlineRule" validate:"omitempty"`
	Status       string              `json:"status"       validate:"omitempty,oneof=active inactive"`
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
