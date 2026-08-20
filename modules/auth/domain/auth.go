package domain

import (
	"time"

	"github.com/google/uuid"
)

// AuthResult carries the outcome of internal credential validation.
type AuthResult struct {
	ServiceName string
}

type InternalAPIKey struct {
	ID         uuid.UUID `db:"id"`
	AppName    string    `db:"app_name"`
	Key        uuid.UUID `db:"key"`
	SecretHash string    `db:"secret_hash"`
	Active     bool      `db:"active"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

type NexusAccountAPIKey struct {
	ID             int       `db:"id"               json:"id"`
	NexusAccountID int       `db:"nexus_account_id" json:"nexusAccountId"`
	APIKey         string    `db:"api_key"          json:"apiKey"`
	APISecret      string    `db:"api_secret"       json:"apiSecret"`
	CreatedAt      time.Time `db:"created_at"       json:"createdAt"`
	UpdatedAt      time.Time `db:"updated_at"       json:"updatedAt"`
}

type StoreNexusAPIKeyInput struct {
	NexusAccountID int
	APIKey         string
	APISecret      string
}
