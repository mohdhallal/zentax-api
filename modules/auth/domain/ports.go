package domain

import (
	"context"

	"github.com/google/uuid"
)

type InternalAPIKeyRepo interface {
	GetByKey(ctx context.Context, key uuid.UUID) (*InternalAPIKey, error)
}

type Validator interface {
	Validate(ctx context.Context, key, secret string) (*AuthResult, error)
}

type NexusAccountAPIKeyRepo interface {
	Store(ctx context.Context, input StoreNexusAPIKeyInput) (*NexusAccountAPIKey, error)
	GetByNexusAccountID(ctx context.Context, nexusAccountID int) (*NexusAccountAPIKey, error)
}

type NexusAccountAPIKeyUseCases interface {
	StoreExternalAPIKey(ctx context.Context, input StoreNexusAPIKeyInput) (*NexusAccountAPIKey, error)
	GetByNexusAccountID(ctx context.Context, nexusAccountID int) (*NexusAccountAPIKey, error)
}
