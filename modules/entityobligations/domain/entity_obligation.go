package domain

import "time"

type EntityObligationID = string

// EntityObligation links an entity to an obligation type with jurisdiction,
// periodicity, and a deadline rule. TenantID is infrastructure (RLS), absent
// from the model. EntityID and ObligationTypeID are fixed at creation.
type EntityObligation struct {
	ID               EntityObligationID `json:"id"               db:"id"`
	EntityID         string             `json:"entityId"         db:"entity_id"`
	ObligationTypeID string             `json:"obligationTypeId" db:"obligation_type_id"`
	Jurisdiction     *string            `json:"jurisdiction"     db:"jurisdiction"`
	Periodicity      string             `json:"periodicity"      db:"periodicity"`
	DeadlineRule     DeadlineRule       `json:"deadlineRule"     db:"deadline_rule"`
	Status           string             `json:"status"           db:"status"`
	CreatedAt        time.Time          `json:"createdAt"        db:"created_at"`
	UpdatedAt        time.Time          `json:"updatedAt"        db:"updated_at"`
}

type CreateEntityObligationInput struct {
	EntityID         string
	ObligationTypeID string
	Jurisdiction     *string
	Periodicity      string
	DeadlineRule     DeadlineRule
}

type UpdateEntityObligationInput struct {
	Jurisdiction *string
	Periodicity  string
	DeadlineRule DeadlineRule
	Status       string
}
