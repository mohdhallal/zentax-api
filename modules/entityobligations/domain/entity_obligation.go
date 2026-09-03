package domain

import "time"

type EntityObligationID = string

// EntityObligation links an entity to an obligation type with jurisdiction,
// periodicity, and a deadline rule. TenantID is infrastructure (RLS), absent
// from the model. EntityID and ObligationTypeID are fixed at creation.
// Jurisdiction is the country; JurisdictionState narrows it to a state /
// province for sub-national taxes. Currency is ISO 4217 alpha-3.
type EntityObligation struct {
	ID                 EntityObligationID `json:"id"                 db:"id"`
	EntityID           string             `json:"entityId"           db:"entity_id"`
	ObligationTypeID   string             `json:"obligationTypeId"   db:"obligation_type_id"`
	TaxReferenceNumber *string            `json:"taxReferenceNumber" db:"tax_reference_number"`
	Jurisdiction       *string            `json:"jurisdiction"       db:"jurisdiction"`
	JurisdictionState  *string            `json:"jurisdictionState"  db:"jurisdiction_state"`
	Currency           *string            `json:"currency"           db:"currency"`
	Periodicity        string             `json:"periodicity"        db:"periodicity"`
	DeadlineRule       DeadlineRule       `json:"deadlineRule"       db:"deadline_rule"`
	Status             string             `json:"status"             db:"status"`
	CreatedBy          *string            `json:"createdBy"          db:"created_by"`
	UpdatedBy          *string            `json:"updatedBy"          db:"updated_by"`
	CreatedAt          time.Time          `json:"createdAt"          db:"created_at"`
	UpdatedAt          time.Time          `json:"updatedAt"          db:"updated_at"`
}

type CreateEntityObligationInput struct {
	EntityID           string
	ObligationTypeID   string
	TaxReferenceNumber *string
	Jurisdiction       *string
	JurisdictionState  *string
	Currency           *string
	Periodicity        string
	DeadlineRule       DeadlineRule
}

type UpdateEntityObligationInput struct {
	TaxReferenceNumber *string
	Jurisdiction       *string
	JurisdictionState  *string
	Currency           *string
	Periodicity        string
	DeadlineRule       DeadlineRule
	Status             string
}
