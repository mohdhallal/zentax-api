package domain

import "time"

type ObligationTypeID = string

// ObligationType is a tax obligation definition (VAT/CIT/TP/WHT/Custom), either
// curated-predefined or tenant-custom. TenantID is infrastructure (RLS), absent
// from the model. Code is unique per tenant.
type ObligationType struct {
	ID          ObligationTypeID `json:"id"          db:"id"`
	Name        string           `json:"name"        db:"name"`
	Code        string           `json:"code"        db:"code"`
	Category    string           `json:"category"    db:"category"`
	Template    string           `json:"template"    db:"template"`
	Status      string           `json:"status"      db:"status"`
	Description *string          `json:"description" db:"description"`
	CreatedAt   time.Time        `json:"createdAt"   db:"created_at"`
	UpdatedAt   time.Time        `json:"updatedAt"   db:"updated_at"`
}

type CreateObligationTypeInput struct {
	Name        string
	Code        string
	Category    string
	Template    string
	Description *string
}

type UpdateObligationTypeInput struct {
	Name        string
	Code        string
	Category    string
	Template    string
	Status      string
	Description *string
}
